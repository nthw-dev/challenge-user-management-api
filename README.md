# user-management-api

A user-management REST API in Go — MongoDB, JWT (HS256), hexagonal layout, with a gRPC adapter on the same core.

This is **Part 1** of [backend-challenge](https://github.com/7-solutions/backend-challenge)
— the User Management API section, a build exercise. Author: **[Natthawat Narin](https://nthw-dev.vercel.app/)**.

The challenge as received is [challenge-user-management-api.md](challenge-user-management-api.md); every
requirement in it is answered one by one in **[checklist.md](checklist.md)**.

---

## Run it

Only **Docker** is needed — no Go, no MongoDB, no config to edit.

```bash
make up      # builds the image, starts API + MongoDB, tails the log
```

### Running
```sh
api-1  | time=yyyy-MM-ddTHH:MM:SSZ level=INFO msg="starting service" version=local env=development http_addr=:8080 grpc_addr=:9090
api-1  | time=yyyy-MM-ddTHH:MM:SSZ level=INFO msg="Swagger UI — try the REST API from a browser" url=http://localhost:8080/swagger/
api-1  | time=yyyy-MM-ddTHH:MM:SSZ level=INFO msg="gRPC console — try the gRPC API from a browser" url=http://localhost:8080/grpcui/
api-1  | time=yyyy-MM-ddTHH:MM:SSZ level=INFO msg="HTTP server ready" addr=[::]:8080
api-1  | time=yyyy-MM-ddTHH:MM:SSZ level=INFO msg="gRPC server ready" addr=[::]:9090
api-1  | time=yyyy-MM-ddTHH:MM:SSZ level=INFO msg="user_counter started" interval=10s
api-1  | time=yyyy-MM-ddTHH:MM:SSZ level=INFO msg=user_count total=0
```

Ready when the log shows `HTTP server ready` and `gRPC server ready`. Nothing else has to be installed to try it.

### Where to start (for a reviewer)

**1. Try the REST API — [http://localhost:8080/swagger/](http://localhost:8080/swagger/)**

Swagger UI calls every endpoint from the browser, no client needed. Start at
`POST /api/v1/auth/register`, then `POST /api/v1/auth/login` — copy `data.access_token` from the
response, press **Authorize** (top right), paste it as `Bearer <access_token>`, and every protected
route under `/api/v1/users` works from there.

Prefer Postman? Import both files in [`postman/`](postman/) — the collection registers, logs in and
attaches `Authorization: Bearer …` by itself, so any request can be sent straight away. Or
`make postman` to run all 27 requests / 69 assertions at once.

**2. Try the gRPC API — [http://localhost:8080/grpcui/](http://localhost:8080/grpcui/)**

The same core over gRPC, in a browser console — no `.proto` file, no grpcurl. It reads the contract
over reflection, so every service and rpc is already listed. Same order: `AuthService/Register`,
then `AuthService/Login`; for `UserService` the **Request Metadata** `authorization` row is already
waiting as `Bearer ` — paste the access token after it.

**3. See what was written — [http://localhost:3100](http://localhost:3100)**

A Mongo browser ([Mongoku](https://github.com/huggingface/mongoku)) already pointed at the running
database: open **userdb → users** to see the stored document (the password is a bcrypt hash, never
the password itself), and **refresh_tokens** to watch a token rotate on refresh and disappear on
logout. MongoDB is also on `localhost:27017` if you would rather use Compass or `mongosh`.

| | Address |
| --- | --- |
| Swagger UI — call every REST endpoint | http://localhost:8080/swagger/ |
| gRPC console — call every rpc | http://localhost:8080/grpcui/ |
| Mongo browser — see what was written | http://localhost:3100 |
| REST API | http://localhost:8080/api/v1 |
| gRPC | localhost:9090 |
| MongoDB | mongodb://localhost:27017 |

Everything can also be walked end to end from the terminal:

```bash
make py-deps     # once: the two walkthrough scripts below need Python 3.9+ and rich
make test-rest   # walks every REST endpoint and asserts each case   (Python only)
make test-grpc   # the same over gRPC                                (also needs grpcurl)
make postman     # the same REST walk through Postman's runner       (needs npx)
make down        # stop everything and wipe the data
```

### The walkthrough scripts

[`scripts/test_rest.py`](scripts/test_rest.py) and [`scripts/test_grpc.py`](scripts/test_grpc.py) call a running
server the way a client really would — sign up, log in, page, rotate, delete — and assert every case, printing the
run as it goes and writing each exchange in full to `scripts/logs/`. They exit non-zero if any case fails,
so they drop straight into CI.

Everything they use is the standard library except **[rich](https://github.com/Textualize/rich)**, which is what
makes the console readable — panels, rules, highlighted JSON, a pass/fail tally at the end:

```bash
make py-deps                                       # python3 -m pip install -r scripts/requirements.txt

# or keep it in a virtualenv, and point the make targets at it:
python3 -m venv .venv && .venv/bin/pip install -r scripts/requirements.txt
make test-rest PY=.venv/bin/python
```

`make test-grpc` also needs the **grpcurl** binary (`make tools-install`) — it reads the contract over reflection,
so no `.proto` file has to be pointed at, and the server must run with `APP_ENV=development` for reflection to be on.

Both scripts take the target and the account from flags or environment variables:

```bash
./scripts/test_rest.py --base http://localhost:8080 --log-dir /tmp/api-log
./scripts/test_grpc.py --grpc localhost:9090 --no-color        # --no-color for CI logs
BASE=http://localhost:8080 ./scripts/test_rest.py              # the env vars still work
./scripts/test_rest.py --help                                  # every flag
```

### Postman

Import both files in [`postman/`](postman/), select the environment, press
**Send** on anything — a collection script registers, logs in and attaches
`Authorization: Bearer …` for you. 27 requests, 69 assertions, re-runnable.

A quick call by hand:

```bash
curl -s -X POST localhost:8080/api/v1/auth/register -H 'Content-Type: application/json' \
  -d '{"name":"Tester","email":"tester@example.com","password":"Str0ng-Passw0rd!"}'

TOKEN=$(curl -s -X POST localhost:8080/api/v1/auth/login -H 'Content-Type: application/json' \
  -d '{"email":"tester@example.com","password":"Str0ng-Passw0rd!"}' | jq -r .data.access_token)

curl -s localhost:8080/api/v1/users -H "Authorization: Bearer $TOKEN" | jq
```

---

## Develop on it (macOS)

Pinned to **Go 1.27.1** ([go.mod](go.mod)); the `go` formula tracks it.

```bash
brew install go golangci-lint direnv
make tools-install                        # swag, mockery, grpcui, grpcurl, buf, protoc plugins, govulncheck — pinned to CI's versions
echo 'eval "$(direnv hook zsh)"' >> ~/.zshrc && exec zsh
cp .env.example .env && direnv allow      # .env is exported on cd, unexported on leave; gitignored
```

`GOBIN` should be `~/go/bin` and on your `PATH`. Every setting is an env var — see [docs/configuration.md](docs/configuration.md).

```bash
docker compose up -d mongo   # MongoDB alone
make run                     # go run -tags dev ./cmd/api, reading .env
```

### Commands

`make help` lists all of them. The ones you will reach for:

```
make test          unit tests, -race always on
make test-int      integration tests on real MongoDB (testcontainers — needs Docker)
make cover-report  unit + integration coverage merged into coverage/summary.md and coverage.html
make cover-check   fail if the core (domain + app) drops below 80% — the same check CI runs
make lint          golangci-lint
make mocks         regenerate the mockery mocks (commit the output; CI diffs it)
make proto         buf lint && buf generate      (commit the output; CI diffs it)
make swagger       regenerate the OpenAPI spec from the annotations
make build         production binary into bin/api (no grpcui)
make py-deps       install rich, which scripts/test_rest.py and scripts/test_grpc.py need
make test-rest     walk every REST endpoint against a running server, asserting each case
make test-grpc     the same over gRPC (needs grpcurl)
```

---

## Documentation

| | |
| --- | --- |
| **[checklist.md](checklist.md)** | Every requirement in the brief — done or not, how, and where in the code |
| [docs/architecture.md](docs/architecture.md) | The hexagonal layout and where everything lives |
| [docs/configuration.md](docs/configuration.md) | Every environment variable and its default |
| [docs/jwt-guide.md](docs/jwt-guide.md) | Generating and using a JWT, and what is inside one |
| [docs/rest-api.md](docs/rest-api.md) | The REST contract: every endpoint, request, response and error, with where the code is |
| [docs/grpc-api.md](docs/grpc-api.md) | The gRPC contract, the same way |
| [docs/grpc.md](docs/grpc.md) | Generating the gRPC code, calling it, the browser console |
| [docs/testing.md](docs/testing.md) | The four test tiers, coverage, and where the mocking boundary sits |
| [docs/design-decisions.md](docs/design-decisions.md) | Assumptions, decisions, and what was deliberately left out |
| [docs/dependencies.md](docs/dependencies.md) | Every dependency, its version, why it was chosen and what it was picked over |

---

## The other part

**Part 2** of the same challenge — the Lottery Search System, a design exercise with no code required —
is in a separate repository:
**[github.com/nthw-dev/challenge-lottery-search-system](https://github.com/nthw-dev/challenge-lottery-search-system)**.

---

Written for a programming skills assessment.