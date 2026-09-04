//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"

	"github.com/nthw-dev/user-management-api/internal/app"
	"github.com/nthw-dev/user-management-api/internal/app/apptest"
)

func sampleToken(userID, hash string) app.RefreshToken {
	return app.RefreshToken{
		UserID:    userID,
		TokenHash: hash,
		CreatedAt: apptest.SeededAt,
		ExpiresAt: apptest.SeededAt.Add(168 * time.Hour),
	}
}

func TestRefreshTokenRepo_StoreAndFind(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := newRepos(t).refresh

	require.NoError(t, repo.Store(ctx, sampleToken(apptest.SeededID, "hash-1")))

	got, err := repo.FindByHash(ctx, "hash-1")
	require.NoError(t, err)
	require.Len(t, got.ID, 24, "the stored id comes back so the token can be revoked by id later")
	require.Equal(t, apptest.SeededID, got.UserID)
	require.Equal(t, apptest.SeededAt, got.CreatedAt)
	require.False(t, got.Revoked())

	_, err = repo.FindByHash(ctx, "never-stored")
	require.ErrorIs(t, err, app.ErrRefreshTokenNotFound)
}

// The unique index on the hash is what guarantees one token maps to one row.
func TestRefreshTokenRepo_DuplicateHash(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := newRepos(t).refresh

	require.NoError(t, repo.Store(ctx, sampleToken(apptest.SeededID, "same-hash")))

	err := repo.Store(ctx, sampleToken("another-user", "same-hash"))

	require.ErrorContains(t, err, "hash collision")
}

func TestRefreshTokenRepo_Revoke(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := newRepos(t).refresh

	require.NoError(t, repo.Store(ctx, sampleToken(apptest.SeededID, "hash-1")))
	stored, err := repo.FindByHash(ctx, "hash-1")
	require.NoError(t, err)

	now := apptest.SeededAt.Add(time.Minute)
	require.NoError(t, repo.Revoke(ctx, stored.ID, now))

	got, err := repo.FindByHash(ctx, "hash-1")
	require.NoError(t, err)
	require.True(t, got.Revoked())
	require.Equal(t, now, got.RevokedAt.UTC())

	// A second revoke finds no live token — and says so, rather than pretending it did something.
	require.ErrorIs(t, repo.Revoke(ctx, stored.ID, now), app.ErrRefreshTokenNotFound)
	require.ErrorIs(t, repo.Revoke(ctx, "not-an-objectid", now), app.ErrRefreshTokenNotFound)
}

func TestRefreshTokenRepo_RevokeAllForUser(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := newRepos(t).refresh

	require.NoError(t, repo.Store(ctx, sampleToken(apptest.SeededID, "hash-a")))
	require.NoError(t, repo.Store(ctx, sampleToken(apptest.SeededID, "hash-b")))
	require.NoError(t, repo.Store(ctx, sampleToken("someone-else", "hash-c")))

	require.NoError(t, repo.RevokeAllForUser(ctx, apptest.SeededID, apptest.SeededAt))

	for _, hash := range []string{"hash-a", "hash-b"} {
		got, err := repo.FindByHash(ctx, hash)
		require.NoError(t, err)
		require.True(t, got.Revoked(), "%s belongs to the user and must be revoked", hash)
	}
	other, err := repo.FindByHash(ctx, "hash-c")
	require.NoError(t, err)
	require.False(t, other.Revoked(), "another user's session must be left alone")

	// Nothing left to revoke is not an error.
	require.NoError(t, repo.RevokeAllForUser(ctx, apptest.SeededID, apptest.SeededAt))
}

// The TTL index is what keeps the collection from growing without bound; it has to really exist, with the expected options.
func TestRefreshTokenRepo_Indexes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newRepos(t)

	names := map[string]bson.M{}
	cur, err := f.db.Collection("refresh_tokens").Indexes().List(ctx)
	require.NoError(t, err)
	for cur.Next(ctx) {
		var idx bson.M
		require.NoError(t, cur.Decode(&idx))
		names[idx["name"].(string)] = idx
	}

	require.Contains(t, names, "uniq_token_hash")
	require.Equal(t, true, names["uniq_token_hash"]["unique"])
	require.Contains(t, names, "user_id")
	require.Contains(t, names, "ttl_expires_at")
	require.EqualValues(t, 0, names["ttl_expires_at"]["expireAfterSeconds"])
}
