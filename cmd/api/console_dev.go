//go:build dev

package main

import (
	"log/slog"
	"net/http"

	grpcapi "github.com/nthw-dev/user-management-api/internal/adapter/inbound/grpc"
	"github.com/nthw-dev/user-management-api/internal/adapter/inbound/grpcconsole"
	"github.com/nthw-dev/user-management-api/internal/platform/config"
)

// newGRPCConsole assembles the grpcui page at /grpcui — present only in a binary built with -tags dev,
// and enabled only for APP_ENV=development, the same condition as reflection, since reflection is what makes it work.
//
// The guide comes from the gRPC adapter rather than from grpcconsole — whoever knows what the contract requires is the contract's owner.
func newGRPCConsole(cfg config.Config, log *slog.Logger) (http.Handler, string) {
	if !cfg.IsDevelopment() {
		return nil, ""
	}
	c := grpcconsole.New(cfg.GRPCAddr, log, grpcconsole.WithGuide(grpcapi.ConsoleGuide()))
	return c, c.Target()
}
