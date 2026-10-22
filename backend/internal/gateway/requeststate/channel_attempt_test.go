package requeststate

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestChannelRPMCountSeparatesGroupsAndRechecksGlobalCount repeats admission across
// three channels with the same payer. Global RPM increments once while each
// channel records its own successful counter for later limit revalidation.
func TestChannelRPMCountSeparatesGroupsAndRechecksGlobalCount(t *testing.T) {
	ctx := WithChannelRPMCounts(context.Background())
	increments := 0
	for _, group := range []int64{0, 1, 0, 2, 0, 3, 1} {
		value, err := ChannelRPMCount(ctx, 9, group, func() (int, error) { increments++; return 4, nil })
		require.NoError(t, err)
		require.Equal(t, 4, value)
	}
	require.Equal(t, 4, increments)
}

// TestChannelAttemptStopsReplayBeforeAsyncCompletion blocks replay after registering
// temporary ownership. Finishing a blocked attempt commits the claim, preventing
// usage submission from racing with another channel's settlement identity.
func TestChannelAttemptStopsReplayBeforeAsyncCompletion(t *testing.T) {
	ctx, state := WithChannelAttempt(context.Background())
	released := true
	state.RegisterClaim("session", func(_ context.Context, release bool) error { released = release; return nil })
	BlockChannelReplay(ctx)
	require.False(t, state.CanReplay())
	require.NoError(t, state.Finish(ctx, true))
	require.False(t, released)
}
