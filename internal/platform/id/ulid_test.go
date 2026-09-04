package id_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nthw-dev/user-management-api/internal/platform/id"
)

const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

func TestNewULID_Shape(t *testing.T) {
	t.Parallel()

	got := id.NewULID()

	require.Len(t, got, 26)
	for _, c := range got {
		require.True(t, strings.ContainsRune(alphabet, c), "character %q is outside the Crockford base32 set", c)
	}
}

func TestNewULID_Unique(t *testing.T) {
	t.Parallel()

	seen := make(map[string]struct{}, 10_000)
	for i := 0; i < 10_000; i++ {
		v := id.NewULID()
		_, dup := seen[v]
		require.False(t, dup, "duplicate value at round %d", i)
		seen[v] = struct{}{}
	}
}

// The first 10 characters are a timestamp, so a value issued later always sorts after one issued earlier,
// which is the reason for choosing ULID over UUIDv4.
func TestNewULID_SortsByTime(t *testing.T) {
	t.Parallel()

	first := id.NewULID()
	time.Sleep(2 * time.Millisecond)
	second := id.NewULID()

	require.Less(t, first[:10], second[:10])
}
