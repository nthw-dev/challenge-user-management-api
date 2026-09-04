# Design decisions and assumptions

[README](../README.md) · [Checklist](../checklist.md) · [Architecture](architecture.md) · [Configuration](configuration.md) · [JWT](jwt-guide.md) · [REST API](rest-api.md) · [gRPC API](grpc-api.md) · [gRPC tooling](grpc.md) · [Testing](testing.md) · [Dependencies](dependencies.md)

## Architecture

- **Ports are declared in `app`, consumer-side.** A use case asks for `UserRepository`, never for a `*mongo.Collection`. That is what makes this hexagonal rather than just foldered, and what makes a fake fit.
- **Who is calling is an argument, not a context value.** `Update(ctx, actorID, id, …)` and `Delete(ctx, actorID, id)` take the verified subject explicitly, so "only your own account" is visible in the signature and checkable by a mock. Transports carry the id in the context via the small `actor` package. The one wrinkle: the request log line is written by the outermost middleware, which cannot see a value set further in — so the logging layer reserves a slot and the auth layer fills it.
- **One error vocabulary.** `apierr.Classify` turns a core error into a code, a safe message and the failing fields, once. REST maps the code to a status, gRPC to a code — nothing else. Before this the two transports had drifted (gRPC said which auth stage failed; REST deliberately did not).
- **Every failing field at once.** `user.New` collects name/email/password into a `ValidationErrors` (which `Unwrap`s to each `ErrValidation`, so `errors.Is` still works). A form-facing API that reports one field per round trip is a poor one.
- **Paging rules live in the use case** (`ListFilter.Resolve`). A limit that was sent — zero included — must be in range; an absent one gets the default. Transports only parse.
- **The worker goes through a use case**, so it is just another inbound adapter and tests with the same fakes.
- **No globals, no `init()` that connects.** Everything is wired in `cmd/api`, one function per boot stage; both ports are bound before anything starts, so a busy address fails the boot rather than a goroutine.
- **Readiness flips before the listeners close.** Shutdown first makes `/readyz` answer 503 `draining` and gRPC health `NOT_SERVING`, keeps serving for `SHUTDOWN_DELAY`, then drains. Closing first would fail whatever the load balancer had already sent. `/healthz` stays 200 — flipping liveness would get the pod restarted mid-drain.
- **Response DTOs are separate structs.** An entity is never marshalled out, so `password_hash` cannot leak by oversight; reads that do not need it also project it away.

## Data

- **Email uniqueness is the unique index.** A check-then-insert loses the race between two requests. We catch `E11000` and answer 409 — and fire twenty registrations at once in a test to prove it.
- **`Email` is a value object**: lowercased in one place, or `A@x.com` and `a@x.com` would both pass the index.
- **Times are UTC, truncated to milliseconds** — all BSON can store, so a value reads back as it was written.
- **No transactions**: every command touches one document. **Write concern majority**: worth the latency for account data.
- **`CountDocuments`, not `EstimatedDocumentCount`** — accuracy over speed at this size.
- **Indexes are built at boot by the repository that owns them.** Idempotent, so restarts are safe — but on a large collection this stalls a deploy; a real system moves it to a migration step.
- **Refresh rotation claims the old token first** (a compare-and-swap on `revoked_at`), then issues. Two concurrent refreshes have exactly one winner; the loser gets 401 with no wipe — it raced itself, nothing leaked. Only presenting a token *already known* to be rotated wipes every session. The cost: if storing the new token fails after the claim, the caller logs in again. The earlier order (issue, then revoke) could end a lost race in a 500 with a second live token behind — a forced login is the better trade.
- **A wrong password and a broken comparison are different errors.** A mismatch is `ErrPasswordMismatch`; a corrupt stored hash is a 500 with a log line, not a permanent 401.
- **Deletes are real**, as the brief says, and they revoke the user's refresh tokens with the row. Audit requirements would call for a soft delete.
- **`?query=` is an escaped `$regex`**, which cannot use an index. Fine here; a real system wants a text index or Atlas Search.

## Assumptions

- **Self-only authorization, no roles.** Anyone authenticated may read, list and create; changing or deleting is your own account only, refused with 403 *before* the row is read so the answer cannot probe for accounts. A role would extend `requireSelf` and add a claim, without touching the transports.
- **No email verification** — signing up makes the account usable at once.
- **A malformed id is 422, not 404** — a format error, and the message does not say "ObjectId".
- **No rate limiting** — `/auth/*` is unthrottled; that belongs at the ingress or in Redis.
- **`RealIP` trusts `X-Forwarded-For`** — fine behind a trusted proxy, forgeable without one.
- **The compose `JWT_SECRET` is a dev value**; `/metrics` is unprotected. Both are ingress/secret-manager concerns in production.

## Considered and rejected

The full list of dependencies, versions and alternatives is in [dependencies.md](dependencies.md); the big calls:

| | Why not |
| --- | --- |
| Gin / Fiber | Their own context instead of `http.Handler`; chi is plain `net/http` |
| An ODM (mgm, mongox) | Hides the queries; the brief asks for the official driver |
| RS256 | Right when several services verify without sharing a secret; the brief says HS256, and the `token` package can swap later |
| Session cookies | The brief wants a self-verifying token that also works over gRPC |
| zap / zerolog | `log/slog` is in the stdlib and already structured |
| A ULID library | Under 60 lines by hand, no dependency |

## Deliberately not built

- **Roles / permissions** — guessing a model in advance usually guesses wrong; the seam is there.
- **Email verification, password reset** — need an outbound mail port, outside the brief.
- **Access-token denylist** — `jti` is in the token if ever needed; a `tokens_valid_after` field on the user is cheaper.
- **End-to-end tests over a real socket in `go test`** — the in-process journey on real MongoDB already covers the stack; `make test-rest` / `make test-grpc` hit a running server for the rest.
- **Distributed tracing** — a request id on every log line is enough for one service.
- **A migration tool** — only `EnsureIndexes` at boot, with the caveat above.
