package httpapi

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// channel_routing.go coordinates ordered automatic routing around credential
// admission and a single protocol handler. Failed upstream responses remain
// buffered until another eligible channel succeeds or the last real error is
// returned. Each attempt reauthenticates the credential and reruns funding checks;
// outer logging and request identity remain owned by the original Gin chain.
//
// The coordinator shares failed-provider exclusions, cooldown observations and
// RPM identity across attempts. Completion submission and committed business
// output close replay synchronously. Remote continuation, background generation,
// image generation and server tools also close replay because their upstream
// state or side effects cannot move safely to another channel. Temporary session
// ownership is finalized before another attempt starts, and cleanup failure stops
// the sequence. Every group is attempted at most once per client request.

const (
	// channelRoutingContextKey connects automatic model selection to its request
	// coordinator; fixed single-group credentials leave the coordinator inactive.
	channelRoutingContextKey = "channel_routing_execution"
)

// ChannelCooldownCache stores failures by Key, provider, model and inbound endpoint.
type ChannelCooldownCache interface {
	// GetChannelCooldowns returns remaining provider windows for one credential scope.
	GetChannelCooldowns(context.Context, int64, []int64, string, string) (map[int64]time.Duration, error)
	// CooldownChannelProviders records fixed windows for confirmed upstream provider failures.
	CooldownChannelProviders(context.Context, int64, []int64, string, string, time.Duration) error
}

// channelRoutingExecution retains original request bytes and bounded attempt state.
type channelRoutingExecution struct {
	responseStore      session.OpenAIWSStateStore // Verified remote continuation ownership.
	requiredProviderID int64                      // Verified continuation provider; zero means unknown.

	base      *gin.Context                 // Original outer request values.
	request   *http.Request                // Original request before Key model rewriting.
	body      []byte                       // Complete original client bytes.
	parent    gin.ResponseWriter           // Final client response writer.
	writer    *channelRoutingAttemptWriter // Current bounded attempt output.
	attempt   *requeststate.ChannelAttempt // Current usage and ownership barriers.
	visited   map[int64]bool               // Groups already exhausted by this request.
	keyID     int64                        // Original authenticated credential identity.
	groupID   int64                        // Current selected routing group.
	model     string                       // Original model used for cooldown isolation.
	endpoint  string                       // Registered inbound cooldown scope.
	duration  time.Duration                // Administrator cooldown duration.
	blocked   string                       // Request-side replay exclusion reason.
	active    bool                         // Automatic model routing has started.
	exhausted bool                         // Candidate selection found no later group.
	cache     ChannelCooldownCache         // Optional shared failure store.
	mu        sync.Mutex                   // Protects provider observations during selector callbacks.
	failed    map[int64]struct{}           // Providers failed anywhere in this request.
	loaded    map[int64]bool               // Providers with a sampled cooldown observation.
	cooling   map[int64]time.Time          // Sampled provider cooldown deadlines.
	release   func()                       // One Key request lease retained across attempts.
}

