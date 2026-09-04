package user_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nthw-dev/user-management-api/internal/domain/user"
)

var now = time.Date(2026, 9, 3, 9, 14, 22, 0, time.UTC)

// stubHash stands in for bcrypt in the domain tests — fast, and it lets us check that what is stored is the hash result
func stubHash(plain string) (string, error) { return "hashed:" + plain, nil }

func TestNew(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		inName    string
		inEmail   string
		inPass    string
		wantErr   bool
		wantName  string
		wantEmail string
	}{
		{
			name: "succeeds when the input is valid", inName: "Natthawat N.",
			inEmail: "natthawat@example.com", inPass: "Str0ng-Pass!",
			wantName: "Natthawat N.", wantEmail: "natthawat@example.com",
		},
		{
			name: "trims surrounding whitespace from the name", inName: "  Somchai  ",
			inEmail: "somchai@example.com", inPass: "Str0ng-Pass!",
			wantName: "Somchai", wantEmail: "somchai@example.com",
		},
		{
			name: "email is always lowercased", inName: "Malee",
			inEmail: "  Malee@Example.COM ", inPass: "Str0ng-Pass!",
			wantName: "Malee", wantEmail: "malee@example.com",
		},
		{
			name: "empty name must not pass", inName: "   ",
			inEmail: "a@example.com", inPass: "Str0ng-Pass!", wantErr: true,
		},
		{
			name: "a name longer than 100 characters must not pass", inName: strings.Repeat("ก", 101),
			inEmail: "a@example.com", inPass: "Str0ng-Pass!", wantErr: true,
		},
		{
			name: "a malformed email must not pass", inName: "Somchai",
			inEmail: "not-an-email", inPass: "Str0ng-Pass!", wantErr: true,
		},
		{
			name: "a too-short password must not pass", inName: "Somchai",
			inEmail: "a@example.com", inPass: "short", wantErr: true,
		},
		{
			name: "a common password must not pass", inName: "Somchai",
			inEmail: "a@example.com", inPass: "password123", wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			u, err := user.New(tt.inName, tt.inEmail, tt.inPass, stubHash, now)

			if tt.wantErr {
				require.Error(t, err)
				require.True(t, errors.Is(err, user.ErrValidation{}), "must be an ErrValidation")
				require.Nil(t, u)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.wantName, u.Name)
			require.Equal(t, tt.wantEmail, u.Email.String())
			require.Equal(t, "hashed:"+tt.inPass, u.PasswordHash, "must store the hash result, not the raw password")
			require.Equal(t, now, u.CreatedAt)
			require.Equal(t, now, u.UpdatedAt)
		})
	}
}

// Every failing field is reported together, in the order the invariants run, and nothing is hashed for input that fails.
func TestNew_ReportsEveryFailingFieldAtOnce(t *testing.T) {
	t.Parallel()

	hashed := false
	u, err := user.New("   ", "not-an-email", "short", func(p string) (string, error) { hashed = true; return p, nil }, now)

	require.Nil(t, u)
	require.False(t, hashed, "nothing may be hashed when validation fails")
	require.True(t, errors.Is(err, user.ErrValidation{}), "the whole must still read as a validation failure")

	var all user.ValidationErrors
	require.True(t, errors.As(err, &all))
	require.Len(t, all, 3)
	require.Equal(t, []string{"name", "email", "password"}, []string{all[0].Field, all[1].Field, all[2].Field})

	var one user.ErrValidation
	require.True(t, errors.As(err, &one), "errors.As on the single form must still find one")
	require.Equal(t, "name", one.Field)
}

// A single bad field is still a ValidationErrors — one shape for the caller — reading exactly as the one error does.
func TestNew_SingleFailureKeepsTheSameMessage(t *testing.T) {
	t.Parallel()

	_, err := user.New("Somchai", "a@example.com", "short", stubHash, now)

	var all user.ValidationErrors
	require.True(t, errors.As(err, &all))
	require.Len(t, all, 1)
	require.Equal(t, all[0].Error(), err.Error())
}

// A failing hash must propagate its own error upward, not be swallowed into an ErrValidation.
func TestNew_HashErrorPropagates(t *testing.T) {
	t.Parallel()

	boom := errors.New("hasher blew up")
	u, err := user.New("Somchai", "a@example.com", "Str0ng-Pass!", func(string) (string, error) { return "", boom }, now)

	require.ErrorIs(t, err, boom)
	require.False(t, errors.Is(err, user.ErrValidation{}))
	require.Nil(t, u)
}

// A name of 100 Thai characters must pass, because we count runes rather than bytes.
func TestNew_NameLengthCountsRunesNotBytes(t *testing.T) {
	t.Parallel()

	u, err := user.New(strings.Repeat("ก", 100), "a@example.com", "Str0ng-Pass!", stubHash, now)

	require.NoError(t, err)
	require.Equal(t, 300, len(u.Name), "100 Thai characters take up 300 bytes")
}

func TestRename(t *testing.T) {
	t.Parallel()

	later := now.Add(time.Hour)
	u, err := user.New("old name", "a@example.com", "Str0ng-Pass!", stubHash, now)
	require.NoError(t, err)

	require.NoError(t, u.Rename("  new name  ", later))
	require.Equal(t, "new name", u.Name)
	require.Equal(t, later, u.UpdatedAt, "a mutation must advance UpdatedAt")

	require.Error(t, u.Rename("", later))
	require.Equal(t, "new name", u.Name, "when validation fails the previous value must be left untouched")
}

func TestChangeEmail(t *testing.T) {
	t.Parallel()

	later := now.Add(time.Hour)
	u, err := user.New("Somchai", "old@example.com", "Str0ng-Pass!", stubHash, now)
	require.NoError(t, err)

	require.NoError(t, u.ChangeEmail("New@Example.com", later))
	require.Equal(t, "new@example.com", u.Email.String())

	require.Error(t, u.ChangeEmail("bad", later))
	require.Equal(t, "new@example.com", u.Email.String())
}
