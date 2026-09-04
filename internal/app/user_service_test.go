package app_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nthw-dev/user-management-api/internal/app"
	"github.com/nthw-dev/user-management-api/internal/app/apptest"
	"github.com/nthw-dev/user-management-api/internal/domain/user"
)

func ptr[T any](v T) *T { return &v }

func TestUserService_Create(t *testing.T) {
	t.Parallel()

	t.Run("stores only the hashed value, never the raw password", func(t *testing.T) {
		t.Parallel()
		s := newSuite(t)

		u, err := s.users.Create(context.Background(), app.CreateUserInput{
			Name: "Somchai", Email: "a@x.com", Password: apptest.SeededPassword,
		})

		require.NoError(t, err)
		require.NotEmpty(t, u.PasswordHash)
		require.NotContains(t, u.PasswordHash, apptest.SeededPassword)
		require.Equal(t, apptest.SeededAt, u.CreatedAt, "the time must come from the injected Clock")
	})

	t.Run("does not query for a duplicate email before inserting", func(t *testing.T) {
		t.Parallel()
		s := newSuite(t)

		_, err := s.users.Create(context.Background(), app.CreateUserInput{
			Name: "Somchai", Email: "a@x.com", Password: apptest.SeededPassword,
		})

		require.NoError(t, err)
		// A check before the insert always loses the race between two concurrent requests.
		// The truth about email uniqueness has to come from the unique index at the storage layer.
		require.Equal(t, []string{"Create"}, s.repo.recorded())
	})

	t.Run("a failing hash must not touch the database", func(t *testing.T) {
		t.Parallel()
		repo := newFakeRepo()
		svc := app.NewUserService(repo, newFakeRefreshRepo(), fakeHasher{hashErr: errors.New("boom")}, apptest.NewClock())

		_, err := svc.Create(context.Background(), app.CreateUserInput{
			Name: "Somchai", Email: "a@x.com", Password: apptest.SeededPassword,
		})

		require.Error(t, err)
		require.Empty(t, repo.recorded())
	})
}

// The rules every new user must pass, whether they arrive via /auth/register or POST /users.
func TestUserService_Create_Rules(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   app.CreateUserInput
		seed    []string
		wantErr error
	}{
		{
			name:  "signs up successfully when the input is valid",
			input: app.CreateUserInput{Name: "Somchai", Email: "a@x.com", Password: apptest.SeededPassword},
		},
		{
			name:    "a duplicate email must yield ErrEmailTaken",
			input:   app.CreateUserInput{Name: "Somchai", Email: "a@x.com", Password: apptest.SeededPassword},
			seed:    []string{"a@x.com"},
			wantErr: user.ErrEmailTaken,
		},
		{
			name:    "emails differing only in case count as duplicates",
			input:   app.CreateUserInput{Name: "Somchai", Email: "A@X.com", Password: apptest.SeededPassword},
			seed:    []string{"a@x.com"},
			wantErr: user.ErrEmailTaken,
		},
		{
			name:    "a too-short password must fail validation",
			input:   app.CreateUserInput{Name: "Somchai", Email: "b@x.com", Password: "123"},
			wantErr: user.ErrValidation{},
		},
		{
			name:    "an easily guessed password must not pass",
			input:   app.CreateUserInput{Name: "Somchai", Email: "b@x.com", Password: "password123"},
			wantErr: user.ErrValidation{},
		},
		{
			name:    "a malformed email must not pass",
			input:   app.CreateUserInput{Name: "Somchai", Email: "not-an-email", Password: apptest.SeededPassword},
			wantErr: user.ErrValidation{},
		},
		{
			name:    "an empty name must not pass",
			input:   app.CreateUserInput{Name: "  ", Email: "c@x.com", Password: apptest.SeededPassword},
			wantErr: user.ErrValidation{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := newSuite(t)
			for _, email := range tt.seed {
				s.seed(t, email)
			}

			got, err := s.users.Create(context.Background(), tt.input)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				require.Nil(t, got)
				return
			}

			require.NoError(t, err)
			require.NotEmpty(t, got.ID)
			require.Equal(t, "a@x.com", got.Email.String(), "the email must be normalized")
			require.NotContains(t, got.PasswordHash, tt.input.Password,
				"the raw password must not appear inside the hashed value")
		})
	}
}

