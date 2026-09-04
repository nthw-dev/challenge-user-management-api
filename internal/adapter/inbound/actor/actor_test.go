package actor_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nthw-dev/user-management-api/internal/adapter/inbound/actor"
)

func TestID_IsEmptyWhenNothingWasSet(t *testing.T) {
	t.Parallel()

	require.Empty(t, actor.ID(context.Background()))
	require.Empty(t, actor.ID(actor.Reserve(context.Background())), "a reserved but unfilled slot reads as no actor")
}

func TestSet_IsVisibleDownstream(t *testing.T) {
	t.Parallel()

	ctx := actor.Set(context.Background(), "user-1")

	require.Equal(t, "user-1", actor.ID(ctx))
}

// The point of the slot: a layer that ran BEFORE Set, holding the earlier context, still learns the id.
func TestReserve_LetsAnOuterLayerSeeAnInnerSet(t *testing.T) {
	t.Parallel()

	outer := actor.Reserve(context.Background())
	inner := actor.Set(outer, "user-1")

	require.Equal(t, "user-1", actor.ID(inner))
	require.Equal(t, "user-1", actor.ID(outer), "the outer context reads the slot the inner layer filled")
}

// Without a reservation, Set cannot leak upward — the outer context stays empty, as context values always do.
func TestSet_WithoutReserveStaysDownstreamOnly(t *testing.T) {
	t.Parallel()

	outer := context.Background()
	_ = actor.Set(outer, "user-1")

	require.Empty(t, actor.ID(outer))
}
