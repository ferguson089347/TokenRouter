package rediscache

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// channel_routing.go stores automatic-routing cooldowns and temporary session claims.
// Failure keys include credential, provider, model and endpoint so a failed model
// does not pause another Key or healthy provider in the same group. Redis uses
// SET NX for a fixed cooldown window; concurrent failures cannot extend it forever.
// Known observations also enter a bounded local cache for short Redis outages.
//
// Session ownership retains the existing numeric owner value, with a separate
// random-token key identifying an uncommitted automatic attempt. Cleanup removes
// the owner only while its token still matches. Ordinary ownership reads commit
// temporary claims so another request relying on that owner prevents its removal.
// All scripts compare and update their related keys atomically inside Redis.

const (
	// channelLocalCooldownLimit bounds remembered failures when shared Redis is unavailable.
	channelLocalCooldownLimit = 2048
)

// Channel claim scripts preserve numeric ownership and atomically guard temporary
// removal with the random token registered by the current automatic attempt.
var (
	claimChannelSession = redis.NewScript(`
if redis.call('EXISTS',KEYS[1])==1 then return 0 end
redis.call('SET',KEYS[1],ARGV[1],'PX',ARGV[3])
redis.call('SET',KEYS[2],ARGV[2],'PX',ARGV[3])
return 1`)
	finishChannelSession = redis.NewScript(`
if redis.call('GET',KEYS[2])~=ARGV[2] then return 0 end
if ARGV[3]=='1' and redis.call('GET',KEYS[1])==ARGV[1] then redis.call('DEL',KEYS[1]) end
redis.call('DEL',KEYS[2])
return 1`)
	readChannelSession = redis.NewScript(`
local owner=redis.call('GET',KEYS[1])
if owner then redis.call('DEL',KEYS[2]) end
return owner`)
	refreshChannelSession = redis.NewScript(`
redis.call('DEL',KEYS[2])
return redis.call('PEXPIRE',KEYS[1],ARGV[1])`)
)

// channelLocalCooldowns retains bounded provider deadlines under synchronized access.
type channelLocalCooldowns struct {
	mu     sync.Mutex           // Protects local failure deadlines.
	values map[string]time.Time // Known cooldown deadlines by hashed scope.
}

// channelCooldownKey hashes model and endpoint while retaining credential/provider identity.
func channelCooldownKey(keyID, providerID int64, model, endpoint string) string {
	hash := sha256.Sum256([]byte(fmt.Sprintf("%d:%s%s", len(model), model, endpoint)))
	return fmt.Sprintf("automatic_route:cooldown:%d:%d:%x", keyID, providerID, hash)
}

// channelClient derives a bounded client view sharing the application's connection pool.
// Initialization is synchronized and this view is never closed independently. Socket
// timeouts and disabled retries keep cooldown and claim calls within short budgets.
func (c *gatewayCache) channelClient() *redis.Client {
	c.channelOnce.Do(func() {
		if c.rdb != nil {
			c.channelRedis = c.rdb.WithTimeout(250 * time.Millisecond)
			c.channelRedis.Options().ContextTimeoutEnabled = true
			c.channelRedis.Options().MaxRetries = 0
		}
	})
	return c.channelRedis
}

// remember records an unexpired cooldown while retaining the first known window.
// Expired entries are reclaimed first; a full cache evicts its earliest deadline.
func (c *channelLocalCooldowns) remember(key string, ttl time.Duration, onlyAbsent bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	if ttl <= 0 {
		delete(c.values, key)
		return
	}
	if onlyAbsent && c.values[key].After(now) {
		return
	}
	if c.values == nil {
		c.values = make(map[string]time.Time)
	}
	if len(c.values) >= channelLocalCooldownLimit {
		oldest := ""
		var deadline time.Time
		for candidate, until := range c.values {
			if !until.After(now) {
				delete(c.values, candidate)
				continue
			}
			if deadline.IsZero() || until.Before(deadline) {
				oldest = candidate
				deadline = until
			}
		}
		if len(c.values) >= channelLocalCooldownLimit {
			delete(c.values, oldest)
		}
	}
	c.values[key] = now.Add(ttl)
}

// remaining returns the known cooldown and removes entries after their deadline.
func (c *channelLocalCooldowns) remaining(key string) time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	ttl := time.Until(c.values[key])
	if ttl <= 0 {
		delete(c.values, key)
		return 0
	}
	return ttl
}

