//go:build integration

package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nthw-dev/user-management-api/internal/app"
	"github.com/nthw-dev/user-management-api/internal/app/apptest"
	"github.com/nthw-dev/user-management-api/internal/domain/user"
)

// Proves the unique index really works, and that the driver's error has been translated into the language of the domain.
func TestUserRepo_UniqueEmail(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := newRepos(t).users

	require.NoError(t, repo.Create(ctx, mustUser(t, "First Person", "dup@example.com")))

	err := repo.Create(ctx, mustUser(t, "Second Person", "dup@example.com"))

	require.ErrorIs(t, err, user.ErrEmailTaken)
}

func TestUserRepo_CRUD(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := newRepos(t).users

	u := mustUser(t, "Natthawat N.", "natthawat@example.com")
	require.NoError(t, repo.Create(ctx, u))
	require.Len(t, u.ID, 24, "an ObjectId must be converted into 24 hex characters")

	got, err := repo.FindByID(ctx, u.ID)
	require.NoError(t, err)
	require.Equal(t, "natthawat@example.com", got.Email.String())
	// The projection must drop the password field on any read that does not need it.
	require.Empty(t, got.PasswordHash)

	// Login, though, has to get the hash back, otherwise the password cannot be compared.
	byEmail, err := repo.FindByEmail(ctx, "natthawat@example.com")
	require.NoError(t, err)
	require.NotEmpty(t, byEmail.PasswordHash)

	newName := "Natthawat Narin"
	updated, err := repo.Update(ctx, u.ID, app.UpdatePatch{
		Name:      &newName,
		UpdatedAt: apptest.SeededAt.Add(time.Hour),
	})
	require.NoError(t, err)
	require.Equal(t, newName, updated.Name)
	require.Equal(t, "natthawat@example.com", updated.Email.String(), "a field that was not sent must be left untouched")
	require.Equal(t, apptest.SeededAt.Add(time.Hour).UTC(), updated.UpdatedAt)

	n, err := repo.Count(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), n)

	require.NoError(t, repo.Delete(ctx, u.ID))
	require.ErrorIs(t, repo.Delete(ctx, u.ID), user.ErrNotFound)
	_, err = repo.FindByID(ctx, u.ID)
	require.ErrorIs(t, err, user.ErrNotFound)
}

// Updating an email into a collision with someone else's must be rejected by the unique index, just as at creation.
func TestUserRepo_UpdateToTakenEmail(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := newRepos(t).users

	first := mustUser(t, "First Person", "first@example.com")
	require.NoError(t, repo.Create(ctx, first))
	second := mustUser(t, "Second Person", "second@example.com")
	require.NoError(t, repo.Create(ctx, second))

	taken := user.Email("first@example.com")
	_, err := repo.Update(ctx, second.ID, app.UpdatePatch{Email: &taken, UpdatedAt: apptest.SeededAt})

	require.ErrorIs(t, err, user.ErrEmailTaken)
}

// Every page must be equally fast, because it jumps straight into the index, and no item may be duplicated or missed.
func TestUserRepo_KeysetPagination(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := newRepos(t).users

	const total = 7
	for i := 0; i < total; i++ {
		require.NoError(t, repo.Create(ctx, mustUser(t,
			fmt.Sprintf("User %d", i),
			fmt.Sprintf("user%d@example.com", i),
		)))
	}

	seen := map[string]struct{}{}
	cursor := ""
	pages := 0

	for {
		page, err := repo.List(ctx, app.ListQuery{Limit: 3, Cursor: cursor})
		require.NoError(t, err)
		pages++

		for _, u := range page.Users {
			_, dup := seen[u.ID]
			require.False(t, dup, "item %s appeared on two pages", u.ID)
			seen[u.ID] = struct{}{}
			require.Empty(t, u.PasswordHash)
		}

		if !page.HasMore() {
			break
		}
		cursor = page.NextCursor
		require.Less(t, pages, 10, "should not loop forever")
	}

	require.Len(t, seen, total)
	require.Equal(t, 3, pages, "7 items at 3 per page must give 3 pages")
}

func TestUserRepo_ListSearch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := newRepos(t).users

	require.NoError(t, repo.Create(ctx, mustUser(t, "Somchai P.", "somchai@example.com")))
	require.NoError(t, repo.Create(ctx, mustUser(t, "Malee K.", "malee@example.org")))

	found, err := repo.List(ctx, app.ListQuery{Limit: 10, Query: "MALEE"})
	require.NoError(t, err)
	require.Len(t, found.Users, 1, "search must ignore case")
	require.Equal(t, "Malee K.", found.Users[0].Name)

	// A regex metacharacter must be escaped rather than interpreted.
	none, err := repo.List(ctx, app.ListQuery{Limit: 10, Query: ".*"})
	require.NoError(t, err)
	require.Empty(t, none.Users)
}

func TestUserRepo_InvalidIDAndCursor(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := newRepos(t).users

	_, err := repo.FindByID(ctx, "not-an-objectid")
	require.ErrorIs(t, err, user.ErrValidation{})

	_, err = repo.List(ctx, app.ListQuery{Limit: 10, Cursor: "not-an-objectid"})
	require.ErrorIs(t, err, user.ErrValidation{})
}
