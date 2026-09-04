# gRPC API spec

[README](../README.md) · [Checklist](../checklist.md) · [Architecture](architecture.md) · [Configuration](configuration.md) · [JWT](jwt-guide.md) · [REST API](rest-api.md) · [gRPC tooling](grpc.md) · [Testing](testing.md) · [Decisions](design-decisions.md)

Port `:9090`, plaintext. Package `user.v1`, two services. Reflection and the browser console at **http://localhost:8080/grpcui/** in development only.
Field names are `snake_case` in the contract (grpcurl prints them camelCase unless told otherwise).

Every section ends with **Tests**: the unit test on fakes, the parity test that holds gRPC to the REST answer, and the black-box script step.

| Code | |
| --- | --- |
| The contract | [`proto/user/v1/user.proto`](../proto/user/v1/user.proto) · [`auth.proto`](../proto/user/v1/auth.proto) → generated `gen/user/v1/` (`make proto`) |
| Service implementations | [`grpc/user_service.go`](../internal/adapter/inbound/grpc/user_service.go) · [`auth_service.go`](../internal/adapter/inbound/grpc/auth_service.go) — thin: map, call the use case, map back |
| Message ↔ domain mapping | [`mapper.go`](../internal/adapter/inbound/grpc/mapper.go) — `toProto`, `toListFilter`, `toListProto`, `toSessionProto` |
| Interceptors (`recovery → logging → errors → timeout → auth`) | [`interceptor.go`](../internal/adapter/inbound/grpc/interceptor.go) · [`errors.go`](../internal/adapter/inbound/grpc/errors.go) — `codeOf`, `toStatus` |
| Codes, messages (shared with REST) | [`apierr/apierr.go`](../internal/adapter/inbound/apierr/apierr.go) |
| Server assembly, health, reflection | [`server.go`](../internal/adapter/inbound/grpc/server.go) |

| Tests | |
| --- | --- |
| Services and interceptors on fakes | [`server_test.go`](../internal/adapter/inbound/grpc/server_test.go) |
| gRPC ≡ REST, same cases, over bufconn | [`parity_test.go`](../internal/adapter/inbound/parity/parity_test.go) |
| The console's examples match the real allowlist | [`console_test.go`](../internal/adapter/inbound/grpc/console_test.go) |
| A running server, every case | [`scripts/test_grpc.py`](../scripts/test_grpc.py) → `make test-grpc` |

## Metadata

```
authorization: Bearer <access_token>   required on every UserService rpc (lowercase key; HTTP/2). Missing/bad → UNAUTHENTICATED
x-request-id: <id>                     optional in; always echoed back as a response header (minted if absent)
```

Every rpc runs under `SERVER_RPC_TIMEOUT` as a ceiling: a shorter client deadline is kept, a longer one capped (`timeoutUnary`).

