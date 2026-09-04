//go:build !dev

package main

import (
	"log/slog"
	"net/http"

	"github.com/nthw-dev/user-management-api/internal/platform/config"
)

// The production binary carries no grpcui and none of its dependencies — the router sees nil and so does not mount /grpcui.
func newGRPCConsole(config.Config, *slog.Logger) (http.Handler, string) { return nil, "" }
