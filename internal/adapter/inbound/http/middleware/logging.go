package middleware

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/nthw-dev/user-management-api/internal/adapter/inbound/actor"
	"github.com/nthw-dev/user-management-api/internal/adapter/inbound/http/reqctx"
)

// Logging records the method, path and elapsed time — requirement 5 of the brief.
func Logging(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Authenticate runs further in and sets the actor on a context this layer never sees —
			// reserving a slot here is what lets its id reach the line written below.
			ctx := actor.Reserve(r.Context())
			r = r.WithContext(ctx)

			rw := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rw, r)

			// One line per request, always written at the end,
			// where the status, the size and the elapsed time are all known — and the log volume does not double.
			attrs := []slog.Attr{
				slog.String("method", r.Method),
				// Path rather than String(), so values in the query string do not leak into the log.
				slog.String("path", r.URL.Path),
				slog.Int("status", rw.status),
				// Stored as a number rather than as text like "183ms", so a log aggregator can compute percentiles right away.
				slog.Int64("duration_ms", time.Since(start).Milliseconds()),
				slog.Int("bytes", rw.bytes),
				slog.String("request_id", reqctx.RequestID(ctx)),
				slog.String("remote_ip", reqctx.RealIP(ctx)),
			}
			// Who did it — present on authenticated routes only, so a public route's line does not carry an empty key.
			if id := actor.ID(ctx); id != "" {
				attrs = append(attrs, slog.String("actor_id", id))
			}
			log.LogAttrs(ctx, slog.LevelInfo, "http_request", attrs...)
		})
	}
}

// responseRecorder captures the status and the number of bytes written out.
type responseRecorder struct {
	http.ResponseWriter
	status  int
	bytes   int
	written bool
}

func (rw *responseRecorder) WriteHeader(status int) {
	if rw.written {
		return
	}
	rw.status = status
	rw.written = true
	rw.ResponseWriter.WriteHeader(status)
}

func (rw *responseRecorder) Write(b []byte) (int, error) {
	if !rw.written {
		rw.WriteHeader(http.StatusOK)
	}
	n, err := rw.ResponseWriter.Write(b)
	rw.bytes += n
	return n, err
}

// Flush and Unwrap have to be here, otherwise streaming responses and hijacking break.
// It is the classic mistake of middleware that wraps a ResponseWriter.
func (rw *responseRecorder) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (rw *responseRecorder) Unwrap() http.ResponseWriter { return rw.ResponseWriter }