// WithAutomaticChannelRouting enables ordered replay around the supplied authenticator.
// Both authenticators invoke their downstream chain on the initial request; copied
// retry contexts invoke only the registered terminal handler after fresh admission.
// Ordinary single-group requests keep the normal chain and response writer.
func WithAutomaticChannelRouting(authenticate gin.HandlerFunc, cache ChannelCooldownCache, stores ...session.OpenAIWSStateStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		state := &channelRoutingExecution{base: c.Copy(), parent: c.Writer, visited: make(map[int64]bool), failed: make(map[int64]struct{}), loaded: make(map[int64]bool), cooling: make(map[int64]time.Time), cache: cache}
		if len(stores) > 0 {
			state.responseStore = stores[0]
		}
		c.Set(channelRoutingContextKey, state)
		terminal := c.Handler()
		defer func() {
			if state.active {
				c.Writer = state.parent
			}
			if state.release != nil {
				state.release()
			}
		}()
		authenticate(c)
		if !state.active || state.writer == nil {
			return
		}
		current := c
		var history []*ops.OpsUpstreamErrorEvent
		for {
			writer := state.writer
			_, streamFailed := GetOpsStreamError(current)
			failed := (writer.failedResponse() || streamFailed) && (channelRoutingUpstreamRetryable(current) || isOpsRoutingCapacityLimited(current))
			state.observeFailures(current, failed)
			retry := failed && state.blocked == "" && state.attempt.CanReplay() && !writer.committed && current.Request.Context().Err() == nil && len(state.visited) < apikey.MaxCompositeAPIKeyGroups-1 && terminal != nil
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(current.Request.Context()), 2*time.Second)
			cleanupErr := state.attempt.Finish(cleanupCtx, retry)
			cancel()
			if cleanupErr != nil {
				retry = false
			}
			if !retry {
				copyChannelOutcome(c, current, history)
				if err := writer.commit(); err != nil {
					slog.Debug("automatic route response write failed", "error", err)
				}
				return
			}
			state.visited[state.groupID] = true
			previous, previousWriter := current, writer
			previousGroup := state.groupID
			next := state.base.Copy()
			next.Request = state.request.Clone(state.request.Context())
			SetRequestBody(next.Request, bytes.Clone(state.body))
			state.writer = newChannelRoutingAttemptWriter(state.parent)
			next.Writer = state.writer
			state.writer.ctx = next
			next.Set(channelRoutingContextKey, state)
			state.exhausted = false
			state.groupID = 0
			authenticate(next)
			selected := GetExecutionAPIKey(next)
			if state.exhausted {
				copyChannelOutcome(c, previous, history)
				_ = previousWriter.commit()
				return
			}
			if selected != nil && (selected.ID != state.keyID || !selected.IsComposite) {
				copyChannelOutcome(c, previous, history)
				_ = previousWriter.commit()
				return
			}
			history = append(history, channelUpstreamEvents(previous)...)
			if selected == nil || !selected.IsComposite || selected.ID != state.keyID || state.groupID <= 0 || next.GetString("api_key_admission") != "accepted" {
				copyChannelOutcome(c, next, history)
				_ = state.writer.commit()
				return
			}
			slog.Info("automatic routing channel switched", "api_key_id", state.keyID, "from_group_id", previousGroup, "to_group_id", state.groupID)
			terminal(next)
			current = next
		}
	}
}

// prepareChannelRouting captures original input before model aliases alter its representation.
// Model-less and unsupported replay endpoints keep their normal output handling.
// Every automatic model call receives its own attempt barrier and provider filter.
func prepareChannelRouting(c *gin.Context, key *apikey.APIKey, keys *apikey.APIKeyService) error {
	state, _ := c.Get(channelRoutingContextKey)
	execution, _ := state.(*channelRoutingExecution)
	if execution == nil || !channelReplayEndpoint(c) {
		return nil
	}
	if !execution.active {
		body, err := ReadAndRestoreRequestBody(c.Request)
		if err != nil {
			return err
		}
		execution.keyID = key.ID
		execution.body = bytes.Clone(body)
		execution.endpoint = c.Request.URL.Path
		execution.blocked = channelReplayBlockReason(body)
		duration, err := keys.RoutingCooldown(c.Request.Context())
		if err != nil {
			return err
		}
		execution.active = true
		execution.duration = duration
		if err := execution.resolveContinuation(c.Request.Context(), key); err != nil {
			return err
		}
		ctx := requeststate.WithChannelProviderFilter(requeststate.WithChannelRPMCounts(c.Request.Context()), execution.exclusions)
		execution.request = c.Request.Clone(ctx)
	}
	ctx, attempt := requeststate.WithChannelAttempt(execution.request.Context())
	ctx = apikey.WithExcludedRoutingGroups(ctx, execution.visited)
	execution.attempt = attempt
	execution.writer = newChannelRoutingAttemptWriter(execution.parent)
	execution.writer.ctx = c
	c.Writer = execution.writer
	c.Request = c.Request.WithContext(ctx)
	return nil
}

