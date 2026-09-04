# user-management-api

A user-management REST API in Go — MongoDB, JWT (HS256), hexagonal layout, with a gRPC adapter on the same core.

Built against [challenge-user-management-api.md](challenge-user-management-api.md); every requirement is answered
one by one in **[checklist.md](checklist.md)**.

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