// GetChannelCooldowns reads provider deadlines, retaining local observations on failure.
// Missing shared keys clear their local observations; unavailable Redis never invents
// cooldowns for providers that this process has not observed failing.
func (c *gatewayCache) GetChannelCooldowns(ctx context.Context, keyID int64, ids []int64, model, endpoint string) (map[int64]time.Duration, error) {
	result := make(map[int64]time.Duration)
	client := c.channelClient()
	if client == nil {
		for _, id := range ids {
			result[id] = c.channelCooldowns.remaining(channelCooldownKey(keyID, id, model, endpoint))
		}
		return result, errors.New("routing Redis unavailable")
	}
	pipe := client.Pipeline()
	commands := make(map[int64]*redis.DurationCmd, len(ids))
	for _, id := range ids {
		commands[id] = pipe.PTTL(ctx, channelCooldownKey(keyID, id, model, endpoint))
	}
	_, err := pipe.Exec(ctx)
	for id, command := range commands {
		key := channelCooldownKey(keyID, id, model, endpoint)
		ttl, readErr := command.Result()
		if readErr != nil {
			result[id] = c.channelCooldowns.remaining(key)
			continue
		}
		result[id] = max(0, ttl)
		c.channelCooldowns.remember(key, result[id], false)
	}
	return result, err
}

// CooldownChannelProviders records fixed failure windows using one Redis pipeline.
// Local evidence is retained before persistence so failed writes cannot erase a
// confirmed provider failure; TTL zero disables both persistence and local changes.
func (c *gatewayCache) CooldownChannelProviders(ctx context.Context, keyID int64, ids []int64, model, endpoint string, ttl time.Duration) error {
	if ttl <= 0 {
		return nil
	}
	for _, id := range ids {
		c.channelCooldowns.remember(channelCooldownKey(keyID, id, model, endpoint), ttl, true)
	}
	client := c.channelClient()
	if client == nil {
		return errors.New("routing Redis unavailable")
	}
	pipe := client.Pipeline()
	for _, id := range ids {
		pipe.SetNX(ctx, channelCooldownKey(keyID, id, model, endpoint), "1", ttl)
	}
	_, err := pipe.Exec(ctx)
	return err
}

// channelSessionKey shares the scheduler's established session-owner key namespace.
func channelSessionKey(userID int64, source, hash string) string {
	return fmt.Sprintf("sticky_session_owner:%d:%s:%s", userID, source, hash)
}

// ClaimChannelSessionOwner creates numeric ownership plus its private attempt token.
// Existing owners reject insertion and remain unchanged. Callers finish successful
// claims before another channel starts, or commit them when the request ends.
func (c *gatewayCache) ClaimChannelSessionOwner(ctx context.Context, userID int64, source, hash string, groupID int64, token string, ttl time.Duration) (bool, error) {
	key := channelSessionKey(userID, source, hash)
	value, err := claimChannelSession.Run(ctx, c.channelClient(), []string{key, key + ":automatic_claim"}, groupID, token, max(int64(1), ttl.Milliseconds())).Int()
	return value == 1, err
}

// FinishChannelSessionOwner releases ownership only while the attempt token matches.
// Reads by another request clear that token and commit ownership, preventing an
// older failed attempt from deleting a session that another request now uses.
func (c *gatewayCache) FinishChannelSessionOwner(ctx context.Context, userID int64, source, hash string, groupID int64, token string, release bool) error {
	key := channelSessionKey(userID, source, hash)
	flag := 0
	if release {
		flag = 1
	}
	return finishChannelSession.Run(ctx, c.channelClient(), []string{key, key + ":automatic_claim"}, groupID, token, flag).Err()
}

// GetSessionOwnerGroupID commits temporary claims before returning their numeric owner.
func (c *gatewayCache) GetSessionOwnerGroupID(ctx context.Context, userID int64, source, hash string) (int64, error) {
	key := channelSessionKey(userID, source, hash)
	return readChannelSession.Run(ctx, c.rdb, []string{key, key + ":automatic_claim"}).Int64()
}

// RefreshSessionOwnerTTL commits temporary claims before renewing the ownership deadline.
func (c *gatewayCache) RefreshSessionOwnerTTL(ctx context.Context, userID int64, source, hash string, ttl time.Duration) error {
	key := channelSessionKey(userID, source, hash)
	return refreshChannelSession.Run(ctx, c.rdb, []string{key, key + ":automatic_claim"}, ttl.Milliseconds()).Err()
}
