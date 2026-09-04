package user_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nthw-dev/user-management-api/internal/domain/user"
)

func TestValidatePassword(t *testing.T) {
	t.Parallel()

	require.NoError(t, user.ValidatePassword("Str0ng-Pass!"))
	require.ErrorIs(t, user.ValidatePassword("short"), user.ErrValidation{})
	require.ErrorIs(t, user.ValidatePassword("password123"), user.ErrValidation{}, "a common value must be rejected")
	require.ErrorIs(t, user.ValidatePassword("PASSWORD123"), user.ErrValidation{}, "the comparison must be case-insensitive")
}
