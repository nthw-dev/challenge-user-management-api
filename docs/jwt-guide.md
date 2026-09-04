# JWT guide

[README](../README.md) · [Checklist](../checklist.md) · [Architecture](architecture.md) · [Configuration](configuration.md) · [REST API](rest-api.md) · [gRPC API](grpc-api.md) · [gRPC tooling](grpc.md) · [Testing](testing.md) · [Decisions](design-decisions.md)

## Get a token

```bash
export JWT_SECRET=$(openssl rand -base64 48)   # ≥ 32 bytes, or the app refuses to boot

curl -s -X POST localhost:8080/api/v1/auth/register -H 'Content-Type: application/json' \
  -d '{"name":"Tester","email":"tester@example.com","password":"Str0ng-Passw0rd!"}'

TOKEN=$(curl -s -X POST localhost:8080/api/v1/auth/login -H 'Content-Type: application/json' \
  -d '{"email":"tester@example.com","password":"Str0ng-Passw0rd!"}' | jq -r .data.access_token)
```

## Use it

```bash
curl -s localhost:8080/api/v1/users -H "Authorization: Bearer $TOKEN" | jq
curl -i -s localhost:8080/api/v1/users | head -n 3      # no token → 401 + WWW-Authenticate: Bearer
```

## What is inside

```bash
echo "$TOKEN" | cut -d. -f2 | base64 -d 2>/dev/null | jq
```

```json
{ "typ":"access", "iss":"user-service", "aud":["user-service-api"],
  "sub":"6702c1f4a3b19d0f9c4e2a71", "jti":"01M1KFRT4VCR94BJNYZ9K5630Y",
  "iat":1788434213, "nbf":1788434213, "exp":1788435113 }
```

The payload is base64, not encryption — anyone holding the token can read it. Only the signature needs the secret.
So there is no email, no name, nothing personal: just `sub`, and the handler reads the rest from the database.

| Claim | Why |
| --- | --- |
| `sub` | The user id — the one thing we need |
| `exp` | 15 minutes; a leaked token is useful briefly |
| `iat` / `nbf` | Reference points a `tokens_valid_after` revocation could use later |
| `iss` / `aud` | Keep a token from another system with the same secret out |
| `jti` | A per-token id, if a denylist is ever needed |
| `typ` | `access`, so nothing else can be passed off as one |

## The line that matters

```go
jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
```

Without it a lax parser accepts `{"alg":"none"}` or a swapped algorithm. [`jwt_test.go`](../internal/adapter/outbound/token/jwt_test.go)
covers `none`, HS512, a foreign secret, a wrong `iss`/`aud`, and a missing `exp`.

## Lifetimes, rotation, revocation

| | Lives | Stored |
| --- | --- | --- |
| Access token | 15 min | Nowhere — verified by signature alone |
| Refresh token | 7 days | 32 random bytes; only its SHA-256 is stored, with a TTL index. SHA-256 not bcrypt: the input is already 256 random bits |

`/auth/refresh` claims the old token (a compare-and-swap on "not yet revoked") *before* issuing the new one, so two
concurrent refreshes give exactly one new session and the other a 401. Presenting a token already known to be rotated
means a copy leaked: every session of that user is wiped. Deleting the account revokes its tokens on the spot.
Twenty concurrent refreshes against real MongoDB prove this in [`concurrency_test.go`](../test/integration/concurrency_test.go).

## Around it

- **bcrypt cost 12** — login costs hundreds of milliseconds on purpose.
- **Uniform login failure** — wrong password, unknown email and malformed email answer identically, and the unknown-email path still runs one bcrypt compare against a decoy hash so timing gives nothing away.
- **Self-only writes** — the token's `sub` is the actor; `PATCH`/`DELETE` on any other account is 403.
- **1 MB body cap, slowloris timeouts, a ceiling on gRPC deadlines.**
- **Password policy** — ≥ 8 characters, common values rejected (domain rule); bcrypt's 72-byte cap is handled in the bcrypt adapter.
- **Passwords and tokens never reach the log**, at any level.