func TestUserService_Get(t *testing.T) {
	t.Parallel()

	s := newSuite(t)
	seeded := s.seed(t, "a@x.com")

	t.Run("fetches an existing user", func(t *testing.T) {
		got, err := s.users.Get(context.Background(), seeded.ID)

		require.NoError(t, err)
		require.Equal(t, "a@x.com", got.Email.String())
	})

	t.Run("a miss must yield ErrNotFound", func(t *testing.T) {
		_, err := s.users.Get(context.Background(), "000000000000000000000099")

		require.ErrorIs(t, err, user.ErrNotFound)
	})

	t.Run("an empty id must yield ErrValidation", func(t *testing.T) {
		_, err := s.users.Get(context.Background(), "")

		require.ErrorIs(t, err, user.ErrValidation{})
	})
}

// The paging rules are applied here, once, so every transport — and any future caller — gets the same cap and the same default.
func TestUserService_List(t *testing.T) {
	t.Parallel()

	t.Run("a limit that was not sent becomes the default and is reported back", func(t *testing.T) {
		t.Parallel()
		s := newSuite(t)
		s.seed(t, "a@x.com")

		page, err := s.users.List(context.Background(), app.ListFilter{})

		require.NoError(t, err)
		require.Equal(t, app.DefaultListLimit, page.Limit)
		require.Len(t, page.Users, 1)
		require.False(t, page.HasMore())
	})

	t.Run("a limit that was sent is validated — zero included", func(t *testing.T) {
		t.Parallel()

		for _, limit := range []int{0, -1, app.MaxListLimit + 1} {
			t.Run(fmt.Sprint(limit), func(t *testing.T) {
				t.Parallel()
				s := newSuite(t)

				_, err := s.users.List(context.Background(), app.ListFilter{Limit: ptr(limit)})

				var invalid user.ErrValidation
				require.ErrorAs(t, err, &invalid)
				require.Equal(t, "limit", invalid.Field)
				require.Empty(t, s.repo.recorded(), "an unusable limit must never reach the repository")
			})
		}
	})

	t.Run("walks every page exactly once through the cursor", func(t *testing.T) {
		t.Parallel()
		s := newSuite(t)
		for i := 0; i < 7; i++ {
			s.seed(t, fmt.Sprintf("user%d@x.com", i))
		}

		seen := map[string]struct{}{}
		cursor := ""
		pages := 0
		for {
			page, err := s.users.List(context.Background(), app.ListFilter{Limit: ptr(3), Cursor: cursor})
			require.NoError(t, err)
			pages++

			require.Equal(t, 3, page.Limit)
			for _, u := range page.Users {
				_, dup := seen[u.ID]
				require.False(t, dup, "item %s appeared on two pages", u.ID)
				seen[u.ID] = struct{}{}
			}
			if !page.HasMore() {
				break
			}
			cursor = page.NextCursor
			require.Less(t, pages, 10, "should not loop forever")
		}

		require.Len(t, seen, 7)
		require.Equal(t, 3, pages, "7 items at 3 per page must give 3 pages")
	})
}

