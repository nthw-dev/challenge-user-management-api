//go:build dev

// Package grpcconsole wraps grpcui's web page into a single ordinary http.Handler.
//
// It exists so the gRPC console comes up alongside the API in one process, the same way Swagger UI does —
// no separate grpcui binary to run, and no second port to remember.
package grpcconsole

import (
	"context"
	"log/slog"
	"net/http"
	"sync"

	"github.com/fullstorydev/grpcui/standalone"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/nthw-dev/user-management-api/internal/platform/netaddr"
)

// Console builds the page lazily on the first request rather than at router assembly time.
//
// The reason is that this page reads the method list over reflection, which requires connecting to the gRPC port first,
// and that port is not yet open when main assembles the router — so building it then would always fail.
// A failed build is also not cached, so the next request gets a fresh attempt.
type Console struct {
	target string
	log    *slog.Logger
	guide  *Guide

	mu      sync.Mutex
	handler http.Handler
}

// Option tunes the console at construction time — with none of them you get grpcui's default page as is.
type Option func(*Console)

// WithGuide hangs a guide panel above the form, and prefills the metadata row and the examples list as the Guide specifies.
func WithGuide(g Guide) Option {
	return func(c *Console) { c.guide = &g }
}

// New takes an address in the same form as GRPC_ADDR, ":9090" for instance, and fills in the host itself.
func New(grpcAddr string, log *slog.Logger, opts ...Option) *Console {
	if log == nil {
		log = slog.Default()
	}
	c := &Console{target: netaddr.Dialable(grpcAddr), log: log}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Target reports where this console points, for use when logging what has been made available.
func (c *Console) Target() string { return c.target }

func (c *Console) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h, err := c.build(r.Context())
	if err != nil {
		c.log.LogAttrs(r.Context(), slog.LevelWarn, "grpc_console_unavailable",
			slog.String("target", c.target), slog.Any("err", err))

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"status":"unavailable"}`))
		return
	}
	h.ServeHTTP(w, r)
}

func (c *Console) build(ctx context.Context) (http.Handler, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.handler != nil {
		return c.handler, nil
	}

	// This dials back into our own process, so there is no TLS to verify and no real network to cross.
	cc, err := grpc.NewClient(c.target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}

	uiOpts, err := c.guide.handlerOptions()
	if err != nil {
		_ = cc.Close()
		return nil, err
	}

	h, err := standalone.HandlerViaReflection(ctx, cc, c.target, uiOpts...)
	if err != nil {
		_ = cc.Close()
		return nil, err
	}

	c.handler = h
	return h, nil
}
