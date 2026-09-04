package middleware

import (
	"log/slog"
	"net/http"

	"github.com/nthw-dev/user-management-api/internal/adapter/inbound/actor"
	"github.com/nthw-dev/user-management-api/internal/adapter/inbound/apierr"
	"github.com/nthw-dev/user-management-api/internal/adapter/inbound/bearer"
	"github.com/nthw-dev/user-management-api/internal/adapter/inbound/http/respond"
	"github.com/nthw-dev/user-management-api/internal/app"
)

// Authenticate verifies the JWT and lets the request through — it never touches the database, so verification is pure computation.
// The verified subject becomes the request's actor: handlers read it with actor.ID and hand it to the use case,
// and the Logging middleware outside picks it up for the request line.
func Authenticate(v app.TokenVerifier, log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, ok := bearer.Token(r.Header.Get("Authorization"))
			if !ok {
				respond.Error(w, r, log, apierr.ErrUnauthenticated)
				return
			}

			userID, err := v.Verify(raw)
			if err != nil {
				// We never say what was wrong with the token — signature, expiry, or a mismatched audience
				// are all details that help an attacker adjust their approach.
				respond.Error(w, r, log, apierr.ErrUnauthenticated)
				return
			}

			next.ServeHTTP(w, r.WithContext(actor.Set(r.Context(), userID)))
		})
	}
}
