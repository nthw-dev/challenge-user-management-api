package app_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nthw-dev/user-management-api/internal/app"
	"github.com/nthw-dev/user-management-api/internal/app/apptest"
	"github.com/nthw-dev/user-management-api/internal/domain/user"
)

// signUp creates the one account every auth test logs into.
func signUp(t *testing.T, s *suite) {
	t.Helper()
	_, err := s.users.Create(context.Background(), app.CreateUserInput{
		Name: "Somchai", Email: "a@x.com", Password: apptest.SeededPassword,
	})
	require.NoError(t, err)
}

func login(t *testing.T, s *suite) *app.Session {
	t.Helper()
	session, err := s.auth.Login(context.Background(), app.LoginInput{Email: "a@x.com", Password: apptest.SeededPassword})
	require.NoError(t, err)
	return session
}

func TestNewAuthService_RefusesWhenTheDecoyCannotBeHashed(t *testing.T) {
	t.Parallel()

	_, err := app.NewAuthService(newFakeRepo(), newFakeRefreshRepo(), fakeHasher{hashErr: errors.New("boom")},
		fakeIssuer{}, apptest.NewClock(), app.AuthConfig{})

	require.Error(t, err, "running without the timing guard would be a silent regression, so it must not start")
}

func TestAuthService_Login(t *testing.T) {
	t.Parallel()

	setup := func(t *testing.T) *suite {
		t.Helper()
		s := newSuite(t)
		signUp(t, s)
		return s
	}

	t.Run("a successful login returns both an access and a refresh token", func(t *testing.T) {
		t.Parallel()
		s := setup(t)

		session := login(t, s)

		require.True(t, strings.HasPrefix(session.AccessToken, "access-token:"))
		require.NotEmpty(t, session.RefreshToken)
		require.Equal(t, 15*time.Minute, session.ExpiresIn)
		require.Equal(t, "a@x.com", session.User.Email.String())
	})

	t.Run("an uppercase email can still log in", func(t *testing.T) {
		t.Parallel()
		s := setup(t)

		_, err := s.auth.Login(context.Background(), app.LoginInput{Email: "A@X.COM", Password: apptest.SeededPassword})

		require.NoError(t, err)
	})

	t.Run("a wrong password must yield ErrInvalidCredentials", func(t *testing.T) {
		t.Parallel()
		s := setup(t)

		_, err := s.auth.Login(context.Background(), app.LoginInput{Email: "a@x.com", Password: "wrong-password"})

		require.ErrorIs(t, err, user.ErrInvalidCredentials)
	})

	// Both cases must yield the same error, otherwise this endpoint becomes a tool for enumerating which emails have accounts.
	t.Run("an email not in the system yields that same error", func(t *testing.T) {
		t.Parallel()
		s := setup(t)

		_, err := s.auth.Login(context.Background(), app.LoginInput{Email: "nobody@x.com", Password: apptest.SeededPassword})

		require.ErrorIs(t, err, user.ErrInvalidCredentials)
	})

	t.Run("a malformed email yields that same error too, not ErrValidation", func(t *testing.T) {
		t.Parallel()
		s := setup(t)

		_, err := s.auth.Login(context.Background(), app.LoginInput{Email: "not-an-email", Password: apptest.SeededPassword})

		require.ErrorIs(t, err, user.ErrInvalidCredentials)
		require.NotErrorIs(t, err, user.ErrValidation{})
	})

	// A comparison that fails for any reason other than a mismatch is an infrastructure fault.
	// Reporting it as "wrong password" would lock that user out forever with nothing in the logs.
	t.Run("a comparison failure that is not a mismatch must surface as such", func(t *testing.T) {
		t.Parallel()
		s := newSuiteWith(t, fakeHasher{compareErr: errors.New("stored hash is corrupt")})
		signUp(t, s)

		_, err := s.auth.Login(context.Background(), app.LoginInput{Email: "a@x.com", Password: apptest.SeededPassword})

		require.Error(t, err)
		require.NotErrorIs(t, err, user.ErrInvalidCredentials)
		require.ErrorContains(t, err, "corrupt")
	})
}

