# REST API spec

[README](../README.md) · [Checklist](../checklist.md) · [Architecture](architecture.md) · [Configuration](configuration.md) · [JWT](jwt-guide.md) · [gRPC API](grpc-api.md) · [gRPC tooling](grpc.md) · [Testing](testing.md) · [Decisions](design-decisions.md)

Base path `/api/v1` · `Content-Type: application/json; charset=utf-8` in and out · ids are 24-hex ObjectIds · times RFC 3339 UTC ·
`snake_case` · every response carries `X-Request-ID` (echoed if sent, minted otherwise).
Live and generated from the same annotations: **http://localhost:8080/swagger/** (`APP_ENV=development`).

Every section below ends with **Tests** — the unit test that pins the shape (handler on fakes), then the integration
journey on real MongoDB, then the black-box script step against a running server. Click through.

| Code | |
| --- | --- |
| Routes, middleware order | [`http/router.go`](../internal/adapter/inbound/http/router.go) |
| Handlers | [`auth_handler.go`](../internal/adapter/inbound/http/auth_handler.go) · [`user_handler.go`](../internal/adapter/inbound/http/user_handler.go) · [`health.go`](../internal/adapter/inbound/http/health.go) |
| Request / response shapes | [`dto.go`](../internal/adapter/inbound/http/dto.go) |
| Envelopes, status table | [`respond/respond.go`](../internal/adapter/inbound/http/respond/respond.go) — `DataEnvelope`, `ErrorEnvelope`, `statusOf` |
| Codes, messages (shared with gRPC) | [`apierr/apierr.go`](../internal/adapter/inbound/apierr/apierr.go) |
| Token check | [`middleware/auth.go`](../internal/adapter/inbound/http/middleware/auth.go) |

| Tests | |
| --- | --- |
| Handlers on fakes | [`handler_test.go`](../internal/adapter/inbound/http/handler_test.go) |
| Each middleware alone | [`middleware_test.go`](../internal/adapter/inbound/http/middleware/middleware_test.go) |
| REST ≡ gRPC, same cases | [`parity_test.go`](../internal/adapter/inbound/parity/parity_test.go) |
| The whole stack on real MongoDB | [`journey_test.go`](../test/integration/journey_test.go) · [`concurrency_test.go`](../test/integration/concurrency_test.go) |
| A running server, every case | [`scripts/test_rest.py`](../scripts/test_rest.py) → `make test-rest` |

## Shapes

```
User     { id, name, email, created_at, updated_at }                                          dto.go: userResponse
Session  { access_token, token_type:"Bearer", expires_in (s), refresh_token, user: User }      dto.go: sessionResponse
ListMeta { limit, next_cursor (null on the last page), has_more }                             dto.go: listMeta

success  { "data": … }  or  { "data": […], "meta": ListMeta }                                 respond.DataEnvelope
error    { "error": { "code", "message", "details"?: [{ "field", "issue" }] }, "request_id" }  respond.ErrorEnvelope
```

