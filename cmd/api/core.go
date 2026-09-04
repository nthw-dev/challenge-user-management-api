package main

import (
	"github.com/nthw-dev/user-management-api/internal/adapter/outbound/clock"
	"github.com/nthw-dev/user-management-api/internal/adapter/outbound/security"
	"github.com/nthw-dev/user-management-api/internal/adapter/outbound/token"
	"github.com/nthw-dev/user-management-api/internal/app"
	"github.com/nthw-dev/user-management-api/internal/platform/config"
)

// core is the use cases, plus the one outbound service the transports call directly (token verification).
type core struct {
	users  *app.UserService
	auth   *app.AuthService
	tokens *token.JWTService
}

// buildCore is where the ports meet their implementations — the only function in the system that names bcrypt and JWT next to a use case.
func buildCore(cfg config.Config, st *storage) (core, error) {
	sysClock := clock.System{}
	hasher := security.NewBcryptHasher(cfg.Bcrypt.Cost)
	jwtService := token.NewJWTService(cfg.JWT.Secret, cfg.JWT.Issuer, cfg.JWT.Audience, sysClock)

	users := app.NewUserService(st.users, st.refresh, hasher, sysClock)
	auth, err := app.NewAuthService(st.users, st.refresh, hasher, jwtService, sysClock, app.AuthConfig{
		AccessTTL:  cfg.JWT.AccessTTL,
		RefreshTTL: cfg.JWT.RefreshTTL,
	})
	if err != nil {
		return core{}, err
	}
	return core{users: users, auth: auth, tokens: jwtService}, nil
}
