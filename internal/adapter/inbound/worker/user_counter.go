// Package worker is an inbound adapter driven by time rather than by requests.
//
// It calls a use case exactly as an HTTP handler does; it does not quietly connect to MongoDB itself.
// As a result this background job can be tested with a fake repository too.
package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/nthw-dev/user-management-api/internal/app"
)

// countTimeout is shorter than the interval, so a stalled round cannot overlap the next one.
const countTimeout = 5 * time.Second

type UserCounter struct {
	users    app.UserUseCase
	log      *slog.Logger
	interval time.Duration

	// observe is the hook metrics attach through, without the worker having to know about Prometheus.
	observe func(int64)
}

// NewUserCounter trusts that interval > 0 — config is what rejects an unusable value, at boot.
func NewUserCounter(users app.UserUseCase, log *slog.Logger, interval time.Duration, observe func(int64)) *UserCounter {
	return &UserCounter{users: users, log: log, interval: interval, observe: observe}
}

// Run works until ctx is cancelled — requirement 6 of the brief.
func (w *UserCounter) Run(ctx context.Context) error {
	t := time.NewTicker(w.interval)
	defer t.Stop()

	w.log.LogAttrs(ctx, slog.LevelInfo, "user_counter started",
		slog.Duration("interval", w.interval))

	for {
		select {
		case <-ctx.Done():
			// ctx.Done() sits inside the select, so it stops the moment shutdown is signalled rather than waiting out the interval.
			// That is the difference between shutting down in 100 milliseconds and shutting down in 10 seconds.
			w.log.LogAttrs(ctx, slog.LevelInfo, "user_counter stopped",
				slog.String("reason", ctx.Err().Error()))
			return nil // shut down on command, which is not a failure

		case <-t.C:
			w.tick(ctx)
		}
	}
}

func (w *UserCounter) tick(ctx context.Context) {
	cctx, cancel := context.WithTimeout(ctx, countTimeout)
	defer cancel()

	n, err := w.users.Count(cctx)
	if err != nil {
		// Missing one round of counting is no reason to take the whole service down — log it and carry on.
		// Returning the error would make errgroup shut down the entire system, which makes no sense for a statistics job.
		w.log.LogAttrs(ctx, slog.LevelError, "failed to count users", slog.Any("err", err))
		return
	}

	if w.observe != nil {
		w.observe(n)
	}
	w.log.LogAttrs(ctx, slog.LevelInfo, "user_count", slog.Int64("total", n))
}