Tests: [TestAuthUnary](../internal/adapter/inbound/grpc/server_test.go#L332) (no metadata, no row, no scheme, lowercase scheme, rejected, accepted, actor set, health public) ·
[TestPublicPrefixes_MatchTheContract](../internal/adapter/inbound/grpc/server_test.go#L414) (every `AuthService` rpc public, every `UserService` rpc not — read from the generated descriptors) ·
[TestTimeoutUnary](../internal/adapter/inbound/grpc/server_test.go#L438) (none / longer / shorter client deadline) ·
[TestLoggingUnary_SettlesTheRequestID](../internal/adapter/inbound/grpc/server_test.go#L529), [TestParity_RequestIDIsEchoed](../internal/adapter/inbound/parity/parity_test.go#L370) ·
script [step 2](../scripts/test_grpc.py#L210).

## Messages

```protobuf
User      { string id; string name; string email; Timestamp created_at; Timestamp updated_at }   // whole seconds, like REST
Session   { string access_token; string token_type = "Bearer"; int64 expires_in; string refresh_token; User user }
ListMeta  { int32 limit; optional string next_cursor;  bool has_more }     // next_cursor unset on the last page
```

Tests: [TestToProto_TimestampsAreWholeSeconds](../internal/adapter/inbound/grpc/server_test.go#L56) ·
[TestAuthService_Login](../internal/adapter/inbound/grpc/server_test.go#L588) (Session shape = REST's) ·
[TestParity_User](../internal/adapter/inbound/parity/parity_test.go#L211), [TestParity_List](../internal/adapter/inbound/parity/parity_test.go#L256), [TestParity_Session](../internal/adapter/inbound/parity/parity_test.go#L288) — key for key against the REST JSON.

## `user.v1.AuthService` — no token

| rpc | request | response | errors |
| --- | --- | --- | --- |
| `Register` | `{ name, email, password }` | `{ user: User }` — no token; log in for one | `INVALID_ARGUMENT` (every failing field), `ALREADY_EXISTS` |
| `Login` | `{ email, password }` | `{ session: Session }` | `UNAUTHENTICATED` reason `INVALID_CREDENTIALS` — same for wrong password, unknown or malformed email |
| `Refresh` | `{ refresh_token }` | `{ session: Session }` — a new pair, old token spent | `UNAUTHENTICATED` reason `UNAUTHORIZED` — unknown, expired, already rotated (wipes every session), or lost a concurrent refresh |

Tests: [TestAuthService_Register](../internal/adapter/inbound/grpc/server_test.go#L548) ·
[TestAuthService_Login](../internal/adapter/inbound/grpc/server_test.go#L588) ·
[TestAuthService_Refresh](../internal/adapter/inbound/grpc/server_test.go#L623) ·
the behaviour behind them is the same use case REST calls: [TestAuthService_Login](../internal/app/auth_service_test.go#L42), [TestAuthService_Refresh](../internal/app/auth_service_test.go#L117), [20 concurrent refreshes](../test/integration/concurrency_test.go#L66) ·
script [step 3](../scripts/test_grpc.py#L221), [step 4](../scripts/test_grpc.py#L247), [step 10](../scripts/test_grpc.py#L351).

## `user.v1.UserService` — `authorization` required

| rpc | request | response | errors |
| --- | --- | --- | --- |
| `CreateUser` | `{ name, email, password }` | `{ user }` | as `Register` |
| `GetUser` | `{ id }` | `{ user }` | `NOT_FOUND`, `INVALID_ARGUMENT` (malformed id) |
| `ListUsers` | `{ optional int32 limit; string cursor; string query }` — `optional` so "not sent" ≠ 0 | `{ users: [User], meta: ListMeta }` | `INVALID_ARGUMENT` field `limit` / `cursor` |
| `UpdateUser` | `{ id; optional name; optional email }` — only fields set change | `{ user }` | `PERMISSION_DENIED` (not your account), `INVALID_ARGUMENT`, `NOT_FOUND`, `ALREADY_EXISTS` |
| `DeleteUser` | `{ id }` | `{}` — empty on purpose (= 204); refresh tokens revoked too | `PERMISSION_DENIED`, `NOT_FOUND` |

Tests: [TestUserService_CreateUser](../internal/adapter/inbound/grpc/server_test.go#L37) ·
[TestUserService_GetUser](../internal/adapter/inbound/grpc/server_test.go#L65) ·
[TestUserService_ListUsers](../internal/adapter/inbound/grpc/server_test.go#L93) (meta, last page, nil limit, unusable limit) ·
[TestUserService_UpdateUser](../internal/adapter/inbound/grpc/server_test.go#L156) (optional fields, actor) ·
[TestUserService_DeleteUser](../internal/adapter/inbound/grpc/server_test.go#L173) (empty answer, forbidden, repeated) ·
[TestParity_User](../internal/adapter/inbound/parity/parity_test.go#L211), [TestParity_List](../internal/adapter/inbound/parity/parity_test.go#L256), [TestParity_Errors](../internal/adapter/inbound/parity/parity_test.go#L313) (incl. `PERMISSION_DENIED` via `UpdateUser`) ·
script [step 5](../scripts/test_grpc.py#L265) – [step 9](../scripts/test_grpc.py#L338), [step 8b](../scripts/test_grpc.py#L321), [step 11](../scripts/test_grpc.py#L367).

## `grpc.health.v1.Health/Check` — no token

`SERVING` for the life of the process; `NOT_SERVING` from the moment shutdown begins, while the port stays open for `SHUTDOWN_DELAY`.

Tests: [TestNewServer_UsesTheInjectedHealthServer](../internal/adapter/inbound/grpc/server_test.go#L498) (SERVING → NOT_SERVING while still answering) ·
[TestAuthUnary/a health check needs no authentication](../internal/adapter/inbound/grpc/server_test.go#L398) ·
script [step 1](../scripts/test_grpc.py#L186).

## Errors

A method returns the core's error untouched; `errorsUnary` builds one status per error through the same `apierr.Classify` REST uses.
The status carries everything the REST error body carries:

```
code      ← codeOf[shared code]                       coarser than REST — two shared codes map to UNAUTHENTICATED
message   ← the same words REST uses
details   ← google.rpc.ErrorInfo  { reason: <shared code>, domain: "user-service", metadata: { request_id } }
            google.rpc.BadRequest { field_violations: [{ field, description }…] }   on validation, every field
```

| Shared code (`ErrorInfo.reason`) | gRPC code | REST |
| --- | --- | --- |
| `VALIDATION_ERROR` | `INVALID_ARGUMENT` | 422 |
| `USER_NOT_FOUND` | `NOT_FOUND` | 404 |
| `EMAIL_TAKEN` | `ALREADY_EXISTS` | 409 |
| `INVALID_CREDENTIALS` · `UNAUTHORIZED` | `UNAUTHENTICATED` | 401 |
| `FORBIDDEN` | `PERMISSION_DENIED` | 403 |
| `INTERNAL` | `INTERNAL` | 500 |

Not from the shared table: a context that ran out (the server ceiling, or the caller's own deadline) is `DEADLINE_EXCEEDED`,
a caller that went away is `CANCELED`, a panic is `INTERNAL` with the stack in the server log.

```bash
grpcurl -plaintext -format-error -d '{"name":"","email":"nope","password":"short"}' localhost:9090 user.v1.AuthService/Register
# Code: InvalidArgument   Message: the data sent is invalid
# ErrorInfo  { reason: VALIDATION_ERROR, metadata: { request_id: 01M1… } }
# BadRequest { fieldViolations: [name, email, password] }
```

Tests: [TestErrorsUnary](../internal/adapter/inbound/grpc/server_test.go#L212) — every row of the table, deadline, cancel, ErrorInfo with the request id, one and many field violations, internal hides detail, a status passes through ·
[TestRecoveryUnary](../internal/adapter/inbound/grpc/server_test.go#L427) ·
[TestParity_Errors](../internal/adapter/inbound/parity/parity_test.go#L313) (code, message, fields and request id equal to REST's for every case) ·
script [step 3](../scripts/test_grpc.py#L221) (three field violations).

## The log line — `loggingUnary`

```json
{"msg":"grpc_request","method":"/user.v1.UserService/UpdateUser","code":"OK","duration_ms":9,"request_id":"01M1…","actor_id":"6702…"}
```

Tests: [TestLoggingUnary_PrintsTheActor](../internal/adapter/inbound/grpc/server_test.go#L479) · [TestLoggingUnary_SettlesTheRequestID](../internal/adapter/inbound/grpc/server_test.go#L529).
