// Package app is the application layer — home to the use cases and to every "port".
//
// The interfaces in this file are declared on the consumer side, not the implementer side.
// That detail is what makes this genuinely hexagonal rather than merely foldered.
package app

import (
	"context"
	"time"

	"github.com/nthw-dev/user-management-api/internal/domain/user"
)

// ---- outbound (driven) ports: what the core calls, and an outbound adapter implements ----

type UserRepository interface {
	Create(ctx context.Context, u *user.User) error
	FindByID(ctx context.Context, id string) (*user.User, error)
	FindByEmail(ctx context.Context, email user.Email) (*user.User, error)
	// List fills Page.Users and Page.NextCursor for a resolved query; the use case fills Page.Limit.
	List(ctx context.Context, q ListQuery) (Page, error)
	Update(ctx context.Context, id string, p UpdatePatch) (*user.User, error)
	Delete(ctx context.Context, id string) error
	Count(ctx context.Context) (int64, error)
}

// RefreshTokenRepository stores hashed values only, never a raw token.
// FindByHash and Revoke report a missing token as ErrRefreshTokenNotFound.
type RefreshTokenRepository interface {
	Store(ctx context.Context, rt RefreshToken) error
	FindByHash(ctx context.Context, hash string) (*RefreshToken, error)
	Revoke(ctx context.Context, id string, now time.Time) error
	RevokeAllForUser(ctx context.Context, userID string, now time.Time) error
}

type PasswordHasher interface {
	Hash(plain string) (string, error)
	// Compare returns ErrPasswordMismatch for a wrong password; any other error means the comparison itself failed.
	Compare(hash, plain string) error
}

type TokenIssuer interface {
	Issue(userID string, ttl time.Duration) (string, error)
}

// TokenVerifier returns just the user id — the only thing an inbound adapter needs from a token.
type TokenVerifier interface {
	Verify(token string) (userID string, err error)
}

type Clock interface{ Now() time.Time }

// ---- inbound (driving) ports: what an inbound adapter is allowed to call ----

// UserUseCase's reads and Create are open to any authenticated caller; the two mutations take the caller's own id as
// actorID and refuse any other row with user.ErrForbidden. Who is calling is an argument, never a context value —
// so a test can state it, and a mock can check it, without either knowing how a transport carries it.
type UserUseCase interface {
	Create(ctx context.Context, in CreateUserInput) (*user.User, error)
	Get(ctx context.Context, id string) (*user.User, error)
	// List resolves the filter (a sent limit is validated, an absent one gets the default) and returns the page.
	List(ctx context.Context, f ListFilter) (Page, error)
	Update(ctx context.Context, actorID, id string, in UpdateUserInput) (*user.User, error)
	// Delete removes the row and revokes every refresh token the user still holds.
	Delete(ctx context.Context, actorID, id string) error
	Count(ctx context.Context) (int64, error)
}

// AuthUseCase has no Register — signing up is UserUseCase.Create, exposed without requiring a token.
// The difference is access control, which is a transport concern rather than a data one.
type AuthUseCase interface {
	Login(ctx context.Context, in LoginInput) (*Session, error)
	Refresh(ctx context.Context, refreshToken string) (*Session, error)
}
