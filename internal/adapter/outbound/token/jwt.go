// Package token is the outbound adapter for issuing and verifying JWTs.
package token

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/nthw-dev/user-management-api/internal/app"
	"github.com/nthw-dev/user-management-api/internal/platform/id"
)

// TypeAccess separates an access token from tokens of any other kind,
// preventing a token issued for some other purpose from being used to call the API directly.
const TypeAccess = "access"

var ErrInvalidToken = errors.New("invalid token")

type claims struct {
	Typ string `json:"typ"`
	jwt.RegisteredClaims
}

// JWTService implements both app.TokenIssuer and app.TokenVerifier.
type JWTService struct {
	secret   []byte
	issuer   string
	audience string
	clock    app.Clock
}

var (
	_ app.TokenIssuer   = (*JWTService)(nil)
	_ app.TokenVerifier = (*JWTService)(nil)
)

func NewJWTService(secret []byte, issuer, audience string, clock app.Clock) *JWTService {
	return &JWTService{secret: secret, issuer: issuer, audience: audience, clock: clock}
}

func (s *JWTService) Issue(userID string, ttl time.Duration) (string, error) {
	now := s.clock.Now()
	c := claims{
		Typ: TypeAccess,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    s.issuer,
			Audience:  jwt.ClaimStrings{s.audience},
			Subject:   userID,
			ID:        id.NewULID(),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	}

	// A JWT payload is just base64; it is not encrypted,
	// so there is no name, no email and no personal data in here at all — only sub.
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(s.secret)
	if err != nil {
		return "", fmt.Errorf("sign token: %w", err)
	}
	return signed, nil
}

// Verify returns the user id (sub) once the token clears every gate — signature, issuer, audience, expiry and type.
func (s *JWTService) Verify(raw string) (string, error) {
	var c claims
	tok, err := jwt.ParseWithClaims(raw, &c,
		func(*jwt.Token) (any, error) { return s.secret, nil },

		// Pin the algorithm hard; never trust the alg value carried in the token's header.
		// Without this line an attacker could send {"alg":"none"}.
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(s.issuer),
		jwt.WithAudience(s.audience),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(30*time.Second), // allows for a little clock skew between machines
		jwt.WithTimeFunc(s.clock.Now),
	)
	if err != nil || !tok.Valid || c.Typ != TypeAccess || c.Subject == "" {
		return "", ErrInvalidToken
	}
	return c.Subject, nil
}
