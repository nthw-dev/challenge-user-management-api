# Dependencies — what, which version, and why

[README](../README.md) · [Checklist](../checklist.md) · [Architecture](architecture.md) · [Configuration](configuration.md) · [Decisions](design-decisions.md) · [Testing](testing.md)

Every direct dependency in [`go.mod`](../go.mod), with the reason it is there and what it was picked over.
The default answer to "should we add a library" is no: the standard library does most of this job, and every
import is something to patch, audit and upgrade for the life of the service.

## Rules

- **Pinned everywhere.** `go.mod` for libraries, `make tools-install` for tools, `.github/workflows/ci.yml` for actions. CI fails on a `go.mod`/`go.sum` that is not tidy, and regenerates protobuf and mocks to diff them — a version drift shows up as a failed build, not a surprise.
- **Vulnerabilities are checked on every push** (`govulncheck` in CI). Every dependency was at its latest stable when this was written (`go get -u -t ./...`).
- **Nothing in the core.** `internal/domain` imports the standard library only; `internal/app` imports the domain only. `depguard` enforces both. Every library below lives in an adapter, `cmd/api`, or a test.
- **Dev-only weight stays out of the production binary.** grpcui and its descriptor stack are behind the `dev` build tag; `make build` does not link them (32.1 vs 35.9 MB).

## Runtime

