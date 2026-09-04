// Package mongostore is the outbound adapter that talks to MongoDB through the official driver.
//
// This is the only place in the system that knows about bson, ObjectID and the driver's error codes.
// Everything leaving this package has already been translated into the language of the domain or the application ports.
package mongostore

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
)

const (
	// appName is what this process calls itself in the server's logs and currentOp output.
	appName = "user-service"
	// serverSelectionTimeout bounds how long the driver hunts for a usable server before an operation fails.
	serverSelectionTimeout = 5 * time.Second
	// pingTimeout bounds the boot-time reachability check.
	pingTimeout = 5 * time.Second
	// opTimeout wraps every command, so no query can hang long enough to eat the whole connection pool.
	opTimeout = 5 * time.Second
)

// Options is the adapter's own view of what it needs to connect — primitives only, so this package
// does not depend on how the composition root happens to load configuration.
type Options struct {
	URI             string
	ConnTimeout     time.Duration
	MaxConnIdleTime time.Duration // zero means idle connections are never closed, per the driver's semantics
	MaxPoolSize     uint64
	MinPoolSize     uint64 // kept warm, to cut the latency of the first request
}

func Connect(ctx context.Context, o Options) (*mongo.Client, error) {
	opts := options.Client().
		ApplyURI(o.URI).
		SetAppName(appName).
		SetConnectTimeout(o.ConnTimeout).
		SetMaxPoolSize(o.MaxPoolSize).
		SetMinPoolSize(o.MinPoolSize).
		SetMaxConnIdleTime(o.MaxConnIdleTime).
		SetServerSelectionTimeout(serverSelectionTimeout).
		SetRetryWrites(true).
		// Slightly slower is an acceptable trade for not losing data when the primary goes down.
		SetWriteConcern(writeconcern.Majority())

	client, err := mongo.Connect(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("mongo connect: %w", err)
	}

	// Better to die at boot than to die once there are real users.
	pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	if err := Ready(client)(pingCtx); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, err
	}
	return client, nil
}

// Ready returns a function for the readiness probe — main need not know about the driver's readpref.
func Ready(client *mongo.Client) func(context.Context) error {
	return func(ctx context.Context) error {
		if err := client.Ping(ctx, readpref.Primary()); err != nil {
			return fmt.Errorf("mongo ping: %w", err)
		}
		return nil
	}
}
