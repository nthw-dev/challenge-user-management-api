package worker_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nthw-dev/user-management-api/internal/adapter/inbound/worker"
	"github.com/nthw-dev/user-management-api/internal/app"
	"github.com/nthw-dev/user-management-api/internal/domain/user"
)

// The worker calls through a use case like any other handler, so it can be tested with the same doubles.
type fakeCounter struct {
	calls atomic.Int64
	err   error
}

func (f *fakeCounter) Count(context.Context) (int64, error) {
	n := f.calls.Add(1)
	if f.err != nil {
		return 0, f.err
	}
	return n * 10, nil
}

func (f *fakeCounter) Create(context.Context, app.CreateUserInput) (*user.User, error) {
	return nil, nil
}
func (f *fakeCounter) Get(context.Context, string) (*user.User, error) { return nil, nil }
func (f *fakeCounter) List(context.Context, app.ListFilter) (app.Page, error) {
	return app.Page{}, nil
}
func (f *fakeCounter) Update(context.Context, string, string, app.UpdateUserInput) (*user.User, error) {
	return nil, nil
}
func (f *fakeCounter) Delete(context.Context, string, string) error { return nil }

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestUserCounter_CountsOnEveryTick(t *testing.T) {
	t.Parallel()

	var (
		mu       sync.Mutex
		observed []int64
	)
	counts := &fakeCounter{}

	w := worker.NewUserCounter(counts, discardLogger(), 10*time.Millisecond, func(n int64) {
		mu.Lock()
		defer mu.Unlock()
		observed = append(observed, n)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()

	require.NoError(t, w.Run(ctx))

	mu.Lock()
	defer mu.Unlock()
	require.GreaterOrEqual(t, len(observed), 3, "should manage several rounds within 120 milliseconds")
	require.Equal(t, int64(10), observed[0])
}

// ctx.Done() sits inside the select, which makes it stop at once rather than waiting out the interval.
// That is the difference between shutting down in 100 milliseconds and shutting down in 10 seconds.
func TestUserCounter_StopsImmediatelyOnCancel(t *testing.T) {
	t.Parallel()

	w := worker.NewUserCounter(&fakeCounter{}, discardLogger(), time.Hour, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	cancel()

	select {
	case err := <-done:
		require.NoError(t, err, "shutting down on command is not a failure")
	case <-time.After(time.Second):
		t.Fatal("the worker did not stop within the allotted time")
	}
}

// Missing one round of counting is no reason to take the whole service down.
// Returning the error would make errgroup shut everything down with it, which makes no sense for a statistics job.
func TestUserCounter_KeepsGoingAfterFailure(t *testing.T) {
	t.Parallel()

	counts := &fakeCounter{err: errors.New("mongo is down")}
	w := worker.NewUserCounter(counts, discardLogger(), 10*time.Millisecond, func(int64) {
		t.Error("no value should be emitted when the count fails")
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()

	require.NoError(t, w.Run(ctx))
	require.GreaterOrEqual(t, counts.calls.Load(), int64(2), "it must retry on the next round")
}
