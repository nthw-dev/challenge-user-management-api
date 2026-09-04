package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	httpSwagger "github.com/swaggo/http-swagger/v2"

	"github.com/nthw-dev/user-management-api/internal/adapter/inbound/http/middleware"
	"github.com/nthw-dev/user-management-api/internal/adapter/inbound/http/respond"
	"github.com/nthw-dev/user-management-api/internal/app"
)

// maxBodyBytes caps the accepted body size — 1MB is more than enough for one user's JSON.
const maxBodyBytes = 1 << 20

// Deps is everything the router needs, all injected from main.go.
// No global state and no init() opening a connection — so a test has full control.
type Deps struct {
	Users  app.UserUseCase
	Auth   app.AuthUseCase
	Tokens app.TokenVerifier
	Logger *slog.Logger

	// Ready reports whether we are ready to take traffic (normally by pinging MongoDB).
	Ready func(ctx context.Context) error

	// Draining, when it reports true, makes /readyz answer 503 without asking Ready — the composition root flips it
	// the moment shutdown begins, so a load balancer stops sending new work before the listener closes.
	Draining func() bool

	// Registry, when nil, leaves /metrics unmounted — so a test need not carry this dependency.
	Registry *prometheus.Registry

	// Docs turns on Swagger UI at /swagger — should only be on in development,
	// for the same reason as gRPC reflection: do not advertise the shape of the API to outsiders in production.
	Docs bool

	// GRPCConsole, when non-nil, serves the grpcui web page at /grpcui.
	// The router does not know what is inside it, only that it is an http.Handler — main is what assembles it.
	GRPCConsole http.Handler
}

// mustBeComplete refuses to build a router that would only fail on its first request —
// a missing use case is a wiring bug, and a wiring bug should surface at boot, not in production traffic.
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
	if len(missing) > 0 {
		panic("httpapi: Deps is missing " + strings.Join(missing, ", "))
	}
}

func NewRouter(d Deps) http.Handler {
	d.mustBeComplete()
	if d.Logger == nil {
		d.Logger = slog.Default()
	}

	users := &userHandler{users: d.Users, log: d.Logger}
	auth := &authHandler{auth: d.Auth, users: d.Users, log: d.Logger}
	r := chi.NewRouter()

	// The order genuinely matters — RequestID has to come before everything else, because the rest use it as a reference,
	// and Recoverer must not sit inside Logger, otherwise a panicking request gets no log line.
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer(d.Logger))
	r.Use(middleware.Logging(d.Logger))
	if d.Registry != nil {
		r.Use(middleware.Metrics(d.Registry))
	}
	r.Use(middleware.MaxBytes(maxBodyBytes))

	// 404/405 go through the same error translator as everything else, so the response shape is identical throughout.
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		respond.Error(w, r, d.Logger, respond.ErrRouteNotFound)
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		respond.Error(w, r, d.Logger, respond.ErrMethodNotAllowed)
	})

	r.Route("/api/v1", func(r chi.Router) {
		r.Route("/auth", func(r chi.Router) {
			r.Post("/register", auth.Register)
			r.Post("/login", auth.Login)
			r.Post("/refresh", auth.Refresh)
		})

		r.Group(func(r chi.Router) {
			r.Use(middleware.Authenticate(d.Tokens, d.Logger))

			r.Post("/users", users.Create)
			r.Get("/users", users.List)
			r.Get("/users/{id}", users.Get)
			r.Patch("/users/{id}", users.Update)
			r.Delete("/users/{id}", users.Delete)
		})
	})

	if d.Docs {
		// The UI reads the spec from /swagger/doc.json, which this very handler serves.
		r.Get("/swagger", http.RedirectHandler("/swagger/index.html", http.StatusFound).ServeHTTP)
		r.Get("/swagger/*", httpSwagger.Handler(httpSwagger.URL("/swagger/doc.json")))
	}

	if d.GRPCConsole != nil {
		// grpcui's handler believes it lives at the root, so the prefix has to be stripped before handing over,
		// and it must be reached at /grpcui/ with the trailing slash, otherwise links in the page point at the wrong level.
		r.Get("/grpcui", http.RedirectHandler("/grpcui/", http.StatusFound).ServeHTTP)
		r.Mount("/grpcui/", http.StripPrefix("/grpcui", d.GRPCConsole))
	}

	r.Get("/healthz", healthz)
	r.Get("/readyz", readyz(d.Ready, d.Draining, d.Logger))

	if d.Registry != nil {
		// Access should be restricted to internal callers — in a real system, blocked at the ingress or moved to a separate port.
		r.Handle("/metrics", promhttp.HandlerFor(d.Registry, promhttp.HandlerOpts{}))
	}

	return r
}