func TestAuthService_Refresh(t *testing.T) {
	t.Parallel()

	loggedIn := func(t *testing.T) (*suite, *app.Session) {
		t.Helper()
		s := newSuite(t)
		signUp(t, s)
		return s, login(t, s)
	}

	t.Run("rotates the old token into a new one", func(t *testing.T) {
		t.Parallel()
		s, first := loggedIn(t)

		second, err := s.auth.Refresh(context.Background(), first.RefreshToken)

		require.NoError(t, err)
		require.NotEqual(t, first.RefreshToken, second.RefreshToken, "a new token must be issued every time")
		require.Equal(t, 1, s.refresh.activeCount(), "the old token must be invalidated")
	})

	t.Run("reusing an already-rotated token must wipe every session", func(t *testing.T) {
		t.Parallel()
		s, first := loggedIn(t)

		_, err := s.auth.Refresh(context.Background(), first.RefreshToken)
		require.NoError(t, err)

		// Reuse means a copy has leaked — cutting every token is safer than leaving them alive.
		_, err = s.auth.Refresh(context.Background(), first.RefreshToken)

		require.ErrorIs(t, err, user.ErrUnauthorized)
		require.Zero(t, s.refresh.activeCount())
	})

	t.Run("an expired token does not work", func(t *testing.T) {
		t.Parallel()
		s, session := loggedIn(t)

		s.clock.Advance(169 * time.Hour)

		_, err := s.auth.Refresh(context.Background(), session.RefreshToken)

		require.ErrorIs(t, err, user.ErrUnauthorized)
	})

	t.Run("a value that was never issued does not work", func(t *testing.T) {
		t.Parallel()
		s, _ := loggedIn(t)

		_, err := s.auth.Refresh(context.Background(), "garbage-value")

		require.ErrorIs(t, err, user.ErrUnauthorized)
	})

	t.Run("an empty value does not work", func(t *testing.T) {
		t.Parallel()
		s, _ := loggedIn(t)

		_, err := s.auth.Refresh(context.Background(), "")

		require.ErrorIs(t, err, user.ErrUnauthorized)
	})

	// The old token is claimed before the new one is stored, so a storage failure costs one login — never a live token
	// left behind next to a new one.
	t.Run("a failure to store the new token leaves nothing live — the caller logs in again", func(t *testing.T) {
		t.Parallel()
		s, first := loggedIn(t)

		s.refresh.failStores(errors.New("mongo: write concern timeout"))
		_, err := s.auth.Refresh(context.Background(), first.RefreshToken)
		require.Error(t, err)
		require.NotErrorIs(t, err, user.ErrUnauthorized, "an infrastructure error must not masquerade as a bad token")
		require.Zero(t, s.refresh.activeCount(), "the claim already went through")

		s.refresh.failStores(nil)
		_, err = s.auth.Refresh(context.Background(), first.RefreshToken)
		require.ErrorIs(t, err, user.ErrUnauthorized, "the old token was claimed, so it is spent")

		_, err = s.auth.Login(context.Background(), app.LoginInput{Email: "a@x.com", Password: apptest.SeededPassword})
		require.NoError(t, err, "the account itself is untouched")
	})

	// The whole point of claim-first: many requests presenting the same token at once yield exactly one new session.
	t.Run("twenty concurrent refreshes of one token: exactly one wins, the rest are 401, never a store error", func(t *testing.T) {
		t.Parallel()
		s, first := loggedIn(t)

		const n = 20
		start := make(chan struct{})
		results := make(chan error, n)
		for i := 0; i < n; i++ {
			go func() {
				<-start
				_, err := s.auth.Refresh(context.Background(), first.RefreshToken)
				results <- err
			}()
		}
		close(start)

		wins, refused := 0, 0
		for i := 0; i < n; i++ {
			switch err := <-results; {
			case err == nil:
				wins++
			case errors.Is(err, user.ErrUnauthorized):
				refused++
			default:
				t.Fatalf("a racing refresh must answer 401 or win, never %v", err)
			}
		}

		require.Equal(t, 1, wins)
		require.Equal(t, n-1, refused)
		require.Equal(t, 2, s.refresh.count(), "the original and the winner's — nobody else stored anything")
		require.LessOrEqual(t, s.refresh.activeCount(), 1, "at most the winner's token is live")
	})
}
