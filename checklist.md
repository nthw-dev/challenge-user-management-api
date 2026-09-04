# Requirement checklist

Every item in [challenge-user-management-api.md](challenge-user-management-api.md): done or not, how, and where in the code.
✅ done · ➕ done and taken further · ⬜ not done, with the reason.

**7 / 7 requirements · 6 / 6 bonus · 4 / 4 deliverables.**

## 1. User model ✅

| Field | |
| --- | --- |
| `ID` | MongoDB `ObjectId`, exposed as 24 hex chars; the domain holds a plain `string` — [`user.go`](internal/domain/user/user.go), [`model.go`](internal/adapter/outbound/mongo/model.go) |
| `Name` | 1–100 characters, counted in runes, so a Thai name is not "too long" |
| `Email` | A value object: trimmed, lowercased, `net/mail`-parsed — [`email.go`](internal/domain/user/email.go). Normalizing in one place is what keeps `A@x.com` and `a@x.com` from both passing the unique index |
| `Password` | Never stored; only `PasswordHash` from bcrypt |
| `CreatedAt` | From an injected `Clock`, UTC, millisecond precision (all BSON keeps) |

`user.New` checks name → email → password, reports **every** failing field together (`ValidationErrors`), and only then hashes.
The struct carries no `bson`/`json` tags — mapping lives in the adapters. The domain imports stdlib only;
[`deps_test.go`](internal/domain/user/deps_test.go) fails the build otherwise.
Tests: [`user_test.go`](internal/domain/user/user_test.go), [`email_test.go`](internal/domain/user/email_test.go), [`password_test.go`](internal/domain/user/password_test.go) — 97.1%.

## 2. Authentication ✅

| | |
| --- | --- |
| Registration | `POST /auth/register` → `UserUseCase.Create` — there is no separate Register use case |
| Login returns a JWT | `POST /auth/login` → [`AuthService.Login`](internal/app/auth_service.go): access token, refresh token, user |
| Endpoints protected, validated in middleware | [`middleware/auth.go`](internal/adapter/inbound/http/middleware/auth.go) wraps `/users/*`; `/auth/*` stays public. Verification is pure computation, no DB |
| HS256 with a secret | [`token/jwt.go`](internal/adapter/outbound/token/jwt.go); `JWT_SECRET` must be ≥ 32 bytes |

Libraries: `golang-jwt/jwt/v5`, `x/crypto/bcrypt`. Beyond the brief:

