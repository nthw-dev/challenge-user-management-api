package grpcapi

import (
	"log/slog"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	userv1 "github.com/nthw-dev/user-management-api/internal/adapter/inbound/grpc/gen/user/v1"
	"github.com/nthw-dev/user-management-api/internal/app"
)

// Deps is everything the server needs, all injected from main.go — the same shape as the REST router's Deps.
type Deps struct {
	Users  app.UserUseCase
	Auth   app.AuthUseCase
	Tokens app.TokenVerifier
	Logger *slog.Logger

	// RPCTimeout is the ceiling on every call: a shorter deadline from the caller is honored, a longer one is capped.
	RPCTimeout time.Duration

	// Reflect is on in development only — in production it helps an attacker enumerate the methods.
	Reflect bool

	// Health, when set, is the health server the composition root keeps a handle on so it can flip every service to
	// NOT_SERVING before draining. Left nil, the server makes its own and it simply stays SERVING for its whole life.
	Health *health.Server
}

// mustBeComplete refuses to build a server that would only fail on its first call — a wiring bug belongs at boot.
func (d Deps) mustBeComplete() {
	var missing []string
	if d.Users == nil {
		missing = append(missing, "Users")
	}
	if d.Auth == nil {
		missing = append(missing, "Auth")
	}
	if d.Tokens == nil {
		missing = append(missing, "Tokens")
	}
	if d.RPCTimeout <= 0 {
		missing = append(missing, "RPCTimeout")
	}
	if len(missing) > 0 {
		panic("grpcapi: Deps is missing " + strings.Join(missing, ", "))
	}
}

// NewServer assembles the gRPC server together with its interceptor chain,
// ordered the same way as the HTTP middleware: recovery → logging → error translation → deadline → auth.
func NewServer(d Deps) *grpc.Server {
	d.mustBeComplete()
	log := d.Logger
	if log == nil {
		log = slog.Default()
	}

	srv := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			recoveryUnary(log),
			loggingUnary(log),
			errorsUnary(log),
			timeoutUnary(d.RPCTimeout),
			authUnary(d.Tokens),
		),
	)

	userv1.RegisterUserServiceServer(srv, &userService{users: d.Users})
	// AuthService is where a caller who speaks only gRPC gets a token.
	userv1.RegisterAuthServiceServer(srv, &authService{users: d.Users, auth: d.Auth})

	// grpc_health_v1 is served so an orchestrator can health-check us the standard way Kubernetes supports.
	hs := d.Health
	if hs == nil {
		hs = health.NewServer()
	}
	hs.SetServingStatus("", healthv1.HealthCheckResponse_SERVING)
	healthv1.RegisterHealthServer(srv, hs)

	if d.Reflect {
		reflection.Register(srv)
	}
	return srv
}
