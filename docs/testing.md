# Testing

[README](../README.md) · [Checklist](../checklist.md) · [Architecture](architecture.md) · [Configuration](configuration.md) · [JWT](jwt-guide.md) · [REST API](rest-api.md) · [gRPC API](grpc-api.md) · [gRPC tooling](grpc.md) · [Decisions](design-decisions.md)

```bash
make test          # unit, -race, -tags dev (the grpcconsole tests need the tag)
make test-int      # real MongoDB via testcontainers — needs Docker
make cover-report  # unit + integration merged → coverage/summary.md, coverage.html
make cover-check   # core (domain + app) must stay ≥ 80% — CI runs this
make mocks         # regenerate the mockery mocks; commit them, CI diffs

make py-deps       # once, for the two walkthrough scripts below (installs rich)
make test-rest     # every REST endpoint against a running server, each case asserted
make test-grpc     # the same over gRPC (needs grpcurl)
```

## Four tiers

| | Runs with | Needs | Proves |
| --- | --- | --- | --- |
| **Unit** | `make test` | Go | Domain rules, use-case sequences, both adapters, every middleware, JWT edge cases |
| **Parity** | `make test` | Go | The same cases through REST and gRPC, on the same fake, answer with the same data, code, field detail and request id — [`parity/`](../internal/adapter/inbound/parity/) |
| **Integration** | `make test-int` | Docker | Both repositories on real MongoDB (unique index, keyset paging, `E11000`, TTL), one user's whole journey through the real router, and the claims under load: 20 concurrent registrations of one email, 20 concurrent refreshes of one token, two users racing for one email — [`test/integration/`](../test/integration/) |
| **Scripts** | `make test-rest` · `make test-grpc` | A running server, Python + `rich` (`make py-deps`), and grpcurl for the gRPC one | Every endpoint for real, each case asserted, every exchange logged to `scripts/logs/` |

Integration sits behind `//go:build integration` so `go test ./...` stays green without Docker.

## Tools

| | Where | For |
| --- | --- | --- |
| Testify `require`/`assert` | every table-driven test | The default |
| Testify `suite` | [`middleware_test.go`](../internal/adapter/inbound/http/middleware/middleware_test.go) | One fixture per test (a fresh JSON log buffer), subtests via `s.Run` |
| Mockery mocks | [`apptest/mocks/`](../internal/app/apptest/mocks/), from `ports.go` | *Interaction*: exact arguments, call order, calls that must not happen. An unexpected call fails the spec |
| Ginkgo + Gomega | `internal/app/*_spec_test.go`, the worker, the integration journey and concurrency specs | Behaviour that reads as a story; `Ordered` for the journey; `Eventually`/`Consistently` for the worker instead of `time.Sleep` |

**Fakes or mocks?** Fakes hold *state* — an in-memory repo with the real ordering rules — so a test can say "7 users at 3 per page is 3 pages, no duplicates".
Mocks hold *expectations*, so a spec can say "the old token is claimed before the new one is stored" or "a foreign id is refused before the repo is read".
Driven-port fakes live in [`internal/app/fake_test.go`](../internal/app/fake_test.go); the driving-port ones (`FakeUsers`, `FakeAuth`, `Verifier`) in
[`apptest`](../internal/app/apptest/apptest.go) because both transports use them.

## Where the doubles stop

The brief says *mock MongoDB where appropriate*. We mock the interfaces we own, never the driver — faking the driver's
protocol tests our beliefs about it, not the system. So: fakes at the ports for logic, real MongoDB for queries.

| Layer | Double |
| --- | --- |
| Domain | none — pure functions |
| App | fakes for the outcome, mocks for the conversation with the port |
| HTTP / gRPC | `FakeUsers`, `FakeAuth`, `Verifier` — identical doubles for both transports |
| Worker | a mock `UserUseCase` |
| Token | real JWT, time through the injected clock |
| Mongo | **none** — a container |
| Whole system | none — the composition root's wiring minus the listeners |

`-race` is always on because there is a background goroutine.

## Coverage

`cover-report` merges the unit and integration profiles (a block counts if either hit it) and writes `coverage/summary.md`
per layer and per package, minus generated protobuf and the test doubles. The core bar is 80%; there is no target for the
overall number, which is dominated by generated code. Run it rather than trust a number in a document.

## Guardrails

`depguard` is the dependency diagram as lint: domain → stdlib only, app → domain only, inbound never outbound and vice
versa, platform neither. [`deps_test.go`](../internal/domain/user/deps_test.go) checks the domain rule again from `go test`.
CI regenerates and diffs the protobuf code and the mocks. It does *not* diff the OpenAPI spec — `swag` versions format
differently — so run `make swagger` after touching an annotation.
