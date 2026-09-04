# ---- stage 1: compile ----
FROM golang:1.27.1-alpine AS builder

WORKDIR /src

# Copy the dependency files first, so this layer stays cached:
# for as long as go.mod / go.sum do not change, the next build skips the whole download.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
# GO_TAGS=dev adds the grpcui console at /grpcui — compose passes it in; the production image leaves it empty.
ARG GO_TAGS=""

# CGO_ENABLED=0 makes the binary static, so it runs on an image with no libc.
# -ldflags "-s -w" strips the debug information, cutting the file size by roughly a third.
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -tags "${GO_TAGS}" \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/api ./cmd/api

# ---- stage 2: run ----
FROM gcr.io/distroless/static-debian13:nonroot

COPY --from=builder /out/api /api

USER nonroot:nonroot
EXPOSE 8080 9090

ENTRYPOINT ["/api"]
