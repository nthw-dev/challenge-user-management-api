package main

import (
	"log/slog"
	"net/http"

	"github.com/nthw-dev/user-management-api/internal/platform/config"
	"github.com/nthw-dev/user-management-api/internal/platform/netaddr"
)

// maxHeaderBytes caps the request head — 1MB is far beyond any legitimate set of headers.
const maxHeaderBytes = 1 << 20

// newHTTPServer applies the timeouts from config. net/http's default is "no timeout at all", which leaves the door open for slowloris;
// the real numbers come from config alone, with no second layer of defaults on top.
func newHTTPServer(cfg config.Server, handler http.Handler) *http.Server {
	return &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout, // allows for the bcrypt that costs hundreds of milliseconds at login
		IdleTimeout:       cfg.IdleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
	}
}

// logDevURLs prints the developer surfaces in full form so they can be clicked straight from a terminal.
// A listen address like ":8080" is not clickable, so it goes through netaddr first. Nothing is printed outside development.
func logDevURLs(cfg config.Config, log *slog.Logger, hasGRPCConsole bool, grpcConsoleTarget string) {
	if !cfg.IsDevelopment() {
		return
	}
	base := netaddr.LocalURL(cfg.HTTPAddr)
	log.Info("Swagger UI — try the REST API from a browser", slog.String("url", base+"/swagger/"))
	if hasGRPCConsole {
		log.Info("gRPC console — try the gRPC API from a browser",
			slog.String("url", base+"/grpcui/"),
			slog.String("target", grpcConsoleTarget))
	}
}