func TestUserService_Update(t *testing.T) {
	t.Parallel()

	newSeeded := func(t *testing.T) (*suite, string) {
		t.Helper()
		s := newSuite(t)
		id := s.seed(t, "a@x.com").ID
		s.seed(t, "taken@x.com")
		return s, id
	}

	t.Run("updates the name alone", func(t *testing.T) {
		t.Parallel()
		s, id := newSeeded(t)
		s.clock.Advance(time.Hour)

		got, err := s.users.Update(context.Background(), id, id, app.UpdateUserInput{Name: ptr("new name")})

		require.NoError(t, err)
		require.Equal(t, "new name", got.Name)
		require.Equal(t, "a@x.com", got.Email.String(), "a field that was not sent must be left untouched")
		require.Equal(t, apptest.SeededAt.Add(time.Hour), got.UpdatedAt)
	})

	t.Run("an updated email gets normalized", func(t *testing.T) {
		t.Parallel()
		s, id := newSeeded(t)

		got, err := s.users.Update(context.Background(), id, id, app.UpdateUserInput{Email: ptr("NEW@X.COM")})

		require.NoError(t, err)
		require.Equal(t, "new@x.com", got.Email.String())
	})

	t.Run("updating to an email already in use must yield ErrEmailTaken", func(t *testing.T) {
		t.Parallel()
		s, id := newSeeded(t)

		_, err := s.users.Update(context.Background(), id, id, app.UpdateUserInput{Email: ptr("taken@x.com")})

		require.ErrorIs(t, err, user.ErrEmailTaken)
	})

	t.Run("sending no fields at all must yield ErrValidation", func(t *testing.T) {
		t.Parallel()
		s, id := newSeeded(t)

		_, err := s.users.Update(context.Background(), id, id, app.UpdateUserInput{})

		require.ErrorIs(t, err, user.ErrValidation{})
	})

	t.Run("an empty name must yield ErrValidation and must not write to the database", func(t *testing.T) {
		t.Parallel()
		s, id := newSeeded(t)

		_, err := s.users.Update(context.Background(), id, id, app.UpdateUserInput{Name: ptr("  ")})

		require.ErrorIs(t, err, user.ErrValidation{})
		require.NotContains(t, s.repo.recorded(), "Update")
	})

	// Every verb answers an empty id the same way, before the repository is asked anything.
	t.Run("an empty id must yield ErrValidation, like Get and Delete", func(t *testing.T) {
		t.Parallel()
		s, _ := newSeeded(t)

		_, err := s.users.Update(context.Background(), "", "", app.UpdateUserInput{Name: ptr("x")})

		var invalid user.ErrValidation
		require.ErrorAs(t, err, &invalid)
		require.Equal(t, "id", invalid.Field)
		require.Empty(t, s.repo.recorded())
	})

	t.Run("another user's id must yield ErrForbidden, and the repository is never asked", func(t *testing.T) {
		t.Parallel()
		s, id := newSeeded(t)
		other := s.seed(t, "other@x.com").ID

		_, err := s.users.Update(context.Background(), id, other, app.UpdateUserInput{Name: ptr("hijacked")})

		require.ErrorIs(t, err, user.ErrForbidden)
		require.Empty(t, s.repo.recorded(), "a foreign id must be refused before any read, so 403 cannot double as an existence check")
	})

	t.Run("a missing actor is refused the same way", func(t *testing.T) {
		t.Parallel()
		s, id := newSeeded(t)

		_, err := s.users.Update(context.Background(), "", id, app.UpdateUserInput{Name: ptr("x")})

		require.ErrorIs(t, err, user.ErrForbidden)
	})

	t.Run("a bad name and a bad email are reported together", func(t *testing.T) {
		t.Parallel()
		s, id := newSeeded(t)

		_, err := s.users.Update(context.Background(), id, id, app.UpdateUserInput{Name: ptr("  "), Email: ptr("nope")})

		var all user.ValidationErrors
		require.ErrorAs(t, err, &all)
		require.Equal(t, []string{"name", "email"}, []string{all[0].Field, all[1].Field})
		require.NotContains(t, s.repo.recorded(), "Update")
	})

	t.Run("a missing user must yield ErrNotFound", func(t *testing.T) {
		t.Parallel()
		s, _ := newSeeded(t)

		_, err := s.users.Update(context.Background(), "000000000000000000000099", "000000000000000000000099",
			app.UpdateUserInput{Name: ptr("x")})

		require.ErrorIs(t, err, user.ErrNotFound)
	})
}

func TestUserService_DeleteAndCount(t *testing.T) {
	t.Parallel()

	s := newSuite(t)
	seeded := s.seed(t, "a@x.com")
	other := s.seed(t, "other@x.com")

	n, err := s.users.Count(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(2), n)

	// A live session, so the delete has something to clean up.
	_, err = s.auth.Login(context.Background(), app.LoginInput{Email: "a@x.com", Password: apptest.SeededPassword})
	require.NoError(t, err)
	require.Equal(t, 1, s.refresh.activeCount())

	// Someone else's row is refused before the repository hears of it.
	s.repo.calls = nil
	require.ErrorIs(t, s.users.Delete(context.Background(), seeded.ID, other.ID), user.ErrForbidden)
	require.Empty(t, s.repo.recorded())

	require.NoError(t, s.users.Delete(context.Background(), seeded.ID, seeded.ID))
	require.Zero(t, s.refresh.activeCount(), "a deleted user's refresh tokens must not outlive the row")

	// Deleting the same one twice yields ErrNotFound — telling the truth that the resource is gone.
	require.ErrorIs(t, s.users.Delete(context.Background(), seeded.ID, seeded.ID), user.ErrNotFound)

	n, err = s.users.Count(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(1), n)
}