// exclusions returns failed and cooling providers among one selector's candidates.
// Observations are sampled once per request. Cache errors retain only locally
// known failures and never fabricate a cooldown for an unobserved provider.
func (s *channelRoutingExecution) exclusions(ids []int64) map[int64]struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	missing := make([]int64, 0, len(ids))
	for _, id := range ids {
		if !s.loaded[id] {
			missing = append(missing, id)
			s.loaded[id] = true
		}
	}
	if s.blocked == "previous_response_id" || s.blocked == "opaque_history" {
		excluded := make(map[int64]struct{})
		if s.requiredProviderID > 0 {
			for id := range s.loaded {
				if id != s.requiredProviderID {
					excluded[id] = struct{}{}
				}
			}
		}
		return excluded
	}
	if s.cache != nil && s.duration > 0 && len(missing) > 0 {
		ctx, cancel := context.WithTimeout(s.request.Context(), 250*time.Millisecond)
		values, err := s.cache.GetChannelCooldowns(ctx, s.keyID, missing, s.model, s.endpoint)
		cancel()
		if err != nil {
			slog.Warn("automatic routing cooldown read failed", "api_key_id", s.keyID, "error", err)
		}
		for id, ttl := range values {
			if ttl > 0 {
				s.cooling[id] = time.Now().Add(ttl)
			}
		}
	}
	excluded := make(map[int64]struct{}, len(s.failed))
	for id := range s.failed {
		excluded[id] = struct{}{}
	}
	for id, until := range s.cooling {
		if until.After(time.Now()) {
			excluded[id] = struct{}{}
		}
	}
	return excluded
}

// observeFailures isolates confirmed provider faults from request-wide policy errors.
// A provider that finally succeeds is removed from this attempt's failure set.
// Cooldown persistence is bounded and survives client cancellation for diagnostics.
func (s *channelRoutingExecution) observeFailures(c *gin.Context, failed bool) {
	ids := make(map[int64]struct{})
	for _, event := range channelUpstreamEvents(c) {
		if event == nil || event.ProviderID <= 0 || event.Scope == "request" {
			continue
		}
		status := event.UpstreamStatusCode
		if status == 401 || status == 402 || status == 403 || status == 408 || status == 429 || status >= 500 || channelTransportFailure(event) {
			ids[event.ProviderID] = struct{}{}
		}
	}
	if !failed {
		if c.Writer.Status() >= 200 && c.Writer.Status() < 300 && !HasOpsClientBusinessLimited(c) {
			if id, ok := c.Request.Context().Value(telemetry.ProviderID).(int64); ok {
				delete(ids, id)
			}
		} else {
			return
		}
	}
	values := make([]int64, 0, len(ids))
	s.mu.Lock()
	for id := range ids {
		s.failed[id] = struct{}{}
		values = append(values, id)
	}
	s.mu.Unlock()
	if s.cache != nil && s.duration > 0 && len(values) > 0 {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 250*time.Millisecond)
		err := s.cache.CooldownChannelProviders(ctx, s.keyID, values, s.model, s.endpoint, s.duration)
		cancel()
		if err != nil {
			slog.Warn("automatic routing cooldown write failed", "api_key_id", s.keyID, "error", err)
		}
	}
}

// channelRoutingUpstreamRetryable requires an actual final upstream fault observation.
// Business limits and content policy terminate locally. Request cancellation cannot
// turn an older provider's HTTP failure into permission to replay another channel.
func channelRoutingUpstreamRetryable(c *gin.Context) bool {
	if HasOpsClientBusinessLimited(c) || GetOpsCyberPolicy(c) != nil || c.Request.Context().Err() != nil {
		return false
	}
	events := channelUpstreamEvents(c)
	for _, event := range slices.Backward(events) {
		if event == nil {
			continue
		}
		if event.Scope == "request" || event.Stage == "provider_auth" {
			return false
		}
		return event.UpstreamStatusCode >= 400 || channelTransportFailure(event)
	}
	return false
}

// channelTransportFailure recognizes observed network exchange failures after credentials resolve.
func channelTransportFailure(event *ops.OpsUpstreamErrorEvent) bool {
	return event != nil && event.UpstreamStatusCode == 0 && event.Stage != "provider_auth" && (event.Kind == "request_error" || event.Kind == "signature_retry_tools_request_error")
}

// channelUpstreamEvents reads the current attempt's immutable upstream diagnostic records.
func channelUpstreamEvents(c *gin.Context) []*ops.OpsUpstreamErrorEvent {
	value, _ := c.Get(OpsUpstreamErrorsKey)
	events, _ := value.([]*ops.OpsUpstreamErrorEvent)
	return events
}

// copyChannelOutcome publishes final attempt context while retaining earlier upstream failures.
// The outer Gin chain keeps ownership of logging and middleware completion. Copied
// Key values and request metadata identify the actual final route for settlement.
func copyChannelOutcome(dst, src *gin.Context, history []*ops.OpsUpstreamErrorEvent) {
	events := append(append([]*ops.OpsUpstreamErrorEvent(nil), history...), channelUpstreamEvents(src)...)
	if dst != src {
		dst.Keys = src.Copy().Keys
		dst.Request = src.Request
		dst.Params = append(gin.Params(nil), src.Params...)
		dst.Errors = src.Errors
	}
	if len(events) > 0 {
		dst.Set(OpsUpstreamErrorsKey, events)
	}
}

