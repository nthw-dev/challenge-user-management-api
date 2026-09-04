// Command api is the composition root — the one place in the system that knows the whole world.
//
// MongoDB, bcrypt and JWT are injected into the use cases here.
// Every remaining package knows only the interfaces it declared for itself.
//
// run reads top to bottom in the order things come to life: configuration, storage, the core, observability,
// the two transports, the worker, and finally the lifecycle that runs and drains them. Each step lives in its own file.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	// A blank import, so that the init() of the package swag generates registers the spec into the registry
	// and Swagger UI at /swagger can find doc.json — without the adapter layer knowing this package at all.
	_ "github.com/nthw-dev/user-management-api/openapi"

	"google.golang.org/grpc/health"

	grpcapi "github.com/nthw-dev/user-management-api/internal/adapter/inbound/grpc"
	httpapi "github.com/nthw-dev/user-management-api/internal/adapter/inbound/http"
	"github.com/nthw-dev/user-management-api/internal/adapter/inbound/worker"
	"github.com/nthw-dev/user-management-api/internal/platform/config"
	"github.com/nthw-dev/user-management-api/internal/platform/logger"
)

// version is replaced at build time via -ldflags "-X main.version=...".
var version = "dev"

// bootTimeout bounds everything that has to happen before the ports open — connecting and building indexes.
const bootTimeout = 30 * time.Second

//	@title			User Management API
//	@version		1.0
//	@description	### Authorization
//	@description	`/api/v1/users/*` requires a token; `/api/v1/auth/*`, `/healthz`, `/readyz` and `/metrics` do not (see the padlock icon at the end of each row)
//	@description
//	@description	1. `POST /api/v1/auth/register` (if no user exists yet) → `POST /api/v1/auth/login` → copy `data.access_token`
//	@description	2. Click **Authorize** at the top right and enter `Bearer <access_token>` — **you must type the `Bearer ` prefix yourself**. The spec is OpenAPI 2.0 and declares `Authorization` as an apiKey, so the page sends the value verbatim and adds nothing
//	@description	3. Once it expires per `JWT_ACCESS_TTL`, call `POST /api/v1/auth/refresh` (the old token stops working; reusing it wipes every session) and Authorize again
//	@description
//	@description	The same contract can be called over gRPC at /grpcui/ — there you attach `authorization` as metadata instead of as a header

//	@license.name	MIT

//	@securityDefinitions.apikey	BearerAuth
//	@in							header
//	@name						Authorization
//	@description				Enter the value as "Bearer <access_token>", where access_token comes from /api/v1/auth/login

//	@tag.name			auth
//	@tag.description	Signing up, logging in, and rotating refresh tokens
//	@tag.name			users
//	@tag.description	User CRUD; every route requires an access token
//	@tag.name			system
//	@tag.description	Liveness and readiness probes

func main() {
	if err := run(); err != nil {
		// A bad config panics before reaching here; what remains are runtime failures.
		fmt.Fprintf(os.Stderr, "service stopped: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// The .env file is a development convenience only; its absence is fine.
	_ = godotenv.Load()

	// A bad configuration cannot be fixed at runtime — log it and panic before the port ever opens.
	cfg := config.MustLoad()

	log := logger.New(cfg.LogLevel, cfg.IsDevelopment())
	log.Info("starting service",
		slog.String("version", version),
		slog.String("env", cfg.AppEnv),
		slog.String("http_addr", cfg.HTTPAddr),
		slog.String("grpc_addr", cfg.GRPCAddr),
	)

	// ctx is cancelled on SIGINT or SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	bootCtx, cancelBoot := context.WithTimeout(ctx, bootTimeout)
	defer cancelBoot()

	store, closeStore, err := openStorage(bootCtx, cfg.Mongo, log)
	if err != nil {
		return err
	}
	defer closeStore()

	core, err := buildCore(cfg, store)
	if err != nil {
		return err
	}

	registry, userGauge := newMetrics()

	// The gRPC console comes up alongside the API in the same process, the same way Swagger UI does.
	// It is nil when not built with -tags dev, or when this is not development — and the router then does not mount /grpcui.
	grpcConsole, grpcConsoleAt := newGRPCConsole(cfg, log)

	// The two readiness switches shutdown flips before it closes anything: /readyz on REST, grpc.health.v1 on gRPC.
	// Both are owned here, so the composition root — and only it — can say "no more work" to a load balancer.
	var draining atomic.Bool
	healthSrv := health.NewServer()

	router := httpapi.NewRouter(httpapi.Deps{
		Users:       core.users,
		Auth:        core.auth,
		Tokens:      core.tokens,
		Logger:      log,
		Ready:       store.ready,
		Draining:    draining.Load,
		Registry:    registry,
		Docs:        cfg.IsDevelopment(),
		GRPCConsole: grpcConsole,
	})
	grpcSrv := grpcapi.NewServer(grpcapi.Deps{
		Users:      core.users,
		Auth:       core.auth,
		Tokens:     core.tokens,
		Logger:     log,
		RPCTimeout: cfg.Server.RPCTimeout,
		Reflect:    cfg.IsDevelopment(),
		Health:     healthSrv,
	})

	// Both ports are bound here, before anything starts — so an address already in use fails the boot, not a goroutine.
	// Binding is part of boot, so it runs under the boot deadline like the storage connection does.
	var lc net.ListenConfig
	httpLis, err := lc.Listen(bootCtx, "tcp", cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("open the HTTP port: %w", err)
	}
	grpcLis, err := lc.Listen(bootCtx, "tcp", cfg.GRPCAddr)
	if err != nil {
		return fmt.Errorf("open the gRPC port: %w", err)
	}

	counter := worker.NewUserCounter(core.users, log, cfg.CountInterval, func(n int64) {
		userGauge.Set(float64(n))
	})

	logDevURLs(cfg, log, grpcConsole != nil, grpcConsoleAt)

	return serve(ctx, log, servers{
		http:     newHTTPServer(cfg.Server, router),
		httpLis:  httpLis,
		grpc:     grpcSrv,
		grpcLis:  grpcLis,
		worker:   counter,
		draining: &draining,
		health:   healthSrv,
		delayFor: cfg.ShutdownDelay,
		drainFor: cfg.ShutdownTimeout,
	})
}
