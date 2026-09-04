// Package clock is the outbound adapter for time — the implementation of app.Clock the real system runs on,
// so that tests can substitute a controllable one without waiting for time to pass.
package clock

import (
	"time"

	"github.com/nthw-dev/user-management-api/internal/app"
)

type System struct{}

var _ app.Clock = System{}

// Now truncates the resolution to milliseconds, because that is all BSON can store.
//
// Without truncating, the value returned at creation carries nanosecond remainders, but once read back from MongoDB
// it becomes a different value — and a caller comparing the two would find a difference with no explanation for it.
func (System) Now() time.Time { return time.Now().UTC().Truncate(time.Millisecond) }