// channelReplayEndpoint limits replay to implemented synchronous model request families.
func channelReplayEndpoint(c *gin.Context) bool {
	if c.Request.Method != http.MethodPost {
		return false
	}
	path := c.Request.URL.Path
	if IsGeminiNativeModelEndpoint(path) {
		return strings.HasSuffix(path, ":generateContent") || strings.HasSuffix(path, ":streamGenerateContent") || strings.HasSuffix(path, ":countTokens")
	}
	for _, prefix := range []string{"/antigravity", "/backend-api/codex", "/v1"} {
		path = strings.TrimPrefix(path, prefix)
	}
	switch path {
	case "/messages", "/messages/count_tokens", "/responses", "/responses/compact", "/responses/input_tokens", "/chat/completions", "/embeddings", "/systemone":
		return true
	}
	return false
}

// channelReplayBlockReason rejects upstream-owned continuation and server-side effects.
// Ordinary encrypted reasoning stays in its original history. Opaque compressed
// history and previous-response handles require the original provider's ownership.
func channelReplayBlockReason(body []byte) string {
	if gjson.GetBytes(body, "previous_response_id").String() != "" {
		return "previous_response_id"
	}
	if gjson.GetBytes(body, "background").Bool() {
		return "background"
	}
	for _, item := range gjson.GetBytes(body, "input").Array() {
		kind := item.Get("type").String()
		if (kind == "compaction" || kind == "compaction_summary") && item.Get("encrypted_content").String() != "" {
			return "opaque_history"
		}
	}
	for _, tool := range gjson.GetBytes(body, "tools").Array() {
		kind := tool.Get("type").String()
		switch kind {
		case "image_generation", "mcp", "shell", "code_execution", "code_interpreter", "computer", "computer_use_preview", "web_search", "web_search_preview":
			return kind
		}
	}
	return ""
}

// resolveContinuation pins a known remote response to its authorized group and provider.
// Lookup shares one bounded budget across candidate groups. Missing ownership stays
// on the first model-matching group and disables cross-group replay; conflicting
// ownership or a missing provider binding rejects before any model is forwarded.
func (s *channelRoutingExecution) resolveContinuation(ctx context.Context, key *apikey.APIKey) error {
	responseID := gjson.GetBytes(s.body, "previous_response_id").String()
	if responseID == "" || s.responseStore == nil || key.User == nil {
		return nil
	}
	lookup, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	groupID := int64(0)
	for _, binding := range key.CompositeGroups {
		group := binding.Group
		if group == nil || !group.IsActive() || !key.User.CanBindGroup(group.ID, group.IsExclusive) {
			continue
		}
		allowed, err := session.ValidateHTTPResponseOwner(lookup, func() session.HTTPResponseOwnerReader { return s.responseStore }, group.ID, responseID, key.User.ID, key.ID)
		if err != nil {
			return err
		}
		if !allowed {
			continue
		}
		if groupID > 0 && groupID != group.ID {
			return apperror.Forbidden("CONTINUATION_OWNER_CONFLICT", "Conflicting continuation ownership")
		}
		providerID, err := s.responseStore.GetResponseProvider(lookup, group.ID, responseID)
		if err != nil {
			return err
		}
		if providerID <= 0 {
			return apperror.ServiceUnavailable("CONTINUATION_PROVIDER_UNAVAILABLE", "Continuation provider binding is unavailable")
		}
		groupID = group.ID
		s.requiredProviderID = providerID
	}
	if groupID > 0 {
		for _, binding := range key.CompositeGroups {
			if binding.GroupID != groupID {
				s.visited[binding.GroupID] = true
			}
		}
	}
	return nil
}

// retryAfter returns the earliest active provider cooldown sampled for this model.
// The coordinator uses this only after model selection has exhausted available
// channels, keeping a later empty lookup from replacing the last upstream error.
func (s *channelRoutingExecution) retryAfter() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	var earliest time.Duration
	for _, until := range s.cooling {
		remaining := time.Until(until)
		if remaining > 0 && (earliest == 0 || remaining < earliest) {
			earliest = remaining
		}
	}
	return earliest
}
