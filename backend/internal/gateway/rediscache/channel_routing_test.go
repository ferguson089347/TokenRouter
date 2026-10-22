package rediscache

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// TestChannelCooldownUsesIndependentFixedWindows fails one provider under one Key.
// Other models, endpoints and credentials remain usable, and repeated failures
// retain the initial deadline so a busy fault cannot postpone recovery indefinitely.
func TestChannelCooldownUsesIndependentFixedWindows(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	cache, ok := NewGatewayCache(client).(*gatewayCache)
	require.True(t, ok)
	ctx := context.Background()
	require.NoError(t, cache.CooldownChannelProviders(ctx, 1, []int64{7}, "model", "/responses", time.Minute))
	server.FastForward(20 * time.Second)
	require.NoError(t, cache.CooldownChannelProviders(ctx, 1, []int64{7}, "model", "/responses", time.Minute))
	remaining, err := cache.GetChannelCooldowns(ctx, 1, []int64{7, 8}, "model", "/responses")
	require.NoError(t, err)
	require.Equal(t, 40*time.Second, remaining[7])
	require.Zero(t, remaining[8])
	for _, scope := range []struct {
		key      int64  // Credential cooldown identity.
		model    string // Requested model scope.
		endpoint string // Inbound endpoint scope.
	}{{2, "model", "/responses"}, {1, "other-model", "/responses"}, {1, "model", "/messages"}} {
		values, err := cache.GetChannelCooldowns(ctx, scope.key, []int64{7}, scope.model, scope.endpoint)
		require.NoError(t, err)
		require.Zero(t, values[7])
	}
	server.FastForward(time.Minute)
	remaining, err = cache.GetChannelCooldowns(ctx, 1, []int64{7}, "model", "/responses")
	require.NoError(t, err)
	require.Zero(t, remaining[7])
}

// TestChannelSessionClaimCannotDeleteCommittedOrReplacedOwner creates temporary claims
// before replay cleanup. A concurrent reader commits ownership, and another token
// cannot delete the current owner even when it knows the same group identifier.
func TestChannelSessionClaimCannotDeleteCommittedOrReplacedOwner(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	cache, ok := NewGatewayCache(client).(*gatewayCache)
	require.True(t, ok)
	ctx := context.Background()
	written, err := cache.ClaimChannelSessionOwner(ctx, 1, "openai", "session", 7, "first-token", time.Minute)
	require.NoError(t, err)
	require.True(t, written)
	require.NoError(t, cache.FinishChannelSessionOwner(ctx, 1, "openai", "session", 7, "wrong-token", true))
	require.True(t, server.Exists(channelSessionKey(1, "openai", "session")))
	require.NoError(t, cache.FinishChannelSessionOwner(ctx, 1, "openai", "session", 7, "first-token", true))
	require.False(t, server.Exists(channelSessionKey(1, "openai", "session")))
	written, err = cache.ClaimChannelSessionOwner(ctx, 1, "openai", "session", 8, "second-token", time.Minute)
	require.NoError(t, err)
	require.True(t, written)
	owner, err := cache.GetSessionOwnerGroupID(ctx, 1, "openai", "session")
	require.NoError(t, err)
	require.EqualValues(t, 8, owner)
	require.NoError(t, cache.FinishChannelSessionOwner(ctx, 1, "openai", "session", 8, "second-token", true))
	require.True(t, server.Exists(channelSessionKey(1, "openai", "session")))
}
