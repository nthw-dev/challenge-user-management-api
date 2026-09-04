package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/nthw-dev/user-management-api/internal/domain/user"
)

const (
	refreshTokenBytes = 32

	// decoyPassword is hashed once, at construction, at the same cost the system really uses.
	// Comparing decoyProbe against that hash makes "no such user" take as long as a real login,
	// so response time cannot reveal which emails have accounts.
	decoyPassword = "timing-equalizer-not-a-real-password"
	decoyProbe    = decoyPassword + "-x"
)

// AuthService handles logging in and rotating refresh tokens.
type AuthService struct {
	repo       UserRepository
	refresh    RefreshTokenRepository
	hasher     PasswordHasher
	issuer     TokenIssuer
	clock      Clock
	accessTTL  time.Duration
	refreshTTL time.Duration
	decoyHash  string
}

var _ AuthUseCase = (*AuthService)(nil)

type AuthConfig struct {
	AccessTTL  time.Duration
	RefreshTTL time.Duration
}

// NewAuthService fails only if the hasher cannot produce the timing decoy —
// better to refuse to start than to run with the timing guard silently disarmed.
func NewAuthService(
	repo UserRepository,
	refresh RefreshTokenRepository,
	hasher PasswordHasher,
	issuer TokenIssuer,
	clock Clock,
	cfg AuthConfig,
) (*AuthService, error) {
	decoy, err := hasher.Hash(decoyPassword)
	if err != nil {
		return nil, fmt.Errorf("hash the timing decoy: %w", err)
	}
	return &AuthService{
		repo: repo, refresh: refresh, hasher: hasher,
		issuer: issuer, clock: clock,
		accessTTL: cfg.AccessTTL, refreshTTL: cfg.RefreshTTL,
		decoyHash: decoy,
	}, nil
}

func (s *AuthService) Login(ctx context.Context, in LoginInput) (*Session, error) {
	email, err := user.NewEmail(in.Email)
	if err != nil {
		// A malformed email answers the same as a wrong password — we never say which part was wrong.
		s.equalizeTiming()
		return nil, user.ErrInvalidCredentials
	}

	u, err := s.repo.FindByEmail(ctx, email)
	if errors.Is(err, user.ErrNotFound) {
		s.equalizeTiming()
		return nil, user.ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}

	if err := s.hasher.Compare(u.PasswordHash, in.Password); err != nil {
		if errors.Is(err, ErrPasswordMismatch) {
			return nil, user.ErrInvalidCredentials
		}
		// A corrupt stored hash is an infrastructure fault, not a wrong password — it must surface, not hide behind a 401.
		return nil, fmt.Errorf("compare password: %w", err)
	}

	return s.issueSession(ctx, u, s.clock.Now())
}

// Refresh rotates the old token into a new one; the old one is invalidated immediately.
func (s *AuthService) Refresh(ctx context.Context, refreshToken string) (*Session, error) {
	now := s.clock.Now()

	stored, err := s.findLiveRefreshToken(ctx, refreshToken, now)
	if err != nil {
		return nil, err
	}

	u, err := s.repo.FindByID(ctx, stored.UserID)
	if errors.Is(err, user.ErrNotFound) {
		return nil, user.ErrUnauthorized
	}
	if err != nil {
		return nil, err
	}

	return s.rotate(ctx, u, stored, now)
}

// findLiveRefreshToken answers ErrUnauthorized for everything that is not a usable token — unknown, revoked, or expired —
// so the caller learns nothing about which it was.
func (s *AuthService) findLiveRefreshToken(ctx context.Context, raw string, now time.Time) (*RefreshToken, error) {
	if raw == "" {
		return nil, user.ErrUnauthorized
	}

	stored, err := s.refresh.FindByHash(ctx, hashRefreshToken(raw))
	if errors.Is(err, ErrRefreshTokenNotFound) {
		return nil, user.ErrUnauthorized
	}
	if err != nil {
		return nil, err
	}

	if stored.Revoked() {
		if err := s.revokeAllOnReuse(ctx, stored.UserID, now); err != nil {
			return nil, err
		}
		return nil, user.ErrUnauthorized
	}
	if !now.Before(stored.ExpiresAt) {
		return nil, user.ErrUnauthorized
	}
	return stored, nil
}

// revokeAllOnReuse is the response to an already-rotated token being presented again: a copy has leaked.
// Wiping every session for that user is safer than letting whoever holds the copy carry on.
func (s *AuthService) revokeAllOnReuse(ctx context.Context, userID string, now time.Time) error {
	return s.refresh.RevokeAllForUser(ctx, userID, now)
}

// rotate claims the old token first — Revoke is a compare-and-swap on "not yet revoked" — and only then issues the new
// session. Two requests presenting the same token at once therefore cannot both win: the second finds the token already
// claimed and is answered exactly like any other unusable token, without wiping the user's sessions (nothing leaked;
// a client simply raced itself). Reuse detection stays where it is, on a token found already revoked at lookup.
//
// The price is the other failure mode: if storing the new token fails after the claim, the caller must log in again.
// That beats the alternative, in which issuing first could leave a second live token behind whenever the revoke lost.
func (s *AuthService) rotate(ctx context.Context, u *user.User, old *RefreshToken, now time.Time) (*Session, error) {
	if err := s.refresh.Revoke(ctx, old.ID, now); err != nil {
		if errors.Is(err, ErrRefreshTokenNotFound) {
			return nil, user.ErrUnauthorized
		}
		return nil, err
	}
	return s.issueSession(ctx, u, now)
}

func (s *AuthService) issueSession(ctx context.Context, u *user.User, now time.Time) (*Session, error) {
	access, err := s.issuer.Issue(u.ID, s.accessTTL)
	if err != nil {
		return nil, fmt.Errorf("issue access token: %w", err)
	}

	raw, err := newRefreshToken()
	if err != nil {
		return nil, err
	}
	rt := RefreshToken{
		UserID:    u.ID,
		TokenHash: hashRefreshToken(raw),
		CreatedAt: now.UTC(),
		ExpiresAt: now.Add(s.refreshTTL).UTC(),
	}
	if err := s.refresh.Store(ctx, rt); err != nil {
		return nil, err
	}

	return &Session{
		AccessToken:  access,
		RefreshToken: raw,
		ExpiresIn:    s.accessTTL,
		User:         u,
	}, nil
}

// equalizeTiming makes the "user not found" path take roughly as long as the normal path.
func (s *AuthService) equalizeTiming() {
	_ = s.hasher.Compare(s.decoyHash, decoyProbe)
}

// A refresh token is pure random bytes rather than a JWT — which is what makes it genuinely revocable, since it must always be checked against the database.
func newRefreshToken() (string, error) {
	b := make([]byte, refreshTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate random refresh token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// Only the hash is stored, so that a leaked database is not a pile of immediately usable keys.
// SHA-256 rather than bcrypt, because the input is already 256 random bits — making the hash slow would add no security.
func hashRefreshToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