| Package | Version | Why this one | Instead of |
| --- | --- | --- | --- |
| `go` | 1.27.1 | Latest stable; `errors.Join`/multi-`Unwrap` (Go 1.20) is what `ValidationErrors` builds on, `log/slog` (1.21) is the logger, range-over-int is used where it reads better | An older toolchain — no reason to |
| [`go-chi/chi/v5`](https://github.com/go-chi/chi) | 5.3.2 | Plain `net/http`: handlers are `http.Handler`, middleware is `func(http.Handler) http.Handler`, so nothing framework-specific leaks into the adapter and `httptest` drives the real router. Route patterns give Prometheus a bounded `route` label | Gin/Fiber (own context types, own middleware signatures); `http.ServeMux` alone (no middleware chaining, no route pattern for metrics) |
| [`go.mongodb.org/mongo-driver`](https://github.com/mongodb/mongo-go-driver) | 1.17.9 | The official driver the brief asks for. Queries stay visible; `IsDuplicateKeyError`, `FindOneAndUpdate`, index builders and TTL are all first-class. Stayed on the 1.x line: v2 changes the API surface without adding anything this service needs, and every integration test would need touching for no behaviour gain | An ODM (mgm, mongox) — hides the queries a reviewer needs to see |
| [`golang-jwt/jwt/v5`](https://github.com/golang-jwt/jwt) | 5.3.1 | The maintained successor of `dgrijalva/jwt-go`; `WithValidMethods`, `WithIssuer`, `WithAudience`, `WithExpirationRequired`, `WithLeeway` and `WithTimeFunc` make the safe parse explicit and testable with an injected clock | `lestrrat-go/jwx` (larger, JWK/JWE we do not need); hand-rolled HMAC (no) |
| `golang.org/x/crypto` | 0.56.0 | `bcrypt` — cost-tunable, the standard for password hashing, and its 72-byte limit is handled where it belongs (the hasher adapter) | argon2id — better on paper, but bcrypt is what the brief and most reviewers expect, and cost 12 is plenty here |
| `golang.org/x/sync` | 0.22.0 | `errgroup` supervises the HTTP server, the gRPC server, the worker and the shutdown gatekeeper: one failing goroutine cancels the rest, and `Wait` gives one error to log | A hand-written WaitGroup + error channel — errgroup is that, done right |
| [`google.golang.org/grpc`](https://github.com/grpc/grpc-go) · `protobuf` · `genproto/googleapis/rpc` | 1.83.2 · 1.36.12 · 2026-08-31 | The gRPC transport, the generated messages, and `errdetails` (`ErrorInfo`, `BadRequest`) so a gRPC status carries the same code, request id and field detail as the REST error body. `grpc/health` and `reflection` come with it | Connect (nice, but the brief says gRPC and grpcurl/grpcui expect the real thing) |
| [`prometheus/client_golang`](https://github.com/prometheus/client_golang) | 1.24.1 | The de-facto metrics client; a private registry (no globals) with a request counter, a latency histogram and the `users_total` gauge the worker feeds through a plain callback | OpenTelemetry metrics — worth it once there is a collector to send to; for one service scraping `/metrics` is enough |
| [`caarlos0/env/v11`](https://github.com/caarlos0/env) | 11.4.1 | Struct tags declare name, default and `required,notEmpty` in one place; parses everything, reports every error in one batch, and a custom parser keeps `[]byte` secrets as raw bytes | `kelseyhightower/envconfig` (unmaintained); viper (config files and globals we do not want); `os.Getenv` scattered around |
| [`joho/godotenv`](https://github.com/joho/godotenv) | 1.5.1 | `godotenv.Load()` in `main` reads `.env` when present and never overrides a real environment variable, so `make run` works with or without direnv | Requiring direnv — a convenience should not be a prerequisite |
| [`swaggo/swag`](https://github.com/swaggo/swag) · `http-swagger/v2` | 1.16.6 · 2.0.2 | The spec is generated from annotations on the real handlers and references the real envelope types, so it cannot drift from the code; the UI is served in development only | Hand-written OpenAPI (drifts); oapi-codegen (spec-first — fine, but the code was the source of truth here) |
| [`fullstorydev/grpcui`](https://github.com/fullstorydev/grpcui) | 1.5.4 | A browser console for the gRPC side, mounted in-process at `/grpcui/` so a reviewer can try every rpc without installing anything. Behind the `dev` build tag | Asking reviewers to install grpcurl |

## Test only

| Package | Version | Why |
| --- | --- | --- |
| [`stretchr/testify`](https://github.com/stretchr/testify) | 1.12.1 | `require`/`assert` for table-driven tests, `suite` for the middleware (a fresh log buffer per test), `mock` as the backend for the generated mocks |
| [`onsi/ginkgo/v2`](https://github.com/onsi/ginkgo) · `gomega` | 2.32.1 · 1.43.0 | Specs that read as behaviour where a table does not: the use-case interaction specs, the worker (`Eventually`/`Consistently` instead of sleeps), the ordered end-to-end journey, the concurrency specs. Runs under plain `go test` |
| [`testcontainers-go`](https://github.com/testcontainers/testcontainers-go) `+ modules/mongodb` | 0.44.0 | One real `mongo:8.0` container per package, a private database per test. Mocking the driver would test our beliefs about MongoDB, not MongoDB |

## Tools (`make tools-install`, pinned to what CI uses)

| Tool | Version | Job |
| --- | --- | --- |
| golangci-lint | v2.13.2 (brew) | errcheck, govet, ineffassign, staticcheck, unused, gosec, bodyclose, noctx, misspell, revive, and `depguard` as the executable dependency diagram |
| mockery v3 | 3.7.4 | Mocks of every port from `ports.go`, `template: testify`; CI regenerates and diffs |
| buf · protoc-gen-go · protoc-gen-go-grpc | 1.72.0 · 1.36.12 · 1.6.2 | Lint and generate the gRPC code without a `protoc` install; CI regenerates and diffs |
| swag | 1.16.6 | `make swagger` — not diffed in CI, since swag versions format differently |
| grpcurl | 1.9.4 | `make test-grpc` — the walkthrough calls through it, reading the contract over reflection |
| govulncheck | 1.7.0 | Known-vulnerability scan, also run by CI on every push |
| Python 3.9+ · [`rich`](https://github.com/Textualize/rich) ≥ 13.7 (`make py-deps`) | any 3.9+ | `make test-rest` / `make test-grpc` — the walkthrough scripts are Python; `rich` is the only import outside the standard library, and it is what makes the run readable (panels, rules, highlighted JSON). Nothing here is linked into the service |

## Images

| Image | Why pinned like this |
| --- | --- |
| `golang:1.27.1-alpine` → `gcr.io/distroless/static-debian13:nonroot` | Static binary (`CGO_ENABLED=0`), no shell, no libc, non-root — the smallest attack surface that still runs |
| `mongo:8.0` | The floating `mongo:8` already points at 8.3, which refuses a volume written by 7.x; 8.0 opens and upgrades it. Same tag in testcontainers |
| `huggingface/mongoku:2.11.2` | A read-only data browser for reviewers; pinned because `:latest` moved under us once |
