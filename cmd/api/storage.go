package main

import (
	"context"
	"log/slog"
	"time"

	mongostore "github.com/nthw-dev/user-management-api/internal/adapter/outbound/mongo"
	"github.com/nthw-dev/user-management-api/internal/platform/config"
)

// disconnectTimeout bounds the very last thing the process does — closing the MongoDB connection after everything has drained.
const disconnectTimeout = 5 * time.Second

// storage is every outbound adapter backed by MongoDB, plus the readiness probe the HTTP side exposes.
type storage struct {
	users   *mongostore.UserRepo
	refresh *mongostore.RefreshTokenRepo
	ready   func(ctx context.Context) error
}

// openStorage connects, builds the repositories, and makes sure their indexes exist — all before the port accepts requests,
// because the unique index is the source of truth for email uniqueness. The returned func closes the connection.
func openStorage(ctx context.Context, cfg config.Mongo, log *slog.Logger) (*storage, func(), error) {
	client, err := mongostore.Connect(ctx, mongostore.Options{
		URI:             cfg.URI,
		ConnTimeout:     cfg.ConnTimeout,
		MaxConnIdleTime: cfg.MaxConnIdleTime,
		MaxPoolSize:     cfg.MaxOpenConns,
		MinPoolSize:     cfg.MaxIdleConns,
	})
	if err != nil {
		return nil, nil, err
	}
	closeFn := func() {
		disconnectCtx, cancel := context.WithTimeout(context.Background(), disconnectTimeout)
		defer cancel()
		if err := client.Disconnect(disconnectCtx); err != nil {
			log.Warn("failed to close the MongoDB connection", slog.Any("err", err))
		}
	}

	db := client.Database(cfg.Database)
	st := &storage{
		users:   mongostore.NewUserRepo(db.Collection(cfg.Collection)),
		refresh: mongostore.NewRefreshTokenRepo(db.Collection(cfg.RefreshCollection)),
		ready:   mongostore.Ready(client),
	}
	if err := st.users.EnsureIndexes(ctx); err != nil {
		closeFn()
		return nil, nil, err
	}
	if err := st.refresh.EnsureIndexes(ctx); err != nil {
		closeFn()
		return nil, nil, err
	}
	return st, closeFn, nil
}
