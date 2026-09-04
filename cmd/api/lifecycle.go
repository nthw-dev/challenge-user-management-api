package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"

	"github.com/nthw-dev/user-management-api/internal/adapter/inbound/worker"
)

// servers is everything that runs for the life of the process.
type servers struct {
	http    *http.Server
	httpLis net.Listener
	grpc    *grpc.Server
	grpcLis net.Listener
	worker  *worker.UserCounter

	// draining and health are the two readiness switches: flipping them is the first thing shutdown does,
	// so a load balancer learns to stop sending work before any listener closes.
	draining *atomic.Bool
	health   *health.Server

	// delayFor is how long readiness answers "draining" before the listeners close — the load balancer's notice period.
	delayFor time.Duration
	// drainFor is how long in-flight work gets to finish once the listeners have closed.
	drainFor time.Duration
}

// serve runs both transports and the worker until ctx is cancelled or one of them fails, then drains the rest.
func serve(ctx context.Context, log *slog.Logger, s servers) error {
	g, gctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		log.Info("HTTP server ready", slog.String("addr", s.httpLis.Addr().String()))
		if err := s.http.Serve(s.httpLis); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("http server: %w", err)
		}
		return nil
	})

	g.Go(func() error {
		log.Info("gRPC server ready", slog.String("addr", s.grpcLis.Addr().String()))
		if err := s.grpc.Serve(s.grpcLis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			return fmt.Errorf("grpc server: %w", err)
		}
		return nil
	})

	g.Go(func() error { return s.worker.Run(gctx) })

	// The gatekeeper: wait for the signal, then shut everything down in order.
	g.Go(func() error {
		return gracefulShutdown(gctx, log, s)
	})

	if err := g.Wait(); err != nil {
		log.Error("service stopped because of an error", slog.Any("err", err))
		return err
	}

	log.Info("service shut down cleanly")
	return nil
}

// gracefulShutdown waits until ctx is cancelled — whether from the SIGINT/SIGTERM that signal.NotifyContext
// intercepts, or from a sibling goroutine in the errgroup returning an error — and then shuts down in two steps:
//
//  1. Say no to new work while still serving: /readyz answers 503 and grpc.health.v1 answers NOT_SERVING, and the
//     process keeps running for delayFor so a load balancer has time to notice and route elsewhere. Closing the
//     listener first would make every request already in flight from the balancer fail with a connection error.
//  2. Close the listeners and let in-flight requests and RPCs run to completion within drainFor.
func gracefulShutdown(ctx context.Context, log *slog.Logger, s servers) error {
	<-ctx.Done()

	if s.draining != nil {
		s.draining.Store(true)
	}
	if s.health != nil {
		s.health.Shutdown() // every service NOT_SERVING, and stays so whatever is set afterwards
	}
	log.Info("shutdown signal received; readiness now answers 503 — waiting for the load balancer to notice",
		slog.Duration("delay", s.delayFor))
	time.Sleep(s.delayFor)

	log.Info("draining in-flight work", slog.Duration("timeout", s.drainFor))

	// This has to be a fresh context, because ctx has already been cancelled.
	// Using ctx would turn Shutdown into an immediate cut rather than a drain.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), s.drainFor)
	defer cancel()

	go func() {
		s.grpc.GracefulStop() // stop accepting new streams, wait for in-flight RPCs to finish
	}()

	if err := s.http.Shutdown(shutdownCtx); err != nil {
		// The drain window has run out; cutting is better than hanging indefinitely.
		s.grpc.Stop()
		_ = s.http.Close()
		return fmt.Errorf("failed to drain within the allotted time: %w", err)
	}
	s.grpc.Stop()
	return nil
}
