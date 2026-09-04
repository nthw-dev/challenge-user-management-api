package clock_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nthw-dev/user-management-api/internal/adapter/outbound/clock"
)

// The real clock answers in UTC at millisecond resolution — the most BSON can store — so a value written and read
// back from MongoDB compares equal to the one the use case produced.
func TestSystem_Now(t *testing.T) {
	t.Parallel()

	now := clock.System{}.Now()

	require.Equal(t, time.UTC, now.Location())
	require.Zero(t, now.Nanosecond()%int(time.Millisecond), "must be truncated to milliseconds")
	require.WithinDuration(t, time.Now(), now, time.Second)
}
