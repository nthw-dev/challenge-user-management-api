.DEFAULT_GOAL := help
.PHONY: help up down logs run build test test-int cover cover-int cover-report cover-html cover-check mocks lint fmt fmt-check proto swagger grpcui tools-install py-deps test-rest test-grpc postman tidy tidy-check

# The grpcui target — override at call time, e.g. make grpcui GRPC_HOST=localhost:19090
GRPC_HOST ?= localhost:9090

# The interpreter the walkthrough scripts run under — point it at a venv if you keep one,
# e.g. make test-rest PY=.venv/bin/python
PY ?= python3

help: ## Show every command
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

up: ## Bring the whole system up (API + MongoDB), then tail the logs
	docker compose up --build -d && docker compose logs -f api

down: ## Shut down and remove the volume
	docker compose down -v

logs: ## Tail the API's logs
	docker compose logs -f api

run: ## Run the API alone on this machine, against MongoDB from compose (-tags dev enables /grpcui)
	go run -tags dev ./cmd/api

build: ## Compile the production binary into bin/api (no grpcui — add GO_TAGS=dev if you want it)
	CGO_ENABLED=0 go build -trimpath -tags "$(GO_TAGS)" -ldflags="-s -w" -o bin/api ./cmd/api

test: ## Unit tests with the race detector (-tags dev so the grpcconsole tests run too)
	go test -race -tags dev -covermode=atomic -coverpkg=./internal/... -coverprofile=cover.out ./...

test-int: ## Integration tests against real MongoDB via testcontainers (requires Docker)
	go test -tags=integration -count=1 ./test/...

py-deps: ## Install what scripts/test_rest.py and scripts/test_grpc.py import (rich)
	$(PY) -m pip install -r scripts/requirements.txt

test-rest: ## Call every REST endpoint for real, asserting each case, with logs kept in scripts/logs (needs make py-deps)
	$(PY) scripts/test_rest.py

test-grpc: ## Call every gRPC rpc for real, asserting each case, with logs kept in scripts/logs (needs make py-deps + grpcurl)
	$(PY) scripts/test_grpc.py

postman: ## Run the REST Postman collection with newman
	npx --yes newman run postman/user-management-api.postman_collection.json \
		-e postman/local.postman_environment.json

cover: test ## Show coverage per package, and the total
	go tool cover -func=cover.out | tail -n 1

cover-int: ## Integration tests with a coverage profile of the code they exercise, into coverage/integration.out (requires Docker)
	@mkdir -p coverage
	go test -tags=integration -count=1 -covermode=atomic -coverpkg=./internal/... -coverprofile=coverage/integration.out ./test/...

cover-report: ## Unit + integration coverage, merged, as coverage/coverage.html and coverage/summary.md for local review
	./scripts/coverage-report.sh

cover-html: test ## Open the unit-test coverage in the browser, file by file
	go tool cover -html=cover.out

cover-check: test ## Fail if the core (internal/domain + internal/app) is below 80% — the same check CI runs
	./scripts/check-coverage.sh cover.out 80

lint: ## Check code quality with golangci-lint
	golangci-lint run

fmt: ## Format the code
	gofmt -w .

fmt-check: ## Fail if any file is not gofmt-formatted (what CI runs)
	@files=$$(gofmt -l cmd internal test); if [ -n "$$files" ]; then echo "not formatted:"; echo "$$files"; exit 1; fi

proto: ## Lint the .proto files and generate code from them
	buf lint && buf generate

mocks: ## Regenerate the testify mocks for every port in internal/app/ports.go, into internal/app/apptest/mocks (needs mockery)
	mockery

swagger: ## Generate the OpenAPI spec from the annotations in the code, into openapi
	swag fmt -d ./ -g cmd/api/main.go
	swag init -g cmd/api/main.go -d ./ -o openapi --packageName openapi --parseInternal --parseDepth 2
	@printf 'Swagger UI: \033[36mhttp://localhost:8080/swagger/\033[0m (only served when APP_ENV=development)\n'

grpcui: ## Launch grpcui as a separate process, to point at another target — the API serves its own at /grpcui when built with -tags dev
	grpcui -plaintext $(GRPC_HOST)

tools-install: ## Install the tools the targets above call, pinned (swag, mockery, grpcui, grpcurl, buf, protoc plugins, govulncheck); golangci-lint comes from brew or its installer
	go install github.com/swaggo/swag/cmd/swag@v1.16.6
	go install github.com/vektra/mockery/v3@v3.7.4
	go install github.com/fullstorydev/grpcui/cmd/grpcui@v1.5.4
	go install github.com/fullstorydev/grpcurl/cmd/grpcurl@v1.9.4
	go install github.com/bufbuild/buf/cmd/buf@v1.72.0
	go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.2
	go install golang.org/x/vuln/cmd/govulncheck@v1.7.0

tidy: ## Tidy up go.mod / go.sum
	go mod tidy

tidy-check: ## Fail if go.mod / go.sum are not tidy (what CI runs)
	go mod tidy && git diff --exit-code go.mod go.sum
