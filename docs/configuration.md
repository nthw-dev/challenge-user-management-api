# Configuration

[README](../README.md) · [Checklist](../checklist.md) · [Architecture](architecture.md) · [JWT](jwt-guide.md) · [REST API](rest-api.md) · [gRPC API](grpc-api.md) · [gRPC tooling](grpc.md) · [Testing](testing.md) · [Decisions](design-decisions.md)

Everything is an environment variable, read once at boot ([`config.go`](../internal/platform/config/config.go)).
A missing or bad value stops the process before the port opens, and every problem is reported at once.
Only `MONGO_URI` and `JWT_SECRET` are required.

| Variable | Default | |
| --- | --- | --- |
| `APP_ENV` | `development` | `development` turns on Swagger UI, the grpcui console, gRPC reflection and text logs. Anything else is production: JSON logs, all three off |
| `HTTP_ADDR` / `GRPC_ADDR` | `:8080` / `:9090` | Bind addresses |
| `LOG_LEVEL` | `info` | `debug` `info` `warn` `error` |
| `MONGO_URI` | **required** | Connection string |
| `MONGO_DATABASE` | `userdb` | |
| `MONGO_COLLECTION` / `MONGO_REFRESH_COLLECTION` | `users` / `refresh_tokens` | |
| `MONGO_CONN_TIMEOUT` | `1m` | Opening one connection |
| `MONGO_MAX_CONN_IDLE_TIME` | `30m` | `0` = never close idle connections |
| `MONGO_MAX_IDLE_CONNS` / `MONGO_MAX_OPEN_CONNS` | `10` / `10` | Pool floor and cap; floor above cap refuses to boot |
| `JWT_SECRET` | **required** | ≥ 32 bytes or it refuses to boot |
| `JWT_ISSUER` / `JWT_AUDIENCE` | `user-service` / `user-service-api` | `iss` / `aud` claims |
| `JWT_ACCESS_TTL` / `JWT_REFRESH_TTL` | `15m` / `168h` | |
| `BCRYPT_COST` | `12` | Drop to 4 in tests |
| `USER_COUNT_INTERVAL` | `10s` | The counting goroutine — the brief says 10 s |
| `SHUTDOWN_DELAY` | `2s` | How long `/readyz` answers `draining` (and gRPC health `NOT_SERVING`) while still serving, before the listeners close. `0` is fine on a laptop |
| `SHUTDOWN_TIMEOUT` | `15s` | Time for in-flight work to finish after the listeners close |
| `SERVER_READ_HEADER_TIMEOUT` / `SERVER_READ_TIMEOUT` | `5s` / `5s` | Slowloris guard |
| `SERVER_WRITE_TIMEOUT` | `10s` | Room for bcrypt at login |
| `SERVER_IDLE_TIMEOUT` | `0` | `0` = fall back to `READ_TIMEOUT` |
| `SERVER_RPC_TIMEOUT` | `10s` | Ceiling on every gRPC call — a shorter client deadline is kept, a longer one is capped |

[`.env.example`](../.env.example) has all of them with comments. Locally, `cp .env.example .env && direnv allow` and they are exported on `cd` (`.env` is gitignored).
`docker-compose.yml` uses a dev `JWT_SECRET` unless one is exported in the shell, so `make up` stays zero-config but `JWT_SECRET=$(openssl rand -base64 48) make up` works.
