package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/nthw-dev/user-management-api/internal/adapter/inbound/http/respond"
)

// statusBody is the probes' answer — deliberately not wrapped in the data envelope, because orchestrators expect a flat object.
type statusBody struct {
	Status string `json:"status"`
}

// healthz reports that the process is alive; it touches no dependency.
// Tying MongoDB to liveness would mean a brief database outage restarts every pod at once.
//
//	@Summary		liveness probe
//	@Description	Answers 200 for as long as the process can serve requests; does not touch MongoDB
//	@Tags			system
//	@Produce		json
//	@Success		200	{object}	map[string]string	"{\"status\":\"ok\"}"
//	@Router			/healthz [get]
func healthz(w http.ResponseWriter, _ *http.Request) {
	respond.Plain(w, http.StatusOK, statusBody{Status: "ok"})
}

// readyz reports whether we are ready to take work — this is the one that should check dependencies,
// and the one that says "no more, please" the moment shutdown begins.
//
//	@Summary		readiness probe
//	@Description	Answers 503 {"status":"draining"} once shutdown has begun, so the load balancer stops sending work before the listener closes; otherwise pings MongoDB with a 2-second timeout and answers 503 {"status":"unavailable"} when it cannot be reached
//	@Tags			system
//	@Produce		json
//	@Success		200	{object}	map[string]string	"{\"status\":\"ok\"}"
//	@Failure		503	{object}	map[string]string	"{\"status\":\"unavailable\"} or {\"status\":\"draining\"}"
//	@Router			/readyz [get]
func readyz(ready func(context.Context) error, draining func() bool, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Draining comes first and skips the ping: the answer is "no" regardless of how the database is doing.
		if draining != nil && draining() {
			respond.Plain(w, http.StatusServiceUnavailable, statusBody{Status: "draining"})
			return
		}
		if ready == nil {
			healthz(w, r)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		if err := ready(ctx); err != nil {
			log.LogAttrs(r.Context(), slog.LevelWarn, "readiness_failed", slog.Any("err", err))
			respond.Plain(w, http.StatusServiceUnavailable, statusBody{Status: "unavailable"})
			return
		}
		healthz(w, r)
	}
}