- **Algorithm pinned** ([`jwt.go:75`](internal/adapter/outbound/token/jwt.go#L75)); `alg: none`, HS512, a foreign secret, wrong `iss`/`aud` all covered in [`jwt_test.go`](internal/adapter/outbound/token/jwt_test.go).
- **No personal data in the payload** — `sub`, `exp`, `iat`, `nbf`, `iss`, `aud`, `jti`, `typ`.
- **Self-only authorization** — the verified `sub` is the actor; `PATCH`/`DELETE` on any other account is 403, decided before the row is read ([`requireSelf`](internal/app/user_service.go)). `actor_id` on every authenticated log line.
- **Refresh tokens are not JWTs** — 32 random bytes, stored as SHA-256 with a TTL index. Rotation claims the old token first (a CAS), so twenty concurrent refreshes yield exactly one session ([`concurrency_test.go`](test/integration/concurrency_test.go)); reusing a rotated token wipes every session; deleting the account revokes them.
- **Uniform login failure**, with a decoy bcrypt compare on the unknown-email path so timing leaks nothing.

Guide: [docs/jwt-guide.md](docs/jwt-guide.md).

## 3. User operations ✅

All five on both transports, one set of use cases: [`user_service.go`](internal/app/user_service.go), [`http/user_handler.go`](internal/adapter/inbound/http/user_handler.go), [`grpc/user_service.go`](internal/adapter/inbound/grpc/user_service.go).

- ➕ `PATCH` with `*string` fields, so "not sent" ≠ "empty". Empty body → 422.
- ➕ Keyset pagination (`limit` 20/100, `next_cursor` null on the last page) — page 500 costs the same as page 1.
- ➕ `?query=` search by name or email, regex escaped; same name as `ListUsersRequest.query`.

Spec: [docs/rest-api.md](docs/rest-api.md), [docs/grpc-api.md](docs/grpc-api.md).

## 4. MongoDB ✅

Official driver `go.mongodb.org/mongo-driver` v1.17.9, no ODM — [`internal/adapter/outbound/mongo/`](internal/adapter/outbound/mongo/). Each repository builds its own indexes at boot.

- Uniqueness is the **unique index**, not a pre-check; `E11000` → `ErrEmailTaken` → 409. Twenty simultaneous registrations of one email give one 201 and one document — tested, not assumed.
- TTL index on `refresh_tokens.expires_at`; projection drops `password_hash` on every read that does not need it; write concern majority.

Tests on real MongoDB via testcontainers, a private database per test: [`user_repo_test.go`](test/integration/user_repo_test.go), [`refresh_repo_test.go`](test/integration/refresh_repo_test.go), the full journey in [`journey_test.go`](test/integration/journey_test.go), and [`concurrency_test.go`](test/integration/concurrency_test.go). `make test-int`.

## 5. Middleware ✅

[`logging.go`](internal/adapter/inbound/http/middleware/logging.go) — `log/slog`, one line per request:

```json
{"msg":"http_request","method":"POST","path":"/api/v1/auth/login","status":200,"duration_ms":183,"bytes":412,"request_id":"01M1…","remote_ip":"172.18.0.1","actor_id":"…"}
```

`path` not the full URL (no query values in the log); `duration_ms` a number; the wrapper implements `Flush`/`Unwrap`.
➕ Six more, and the order matters: `RequestID → RealIP → Recoverer → Logging → Metrics → MaxBytes`, then `Authenticate` on the protected group.
`Recoverer` outside `Logging`, or a panic gets no log line. gRPC mirrors it: `recovery → logging → errors → timeout → auth`.

## 6. Concurrency task ✅

[`worker/user_counter.go`](internal/adapter/inbound/worker/user_counter.go) logs `user_count total=N` every 10 s (`USER_COUNT_INTERVAL`), supervised by `errgroup` in [`lifecycle.go`](cmd/api/lifecycle.go).
It calls `UserUseCase.Count` like any adapter; `ctx.Done()` is inside the `select` so shutdown is immediate; each round has its own 5 s deadline; a failed count logs and continues; the gauge is a plain callback so the worker never imports Prometheus. 100% covered; `-race` is always on because of it.

## 7. Testing ✅

**130 test functions, 262 subtests, 49 Ginkgo specs** on `testing` + testify, plus mockery mocks of every port.
MongoDB is mocked **at the interfaces we own**, never the driver: fakes for logic ([`fake_test.go`](internal/app/fake_test.go), [`apptest/`](internal/app/apptest/)), mocks for interaction ([`apptest/mocks/`](internal/app/apptest/mocks/)), real MongoDB for queries.

➕ Four tiers: unit · parity (same cases through REST and gRPC, compared) · integration (repos, full journey, concurrency) · black-box scripts against a running server.
➕ [`deps_test.go`](internal/domain/user/deps_test.go) fails CI if the domain imports anything outside stdlib.

Coverage (`make cover-report`, 2026-09-04): domain **97.1%**, app **97.3%**, hand-written code **93.0%**. Details: [docs/testing.md](docs/testing.md).

## Bonus

**Containerization ✅** — multi-stage [`Dockerfile`](Dockerfile) (static, distroless, non-root), [`docker-compose.yml`](docker-compose.yml) with a Mongo healthcheck the API waits on. ➕ Mongoku at :3100 to see the data.

**Abstraction ✅** — every port in [`ports.go`](internal/app/ports.go), consumer-side; adapters assert `var _ app.X = (*Y)(nil)`.

**Validation ✅** — no validator library; the rules live in the domain so both transports reject identically. Name 1–100 runes · email parsed, dotted domain, ≤ 254 · password ≥ 8 runes and not common · bcrypt's 72-byte cap in the bcrypt adapter · bad JSON 400, > 1 MB 413 · `limit` in `ListFilter.Resolve`. Every failing field is reported at once. One envelope, mapped once per transport by [`apierr.Classify`](internal/adapter/inbound/apierr/apierr.go).

**Graceful shutdown ✅** — `signal.NotifyContext` in [`main.go`](cmd/api/main.go#L91); [`lifecycle.go`](cmd/api/lifecycle.go) first flips `/readyz` to 503 `draining` and gRPC health to `NOT_SERVING`, keeps serving for `SHUTDOWN_DELAY`, then `GracefulStop` + `Shutdown` on a fresh timeout context (`SHUTDOWN_TIMEOUT`). `/healthz` stays 200 so the pod is not restarted mid-drain.

**gRPC ➕** — asked for `CreateUser` and `GetUser`; delivered all eight rpcs across two services, auth via metadata with the same `TokenVerifier`, self-only writes as `PERMISSION_DENIED`, a deadline ceiling, reflection in dev only, and a browser console at `/grpcui/` behind the `dev` tag. [docs/grpc.md](docs/grpc.md).

**Hexagonal ✅** — three inbound adapters (HTTP, gRPC, worker) share one use-case pair; adding gRPC changed nothing in domain or app. `cmd/api` is the only place that names a concrete adapter. [docs/architecture.md](docs/architecture.md).

## Deliverables

| | |
| --- | --- |
| Git repo | With CI ([`ci.yml`](.github/workflows/ci.yml)): lint + race unit tests, integration, Docker build |
| README | [README.md](README.md) — `make up` and links |
| JWT guide | [docs/jwt-guide.md](docs/jwt-guide.md) |
| Sample requests | [docs/rest-api.md](docs/rest-api.md) and [docs/grpc-api.md](docs/grpc-api.md), plus Swagger UI, the gRPC console, and `make test-rest` / `make test-grpc` which log every exchange |
| Assumptions and decisions | [docs/design-decisions.md](docs/design-decisions.md) |

## Evaluation criteria — where to look

- **Code quality** — [`ports.go`](internal/app/ports.go) shows the system in one screen; [`router.go`](internal/adapter/inbound/http/router.go) shows why middleware order matters. golangci-lint with depguard in CI.
- **REST correctness** — all five operations plus register/login/refresh, health, readiness, metrics; `make test-rest` asserts every case.
- **Security / JWT** — pinned algorithm, no PII in the token, bcrypt 12, uniform login failure with a timing decoy, self-only authorization, claim-first refresh rotation, reuse wipes sessions, delete revokes them, body cap, slowloris and RPC deadline ceilings, internals never returned.
- **MongoDB** — official driver behind a port; unique index + `E11000` → 409; TTL index; keyset paging; projection; majority. Verified on real MongoDB, including the races.
- **Tests and mocking** — 130 tests / 262 subtests / 49 specs, `-race`; domain 97.1%, app 97.3%; fakes at our ports, real DB for queries.
- **Idiomatic Go** — `log/slog`, sentinel errors + typed `ErrValidation`, `context` first, small consumer-side interfaces, no globals, `errgroup`, table-driven subtests, build tags.
- **Bonus** — all six.

### Knowingly stopped short

- ⬜ **No roles** — self-only authorization; an admin would extend `requireSelf` and add a claim.
- ⬜ **No rate limiting** — belongs at the ingress or in Redis.
- ⬜ **No email verification / password reset** — needs a mail port, outside the brief.
- ⬜ **No access-token denylist** — `jti` is there if needed; `tokens_valid_after` is cheaper.
- ⬜ **No distributed tracing** — a request id on every line is enough for one service.
- ⬜ **No migration tool** — indexes at boot; would stall a deploy on a big collection.
- ⬜ **`?query=` is `$regex`** — fine here; a text index or Atlas Search for real.

Reasons: [docs/design-decisions.md](docs/design-decisions.md).
