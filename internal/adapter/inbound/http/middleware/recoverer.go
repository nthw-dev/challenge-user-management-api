package middleware

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/nthw-dev/user-management-api/internal/adapter/inbound/http/reqctx"
	"github.com/nthw-dev/user-management-api/internal/adapter/inbound/http/respond"
)

// Recoverer cannot sit outside Logger — if a panic happens, we still want a log line for that request.
func Recoverer(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				// The caller disconnected mid-flight, which is not a failure of ours.
				if err, ok := rec.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(rec)
				}

				log.LogAttrs(r.Context(), slog.LevelError, "panic_recovered",
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.String("request_id", reqctx.RequestID(r.Context())),
					slog.Any("panic", rec),
					slog.String("stack", string(debug.Stack())),
				)

				respond.Error(w, r, log, fmt.Errorf("panic: %v", rec))
			}()

			next.ServeHTTP(w, r)
		})
	}
}