Tests: [TestGetUser_Success](../internal/adapter/inbound/http/handler_test.go#L102) (User, RFC 3339, no password field) ·
[TestLogin_Success](../internal/adapter/inbound/http/handler_test.go#L493) (Session) ·
[TestListUsers_Pagination](../internal/adapter/inbound/http/handler_test.go#L172) (ListMeta) ·
[TestParity_User](../internal/adapter/inbound/parity/parity_test.go#L211) (same bytes as gRPC).

## Auth — no token

### `POST /auth/register` — `authHandler.Register`

```
→ { "name": "Natthawat N.", "email": "Natthawat@Example.com", "password": "Str0ng-Passw0rd!" }     dto.go: createUserRequest

← 201  Location: /api/v1/users/{id}
  { "data": { "id": "6702c1f4a3b19d0f9c4e2a71", "name": "Natthawat N.", "email": "natthawat@example.com",
              "created_at": "2026-09-03T09:14:22Z", "updated_at": "2026-09-03T09:14:22Z" } }
← 422 VALIDATION_ERROR   every failing field in details, order name → email → password
← 409 EMAIL_TAKEN        the unique index
← 400 MALFORMED_JSON · 413 PAYLOAD_TOO_LARGE
```

No token comes back; log in for one. Email is stored lowercased.

Tests: [TestRegister_Created](../internal/adapter/inbound/http/handler_test.go#L400) ·
[TestRegister_EmailTaken](../internal/adapter/inbound/http/handler_test.go#L414) ·
[TestRegister_ValidationDetails](../internal/adapter/inbound/http/handler_test.go#L427) ·
[TestRegister_ValidationDetails_EveryField](../internal/adapter/inbound/http/handler_test.go#L447) ·
[TestRegister_MalformedJSON](../internal/adapter/inbound/http/handler_test.go#L469) ·
[TestRegister_PayloadTooLarge](../internal/adapter/inbound/http/handler_test.go#L481) ·
journey [signs up](../test/integration/journey_test.go#L174), [duplicate refused](../test/integration/journey_test.go#L187), [every field at once](../test/integration/journey_test.go#L253) ·
[20 concurrent registrations, one 201](../test/integration/concurrency_test.go#L53) ·
script [step 3](../scripts/test_rest.py#L206).

### `POST /auth/login` — `authHandler.Login`

```
→ { "email": "natthawat@example.com", "password": "Str0ng-Passw0rd!" }          dto.go: loginRequest

← 200 { "data": Session }
← 401 INVALID_CREDENTIALS   same answer for wrong password, unknown email, malformed email
```

Tests: [TestLogin_Success](../internal/adapter/inbound/http/handler_test.go#L493) ·
[TestLogin_InvalidCredentials](../internal/adapter/inbound/http/handler_test.go#L573) ·
the three failure modes are one error in [TestAuthService_Login](../internal/app/auth_service_test.go#L42) ·
journey [logs in](../test/integration/journey_test.go#L203), [same 401 for both](../test/integration/journey_test.go#L221) ·
script [step 4](../scripts/test_rest.py#L242).

### `POST /auth/refresh` — `authHandler.Refresh`

```
→ { "refresh_token": "1f4e…c9" }                                               dto.go: refreshRequest

← 200 { "data": Session }        a whole new pair; the old refresh token is spent
← 401 UNAUTHORIZED               unknown, expired, already rotated (→ every session of that user is wiped), or lost a concurrent refresh
← 400 MALFORMED_JSON
```

Tests: [TestRefresh_Success](../internal/adapter/inbound/http/handler_test.go#L524) ·
[TestRefresh_Unauthorized](../internal/adapter/inbound/http/handler_test.go#L550) ·
[TestRefresh_MalformedJSON](../internal/adapter/inbound/http/handler_test.go#L562) ·
rotation, reuse, expiry, the race: [TestAuthService_Refresh](../internal/app/auth_service_test.go#L117) ·
journey [rotates](../test/integration/journey_test.go#L291), [reuse wipes every session](../test/integration/journey_test.go#L307) ·
[20 concurrent refreshes, one winner](../test/integration/concurrency_test.go#L66) ·
script [step 10](../scripts/test_rest.py#L357).

## Users — `Authorization: Bearer <access_token>` on every route, else `401 UNAUTHORIZED` + `WWW-Authenticate: Bearer`

Tests: [TestProtectedRoutes_RequireToken](../internal/adapter/inbound/http/handler_test.go#L140) (all five routes) ·
[TestAuthenticate](../internal/adapter/inbound/http/middleware/middleware_test.go#L399) (no header, wrong scheme, empty, rejected, accepted, actor set) ·
journey [closed without a token](../test/integration/journey_test.go#L195) · script [step 2](../scripts/test_rest.py#L195).

### `POST /users` — `userHandler.Create`

Same body and answers as register; only the token requirement differs.

Tests: [TestCreateUser](../internal/adapter/inbound/http/handler_test.go#L371) · [TestParity_User/create](../internal/adapter/inbound/parity/parity_test.go#L224) · script [step 8](../scripts/test_rest.py#L312).

### `GET /users` — `userHandler.List`

```
?limit=20      1..100, default 20 — validated in app.ListFilter.Resolve, parsed in user_handler.go: listFilterFrom
?cursor=       next_cursor from the previous page (keyset on _id)
?query=        case-insensitive substring of name or email

← 200 { "data": [User…], "meta": { "limit": 2, "next_cursor": "6702…6f" | null, "has_more": true } }
← 422 VALIDATION_ERROR   field "limit" (out of range or not an integer) or "cursor" (not an ObjectId)
```

Tests: [TestListUsers_Pagination](../internal/adapter/inbound/http/handler_test.go#L172) ·
[TestListUsers_NoNextPage](../internal/adapter/inbound/http/handler_test.go#L198) (`null`, `[]`) ·
[TestListUsers_LimitOverCap](../internal/adapter/inbound/http/handler_test.go#L212) ·
[TestListUsers_LimitNotANumber](../internal/adapter/inbound/http/handler_test.go#L255) ·
[TestListUsers_ForwardsCursorAndQuery](../internal/adapter/inbound/http/handler_test.go#L228) ·
[TestListUsers_BadCursor](../internal/adapter/inbound/http/handler_test.go#L243) ·
the rule itself: [TestUserService_List](../internal/app/user_service_test.go#L165) ·
real keyset and search: [TestUserRepo_KeysetPagination](../test/integration/user_repo_test.go#L89), [TestUserRepo_ListSearch](../test/integration/user_repo_test.go#L129), [TestUserRepo_InvalidIDAndCursor](../test/integration/user_repo_test.go#L148) ·
script [step 5](../scripts/test_rest.py#L264), [step 9](../scripts/test_rest.py#L344).

### `GET /users/{id}` — `userHandler.Get`

```
← 200 { "data": User }
← 404 USER_NOT_FOUND
← 422 VALIDATION_ERROR   field "id" — malformed (the message does not say "ObjectId")
```

Tests: [TestGetUser_Success](../internal/adapter/inbound/http/handler_test.go#L102) ·
[TestGetUser_NotFound](../internal/adapter/inbound/http/handler_test.go#L90) ·
[TestGetUser_MalformedID](../internal/adapter/inbound/http/handler_test.go#L126) ·
script [step 6](../scripts/test_rest.py#L285).

### `PATCH /users/{id}` — `userHandler.Update` — own account only

```
→ { "name"?: "…", "email"?: "…" }        dto.go: updateUserRequest (*string: absent ≠ empty)

← 200 { "data": User }                    only the fields sent change
← 403 FORBIDDEN          id is not the caller's — decided before the row is read, so identical whether or not it exists
← 422 VALIDATION_ERROR   empty body (field "body"), bad name and/or email — all reported together
← 404 USER_NOT_FOUND · 409 EMAIL_TAKEN
```

Tests: [TestUpdateUser_PartialPatch](../internal/adapter/inbound/http/handler_test.go#L266) (absent ≠ empty, actor forwarded) ·
[TestUpdateAndDelete_Forbidden](../internal/adapter/inbound/http/handler_test.go#L334) (403, no `WWW-Authenticate`) ·
[TestUpdateUser_ErrorMapping](../internal/adapter/inbound/http/handler_test.go#L283) (404, 409, empty body, two fields) ·
the rules: [TestUserService_Update](../internal/app/user_service_test.go#L232) ·
journey [updates](../test/integration/journey_test.go#L236), [403 for someone else's row, existing or not](../test/integration/journey_test.go#L265) ·
[two users racing for one email](../test/integration/concurrency_test.go#L85) ·
script [step 7](../scripts/test_rest.py#L300), [step 8b](../scripts/test_rest.py#L326).

### `DELETE /users/{id}` — `userHandler.Delete` — own account only

```
← 204  no body; the user's refresh tokens are revoked with the row
← 403 FORBIDDEN · 404 USER_NOT_FOUND (a second delete: it really is gone)
```

Tests: [TestDeleteUser_NoContent](../internal/adapter/inbound/http/handler_test.go#L357) ·
[TestDeleteUser_NotFound](../internal/adapter/inbound/http/handler_test.go#L321) ·
[TestUpdateAndDelete_Forbidden](../internal/adapter/inbound/http/handler_test.go#L334) ·
cascade: [TestUserService_DeleteAndCount](../internal/app/user_service_test.go#L350) ·
journey [deletes, sessions revoked, then 404 and 401](../test/integration/journey_test.go#L327) ·
script [step 11](../scripts/test_rest.py#L374).

## System — no token

```
GET /healthz   ← 200 { "status": "ok" }                   liveness; touches nothing
GET /readyz    ← 200 { "status": "ok" }                   pings MongoDB (2 s)
               ← 503 { "status": "unavailable" }          MongoDB unreachable
               ← 503 { "status": "draining" }             shutdown has begun; stays so for SHUTDOWN_DELAY, then the port closes
GET /metrics   Prometheus: http_requests_total{method,route,status}, http_request_duration_seconds, users_total
```

Unknown route → `404 NOT_FOUND`; wrong method → `405 METHOD_NOT_ALLOWED`; both in the same error envelope.

Tests: [TestHealthAndReadiness](../internal/adapter/inbound/http/handler_test.go#L590) (ok, unavailable, draining without a ping, healthz stays 200) ·
[TestMetricsEndpoint](../internal/adapter/inbound/http/handler_test.go#L633) ·
[TestMetrics_LabelsByRoutePatternNotByPath](../internal/adapter/inbound/http/middleware/middleware_test.go#L258) ·
[TestUnknownRouteAndMethod](../internal/adapter/inbound/http/handler_test.go#L739) ·
[TestPanicIsRecovered](../internal/adapter/inbound/http/handler_test.go#L753) ·
journey [readyz really pings](../test/integration/journey_test.go#L167) · script [step 1](../scripts/test_rest.py#L173), [step 12](../scripts/test_rest.py#L392).

## Error codes

| Code | HTTP | Comes from |
| --- | --- | --- |
| `VALIDATION_ERROR` | 422 | `user.ErrValidation` / `user.ValidationErrors` |
| `MALFORMED_JSON` | 400 | `respond.ErrMalformedJSON` (decoder message never echoed) |
| `PAYLOAD_TOO_LARGE` | 413 | `respond.ErrPayloadTooBig`, 1 MB cap in `middleware/bodylimit.go` |
| `INVALID_CREDENTIALS` | 401 | `user.ErrInvalidCredentials` |
| `UNAUTHORIZED` | 401 | `apierr.ErrUnauthenticated` (no usable token) or `user.ErrUnauthorized` (refresh token) |
| `FORBIDDEN` | 403 | `user.ErrForbidden` |
| `USER_NOT_FOUND` | 404 | `user.ErrNotFound` |
| `NOT_FOUND` / `METHOD_NOT_ALLOWED` | 404 / 405 | router fallbacks |
| `EMAIL_TAKEN` | 409 | `user.ErrEmailTaken` (Mongo `E11000`) |
| `INTERNAL` | 500 | anything else — logged with the request id, message reveals nothing |

Tests: every error → exactly one code, internal reveals nothing: [TestClassify](../internal/adapter/inbound/apierr/apierr_test.go#L15), [TestClassify_InternalHidesDetail](../internal/adapter/inbound/apierr/apierr_test.go#L75) ·
every code answers the same on both transports: [TestParity_Errors](../internal/adapter/inbound/parity/parity_test.go#L313) ·
[TestMaxBytes](../internal/adapter/inbound/http/middleware/middleware_test.go#L358).

## The log line — `middleware/logging.go`

```json
{"msg":"http_request","method":"PATCH","path":"/api/v1/users/6702…","status":200,"duration_ms":12,"bytes":212,
 "request_id":"01M1…","remote_ip":"172.18.0.1","actor_id":"6702…"}
```

One per request, written at the end. `path` only (no query string); `actor_id` on authenticated routes.

Tests: [TestLogging_WritesOneLinePerRequestOnceEverythingIsKnown](../internal/adapter/inbound/http/middleware/middleware_test.go#L169) (query string kept out) ·
[TestLogging_PrintsTheActorWhenAuthenticateRanInsideIt](../internal/adapter/inbound/http/middleware/middleware_test.go#L449) ·
[TestRequestID](../internal/adapter/inbound/http/middleware/middleware_test.go#L79) ·
[TestParity_RequestIDIsEchoed](../internal/adapter/inbound/parity/parity_test.go#L370).
