package security_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/nthw-dev/user-management-api/internal/adapter/outbound/security"
	"github.com/nthw-dev/user-management-api/internal/app"
	"github.com/nthw-dev/user-management-api/internal/domain/user"
)

// Use the lowest cost in tests, otherwise every case would burn hundreds of milliseconds.
const testCost = bcrypt.MinCost

func TestHashAndCompare(t *testing.T) {
	t.Parallel()

	h := security.NewBcryptHasher(testCost)
	const plain = "Str0ng-Passw0rd!"

	hash, err := h.Hash(plain)
	require.NoError(t, err)
	require.NotEqual(t, plain, hash)
	require.NotContains(t, hash, plain, "the raw password must not appear inside the hashed value")

	require.NoError(t, h.Compare(hash, plain))
	require.ErrorIs(t, h.Compare(hash, plain+"x"), app.ErrPasswordMismatch)
}

// bcrypt salts on its own, so the results for the same password must never be identical —
// otherwise one rainbow table could crack the whole database in a single pass.
func TestHash_IsSalted(t *testing.T) {
	t.Parallel()

	h := security.NewBcryptHasher(testCost)

	first, err := h.Hash("same-password")
	require.NoError(t, err)
	second, err := h.Hash("same-password")
	require.NoError(t, err)

	require.NotEqual(t, first, second)
	require.NoError(t, h.Compare(first, "same-password"))
	require.NoError(t, h.Compare(second, "same-password"))
}

func TestNewBcryptHasher_FallsBackOnBadCost(t *testing.T) {
	t.Parallel()

	// A cost outside bcrypt's supported range must fall back to the default rather than make hashing fail.
	hash, err := security.NewBcryptHasher(99).Hash("x")

	require.NoError(t, err)
	cost, err := bcrypt.Cost([]byte(hash))
	require.NoError(t, err)
	require.Equal(t, bcrypt.DefaultCost, cost)
}

// The 72-byte limit belongs to bcrypt, so it is translated into an ErrValidation in this adapter rather than in the domain.
func TestHash_TooLongIsValidationError(t *testing.T) {
	t.Parallel()

	h := security.NewBcryptHasher(testCost)
	_, err := h.Hash(strings.Repeat("a", 73))

	require.ErrorIs(t, err, user.ErrValidation{})
}
