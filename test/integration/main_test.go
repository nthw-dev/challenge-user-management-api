//go:build integration

// Package integration tests the MongoDB adapter against a real MongoDB brought up with testcontainers.
//
// It is gated behind a build tag, because `go test ./...` has to stay fast and must not break on a machine with no Docker.
// CI runs this suite in a separate job, with `go test -tags=integration ./test/...`.
//
// One container serves the whole package; every test gets a database of its own and drops it when done,
// so tests stay independent without paying for a container each.
package integration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcmongo "github.com/testcontainers/testcontainers-go/modules/mongodb"
	"go.mongodb.org/mongo-driver/mongo"

	mongostore "github.com/nthw-dev/user-management-api/internal/adapter/outbound/mongo"
	"github.com/nthw-dev/user-management-api/internal/app/apptest"
	"github.com/nthw-dev/user-management-api/internal/domain/user"
)

var mongoURI string

func TestMain(m *testing.M) {
	ctx := context.Background()

	container, err := tcmongo.Run(ctx, "mongo:8.0") // the same line docker-compose.yml pins
	if err != nil {
		log.Fatalf("start mongo container: %v", err)
	}
	uri, err := container.ConnectionString(ctx)
	if err != nil {
		log.Fatalf("mongo connection string: %v", err)
	}
	mongoURI = uri

	code := m.Run()
	_ = testcontainers.TerminateContainer(container)
	os.Exit(code)
}

// fixture is one test's private database and the repositories built on it.
type fixture struct {
	users   *mongostore.UserRepo
	refresh *mongostore.RefreshTokenRepo
	db      *mongo.Database
}

// newRepos connects with the same pool settings production defaults to, on a database private to this test.
func newRepos(t *testing.T) fixture {
	t.Helper()
	ctx := context.Background()

	client, err := mongostore.Connect(ctx, mongostore.Options{
		URI:             mongoURI,
		ConnTimeout:     time.Minute,
		MaxConnIdleTime: 30 * time.Minute,
		MaxPoolSize:     10,
		MinPoolSize:     10,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })

	db := client.Database("it_" + randomSuffix(t))
	t.Cleanup(func() { _ = db.Drop(context.Background()) })

	f := fixture{
		users:   mongostore.NewUserRepo(db.Collection("users")),
		refresh: mongostore.NewRefreshTokenRepo(db.Collection("refresh_tokens")),
		db:      db,
	}
	require.NoError(t, f.users.EnsureIndexes(ctx))
	require.NoError(t, f.refresh.EnsureIndexes(ctx))
	return f
}

func randomSuffix(t *testing.T) string {
	t.Helper()
	b := make([]byte, 6)
	_, err := rand.Read(b)
	require.NoError(t, err)
	return hex.EncodeToString(b)
}

func mustUser(t *testing.T, name, email string) *user.User {
	t.Helper()
	u, err := user.New(name, email, apptest.SeededPassword,
		func(string) (string, error) { return "$2a$12$hash", nil }, apptest.SeededAt)
	require.NoError(t, err)
	return u
}
