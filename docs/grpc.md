# gRPC — tooling

[README](../README.md) · [Checklist](../checklist.md) · [Architecture](architecture.md) · [Configuration](configuration.md) · [JWT](jwt-guide.md) · [REST API](rest-api.md) · [gRPC API](grpc-api.md) · [Testing](testing.md) · [Decisions](design-decisions.md)

The contract itself — every rpc, message and status — is in [grpc-api.md](grpc-api.md). This page is how to generate and call it.

gRPC was a bonus, and the best proof the layout works: a second transport with no change to the domain or the use cases.

```bash
make proto    # buf lint && buf generate → internal/adapter/inbound/grpc/gen/  (committed; CI regenerates and diffs)
```

## Calling it

```bash
grpcurl -plaintext localhost:9090 list     # reflection: development only

grpcurl -plaintext -d '{"name":"First","email":"a@example.com","password":"Str0ng-Passw0rd!"}' \
  localhost:9090 user.v1.AuthService/Register

TOKEN=$(grpcurl -plaintext -d '{"email":"a@example.com","password":"Str0ng-Passw0rd!"}' \
  localhost:9090 user.v1.AuthService/Login | jq -r .session.accessToken)

grpcurl -plaintext -H "authorization: Bearer $TOKEN" -d '{"limit":20,"query":"a@"}' localhost:9090 user.v1.UserService/ListUsers
grpcurl -plaintext -H "authorization: Bearer $TOKEN" -d '{"id":"<id>","name":"New Name"}' localhost:9090 user.v1.UserService/UpdateUser
grpcurl -plaintext localhost:9090 grpc.health.v1.Health/Check
```

`make test-grpc` runs every case against a running server and logs each exchange to `scripts/logs/`.

## The console at /grpcui/

`make up` and `make run` build with `-tags dev`, which compiles [`grpcconsole`](../internal/adapter/inbound/grpcconsole/) in and mounts
grpcui at **http://localhost:8080/grpcui/** next to Swagger. The page has a short guide, the `authorization` row prefilled with `Bearer `,
and one-click examples for all eight methods in the order you would use them ([`grpc/console.go`](../internal/adapter/inbound/grpc/console.go)).
It is built on the first request (reflection needs the gRPC port, which is not open when the router is assembled) and served only when
`APP_ENV=development`. `make build` has no tag: the production binary carries none of it (32.1 → 35.9 MB on linux/amd64 is grpcui's share).

`make grpcui` runs the standalone binary instead, `GRPC_HOST=host:port` to point it elsewhere.
