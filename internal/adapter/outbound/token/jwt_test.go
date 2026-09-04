package token_test

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"

	"github.com/nthw-dev/user-management-api/internal/adapter/outbound/token"
	"github.com/nthw-dev/user-management-api/internal/app/apptest"
)

const (
	issuer   = "user-service"
	audience = "user-service-api"
	subject  = apptest.SeededID
)

var secret = []byte("test-secret-that-is-definitely-longer-than-thirty-two-bytes")

func newClock() *apptest.Clock { return apptest.NewClock() }

func TestIssueAndVerify(t *testing.T) {
	t.Parallel()

	clk := newClock()
	svc := token.NewJWTService(secret, issuer, audience, clk)

	raw, err := svc.Issue(subject, 15*time.Minute)
	require.NoError(t, err)

	got, err := svc.Verify(raw)
	require.NoError(t, err)
	require.Equal(t, subject, got)
}

// A JWT payload is only base64, so anyone holding the token can read it — there must be no personal data inside.
func TestIssue_PayloadHasNoPersonalData(t *testing.T) {
	t.Parallel()

	svc := token.NewJWTService(secret, issuer, audience, newClock())

	raw, err := svc.Issue(subject, 15*time.Minute)
	require.NoError(t, err)

	parts := strings.Split(raw, ".")
	require.Len(t, parts, 3)

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err)

	body := string(payload)
	require.Contains(t, body, subject)
	require.Contains(t, body, `"typ":"access"`, "the type must be stated, otherwise a token issued for something else could call the API")
	require.Contains(t, body, `"jti"`)
	require.NotContains(t, body, "@", "there must be no email in the payload")
	require.NotContains(t, strings.ToLower(body), "name")
	require.NotContains(t, strings.ToLower(body), "password")
}

func TestVerify_Rejects(t *testing.T) {
	t.Parallel()

	clk := newClock()
	svc := token.NewJWTService(secret, issuer, audience, clk)

	// sign builds a token with every parameter under our control, so we can simulate the various kinds of bad token.
	sign := func(t *testing.T, method jwt.SigningMethod, key any, c jwt.MapClaims) string {
		t.Helper()
		s, err := jwt.NewWithClaims(method, c).SignedString(key)
		require.NoError(t, err)
		return s
	}

	base := func() jwt.MapClaims {
		now := clk.Now()
		return jwt.MapClaims{
			"iss": issuer,
			"aud": audience,
			"sub": subject,
			"typ": token.TypeAccess,
			"iat": now.Unix(),
			"nbf": now.Unix(),
			"exp": now.Add(15 * time.Minute).Unix(),
		}
	}

	tests := []struct {
		name  string
		token func(t *testing.T) string
	}{
		{
			name: "signature from a different secret",
			token: func(t *testing.T) string {
				return sign(t, jwt.SigningMethodHS256, []byte("a-decoy-secret-of-similar-length-but-not-the-real-one"), base())
			},
		},
		{
			// ★ The case that the WithValidMethods line shuts down
			name: "alg is none",
			token: func(t *testing.T) string {
				return sign(t, jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, base())
			},
		},
		{
			name: "algorithm swapped to HS512",
			token: func(t *testing.T) string {
				return sign(t, jwt.SigningMethodHS512, secret, base())
			},
		},
		{
			name: "issuer does not match",
			token: func(t *testing.T) string {
				c := base()
				c["iss"] = "someone-else"
				return sign(t, jwt.SigningMethodHS256, secret, c)
			},
		},
		{
			name: "audience does not match",
			token: func(t *testing.T) string {
				c := base()
				c["aud"] = "another-api"
				return sign(t, jwt.SigningMethodHS256, secret, c)
			},
		},
		{
			name: "no exp",
			token: func(t *testing.T) string {
				c := base()
				delete(c, "exp")
				return sign(t, jwt.SigningMethodHS256, secret, c)
			},
		},
		{
			name: "typ is not access",
			token: func(t *testing.T) string {
				c := base()
				c["typ"] = "refresh"
				return sign(t, jwt.SigningMethodHS256, secret, c)
			},
		},
		{
			name: "no sub",
			token: func(t *testing.T) string {
				c := base()
				delete(c, "sub")
				return sign(t, jwt.SigningMethodHS256, secret, c)
			},
		},
		{
			name:  "not a JWT at all",
			token: func(*testing.T) string { return "garbage-value" },
		},
		{
			name:  "empty value",
			token: func(*testing.T) string { return "" },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := svc.Verify(tt.token(t))

			require.ErrorIs(t, err, token.ErrInvalidToken)
		})
	}
}

func TestVerify_Expiry(t *testing.T) {
	t.Parallel()

	clk := newClock()
	svc := token.NewJWTService(secret, issuer, audience, clk)

	raw, err := svc.Issue(subject, 15*time.Minute)
	require.NoError(t, err)

	// Still inside the 30-second leeway, so it must remain valid.
	clk.Advance(15*time.Minute + 20*time.Second)
	_, err = svc.Verify(raw)
	require.NoError(t, err)

	clk.Advance(time.Minute)
	_, err = svc.Verify(raw)
	require.ErrorIs(t, err, token.ErrInvalidToken)
}
