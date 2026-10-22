package requeststate

import (
	"context"
	"errors"
	"maps"
	"sync"

	"github.com/google/uuid"
)

// channel_attempt.go records replay eligibility for one automatic routing attempt.
// Completion submission closes replay synchronously, before asynchronous usage work
// can race with another channel. Session stores register token-checked cleanup so
// failed attempts can release their own temporary ownership before another group
// starts. The request coordinator finishes each attempt after the handler returns.
//
// A separate shared provider filter excludes failed or cooling providers across
// groups while preserving each caller's own exclusion set. State belongs to one
// client request and is never stored in credential snapshots. Cleanup callbacks
// receive a bounded context from the coordinator, and any cleanup error stops
// channel replay because ownership can no longer be proven safe to transfer.

type (
	// channelAttemptKey identifies one attempt's ownership and usage replay barriers.
	channelAttemptKey struct{}
	// channelFilterKey identifies the current request's shared provider exclusion callback.
	channelFilterKey struct{}
	// channelRPMKey identifies successful counters retained across one request's channel attempts.
	channelRPMKey struct{}
)

// channelRPMCounts retains successful user and group counters across automatic attempts.
type channelRPMCounts struct {
	mu     sync.Mutex       // Serializes successful admission counter sampling.
	values map[[2]int64]int // Payer/group counters sampled for this client request.
}

// ChannelAttempt holds synchronized replay barriers and temporary ownership cleanup.
type ChannelAttempt struct {
	Token   string                                       // Random temporary ownership token.
	mu      sync.Mutex                                   // Protects replay state and cleanup registrations.
	blocked bool                                         // Usage or another side effect forbids replay.
	cleanup map[string]func(context.Context, bool) error // Token-checked ownership completions.
}

// WithChannelRPMCounts allocates one request's accounting memory before funding admission.
func WithChannelRPMCounts(ctx context.Context) context.Context {
	return context.WithValue(ctx, channelRPMKey{}, &channelRPMCounts{values: make(map[[2]int64]int)})
}

// ChannelRPMCount reuses a successful counter for the same payer and group.
// Global counters use group zero; failures remain retryable and changed lower
// limits are checked against the retained count by the admission caller.
func ChannelRPMCount(ctx context.Context, userID, groupID int64, increment func() (int, error)) (int, error) {
	state, _ := ctx.Value(channelRPMKey{}).(*channelRPMCounts)
	if state == nil {
		return increment()
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	key := [2]int64{userID, groupID}
	if count, ok := state.values[key]; ok {
		return count, nil
	}
	count, err := increment()
	if err == nil {
		state.values[key] = count
	}
	return count, err
}

// WithChannelAttempt creates one attempt's private replay and ownership state.
// Callers retain the returned state and finish it before selecting another channel.
func WithChannelAttempt(ctx context.Context) (context.Context, *ChannelAttempt) {
	state := &ChannelAttempt{Token: uuid.NewString(), cleanup: make(map[string]func(context.Context, bool) error)}
	return context.WithValue(ctx, channelAttemptKey{}, state), state
}

// ChannelAttemptFromContext reads the current automatic attempt or returns nil.
func ChannelAttemptFromContext(ctx context.Context) *ChannelAttempt {
	if ctx == nil {
		return nil
	}
	state, _ := ctx.Value(channelAttemptKey{}).(*ChannelAttempt)
	return state
}

// BlockChannelReplay closes replay before submitting usage or irreversible effects.
// Ordinary requests have no state and remain governed by their existing lifecycle.
func BlockChannelReplay(ctx context.Context) {
	if state := ChannelAttemptFromContext(ctx); state != nil {
		state.mu.Lock()
		state.blocked = true
		state.mu.Unlock()
	}
}

// CanReplay reports whether this attempt has retained its side-effect-free state.
func (s *ChannelAttempt) CanReplay() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.blocked
}

// OwnsClaim reports whether this attempt already registered the supplied ownership.
func (s *ChannelAttempt) OwnsClaim(identity string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.cleanup[identity]
	return ok
}

// RegisterClaim retains token-checked completion for one temporary session owner.
// Calling code registers after its atomic claim succeeds and serializes registration
// with attempt completion, which runs only after the protocol handler returns.
func (s *ChannelAttempt) RegisterClaim(identity string, finish func(context.Context, bool) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanup[identity] = finish
}

// Finish completes temporary ownership using the caller's bounded cleanup context.
// Replay releases only an unblocked attempt's own claims. Any cleanup failure closes
// replay and propagates so the coordinator returns the last actual response.
func (s *ChannelAttempt) Finish(ctx context.Context, replay bool) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var failures []error
	for _, finish := range s.cleanup {
		failures = append(failures, finish(ctx, replay && !s.blocked))
	}
	err := errors.Join(failures...)
	if err != nil {
		s.blocked = true
	}
	clear(s.cleanup)
	return err
}

// WithChannelProviderFilter shares current failure and cooldown decisions with selectors.
// The callback returns excluded provider IDs for the current channel's candidates;
// callers finish binding it before selection and retain it for the client request.
func WithChannelProviderFilter(ctx context.Context, filter func([]int64) map[int64]struct{}) context.Context {
	return context.WithValue(ctx, channelFilterKey{}, filter)
}

// ChannelProviderExclusions merges routing exclusions with the selector's own failures.
// Returned storage is independent of the input map. An absent automatic filter
// leaves ordinary selection's input unchanged and performs no candidate reads.
func ChannelProviderExclusions(ctx context.Context, ids []int64, excluded map[int64]struct{}) map[int64]struct{} {
	filter, _ := ctx.Value(channelFilterKey{}).(func([]int64) map[int64]struct{})
	if filter == nil {
		return excluded
	}
	result := maps.Clone(excluded)
	if result == nil {
		result = make(map[int64]struct{})
	}
	maps.Copy(result, filter(ids))
	return result
}
