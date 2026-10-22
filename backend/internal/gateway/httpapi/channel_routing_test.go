package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// channelContinuationStore supplies verified remote response ownership for one group.
type channelContinuationStore struct{ session.OpenAIWSStateStore }

// channelRequestLeaseFixture captures request-capacity reservation and final release.
type channelRequestLeaseFixture struct {
	apikey.APIKeyCache     // Unused cache methods require fixture setup.
	reserved           int // Key request reservations observed.
	released           int // Active leases returned after handling.
}

// channelAuthenticationRepo supplies current persisted credentials for full admission tests.
type channelAuthenticationRepo struct {
	apikey.APIKeyRepository                // Unused persistence methods require fixture setup.
	key                     *apikey.APIKey // Current credential view returned to each authentication.
	reads                   int            // Authentication storage reload count.
}

// GetByKeyForAuth returns a request-owned copy and counts fresh authorization reads.
func (r *channelAuthenticationRepo) GetByKeyForAuth(context.Context, string) (*apikey.APIKey, error) {
	r.reads++
	return apikey.CopyAPIKey(r.key), nil
}

// UpdateLastUsed accepts diagnostic touches without changing this authorization fixture.
func (*channelAuthenticationRepo) UpdateLastUsed(context.Context, int64, time.Time) error { return nil }

// channelTestRouter binds the actual coordinator and model selector to test admission.
// The fake authenticator exposes only successful group selection; supplied terminal
// behavior controls observable upstream errors, output and completion barriers.
func channelTestRouter(t *testing.T, terminal gin.HandlerFunc, caches ...apikey.APIKeyCache) *gin.Engine {
	t.Helper()
	var cache apikey.APIKeyCache
	if len(caches) > 0 {
		cache = caches[0]
	}
	service := apikey.NewAPIKeyService(nil, nil, nil, nil, nil, cache, &apikey.Options{ResolveGroupModel: func(context.Context, int64, string, string, string) (bool, error) { return true, nil }})
	authenticate := func(c *gin.Context) {
		key := &apikey.APIKey{ID: 9, UserID: 1, IsComposite: true, User: &identity.User{ID: 1, GroupRestrictionsLoaded: true}, CompositeGroups: []apikey.APIKeyCompositeGroup{
			{GroupID: 1, Group: &routing.Group{ID: 1, Status: "active", AllowedProtocols: []ProtocolID{ProtocolOpenAIChatCompletions}}},
			{GroupID: 2, Group: &routing.Group{ID: 2, Status: "active", AllowedProtocols: []ProtocolID{ProtocolOpenAIChatCompletions}}},
			{GroupID: 3, Group: &routing.Group{ID: 3, Status: "active", AllowedProtocols: []ProtocolID{ProtocolOpenAIChatCompletions}}},
		}}
		if cache != nil {
			key.ConcurrencyLimit = 1
		}
		selected, err := ResolveCompositeAPIKeyRequest(c, service, key)
		if err != nil {
			AbortCompositeKeyError(c, err)
			return
		}
		c.Set("api_key", selected)
		release, ok := acquireKeyRequest(c, service, selected, APIKeyAuthorizationOptions{})
		if !ok {
			return
		}
		defer release()
		c.Set("api_key_admission", "accepted")
		c.Next()
	}
	router := gin.New()
	router.POST("/v1/chat/completions", WithAutomaticChannelRouting(authenticate, nil), terminal)
	return router
}

// TestAutomaticChannelsSwitchAfterUpstreamFailure fails two successive channels.
// Only the final success reaches the client, every group runs once and all earlier
// upstream failures remain available to the outer request observation callback.
func TestAutomaticChannelsSwitchAfterUpstreamFailure(t *testing.T) {
	var visited []int64
	router := channelTestRouter(t, func(c *gin.Context) {
		id := *GetExecutionAPIKey(c).GroupID
		visited = append(visited, id)
		if id < 3 {
			AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{ProviderID: 100 + id, UpstreamStatusCode: 503, Kind: "http_error"})
			c.Header("Retry-After", "60")
			c.JSON(503, gin.H{"error": "temporary upstream failure"})
			return
		}
		c.JSON(200, gin.H{"ok": true})
	})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"vendor/model"}`)))
	require.Equal(t, []int64{1, 2, 3}, visited)
	require.Equal(t, 200, recorder.Code)
	require.JSONEq(t, `{"ok":true}`, recorder.Body.String())
	require.Empty(t, recorder.Header().Get("Retry-After"))
}

// TestAutomaticChannelsReturnLastActualError exhausts all configured channels.
// The last upstream status and response survive selection exhaustion instead of
// being replaced by the selector's no-remaining-model error.
func TestAutomaticChannelsReturnLastActualError(t *testing.T) {
	router := channelTestRouter(t, func(c *gin.Context) {
		id := *GetExecutionAPIKey(c).GroupID
		status := 500 + int(id)
		AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{ProviderID: id, UpstreamStatusCode: status})
		c.JSON(status, gin.H{"channel": id})
	})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"same-model"}`)))
	require.Equal(t, 503, recorder.Code)
	require.JSONEq(t, `{"channel":3}`, recorder.Body.String())
}

// TestAutomaticChannelsRespectReplayBarriers reproduces committed output, usage,
// remote continuation, business rejection and server-tool execution. Each case
// stays on the first channel even after observing an upstream failure.
func TestAutomaticChannelsRespectReplayBarriers(t *testing.T) {
	for _, kind := range []string{"output", "usage", "continuation", "business", "server_tool"} {
		t.Run(kind, func(t *testing.T) {
			calls := 0
			router := channelTestRouter(t, func(c *gin.Context) {
				calls++
				AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{ProviderID: 1, UpstreamStatusCode: 503})
				switch kind {
				case "output":
					c.Header("Content-Type", "text/event-stream")
					_, err := c.Writer.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"started\"}}]}\n\n"))
					require.NoError(t, err)
				case "usage":
					requeststate.BlockChannelReplay(c.Request.Context())
				case "business":
					MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
				}
				c.JSON(503, gin.H{"error": "failure"})
			})
			body := `{"model":"same-model"}`
			if kind == "continuation" {
				body = `{"model":"same-model","previous_response_id":"resp_owned"}`
			}
			if kind == "server_tool" {
				body = `{"model":"same-model","tools":[{"type":"code_interpreter"}]}`
			}
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body)))
			require.Equal(t, 1, calls)
		})
	}
}

// TestAutomaticChannelsRetrySplitStreamingPreamble fragments empty role and failure
// events across writes. Preamble bytes from failed channels never reach the final
// successful stream, and generated output closes replay immediately.
func TestAutomaticChannelsRetrySplitStreamingPreamble(t *testing.T) {
	calls := 0
	router := channelTestRouter(t, func(c *gin.Context) {
		calls++
		c.Header("Content-Type", "text/event-stream")
		if calls == 1 {
			AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{ProviderID: 1, UpstreamStatusCode: 429})
			for _, chunk := range []string{"data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n", "\nevent: error\ndata: {\"error\":{\"message\":\"limited\"}}\n", "\n"} {
				_, err := c.Writer.Write([]byte(chunk))
				require.NoError(t, err)
			}
			return
		}
		_, err := c.Writer.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ready\"}}]}\n\n"))
		require.NoError(t, err)
	})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"same-model","stream":true}`)))
	require.Equal(t, 2, calls)
	require.Contains(t, recorder.Body.String(), "ready")
	require.NotContains(t, recorder.Body.String(), "limited")
}

// TestAutomaticChannelsSkipExhaustedCapacity marks the first channel unavailable
// before any upstream attempt starts. A later channel can serve the request,
// while a local funding or policy rejection remains a terminal response.
func TestAutomaticChannelsSkipExhaustedCapacity(t *testing.T) {
	calls := 0
	router := channelTestRouter(t, func(c *gin.Context) {
		calls++
		if calls == 1 {
			c.Set("ops_routing_capacity_limited", true)
			c.JSON(503, gin.H{"error": "no providers"})
			return
		}
		c.JSON(200, gin.H{"ok": true})
	})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"same-model"}`)))
	require.Equal(t, 2, calls)
	require.Equal(t, 200, recorder.Code)
}

// TestAutomaticChannelsRejectAmbiguousModels submits duplicate, case-variant and
// nonstring model fields. Selection rejects the input before any terminal handler
// can forward a model different from the one used for routing.
func TestAutomaticChannelsRejectAmbiguousModels(t *testing.T) {
	for _, body := range []string{`{"model":"a","model":"b"}`, `{"Model":"a"}`, `{"model":42}`, `{"model":{}}`} {
		calls := 0
		router := channelTestRouter(t, func(*gin.Context) { calls++ })
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body)))
		require.Equal(t, 400, recorder.Code)
		require.Zero(t, calls)
	}
}

// TestAutomaticContinuationUsesVerifiedOwner binds a response to a later channel.
// The coordinator selects that authorized owner and excludes every other provider,
// even though the same model exists in earlier administrator-configured channels.
func TestAutomaticContinuationUsesVerifiedOwner(t *testing.T) {
	store := &channelContinuationStore{}
	state := &channelRoutingExecution{body: []byte(`{"previous_response_id":"resp-owned"}`), responseStore: store, visited: make(map[int64]bool), loaded: map[int64]bool{7: true, 8: true}, failed: make(map[int64]struct{}), cooling: make(map[int64]time.Time), blocked: "previous_response_id"}
	key := &apikey.APIKey{ID: 9, User: &identity.User{ID: 1}, CompositeGroups: []apikey.APIKeyCompositeGroup{{GroupID: 1, Group: &routing.Group{ID: 1, Status: "active"}}, {GroupID: 2, Group: &routing.Group{ID: 2, Status: "active"}}}}
	require.NoError(t, state.resolveContinuation(context.Background(), key))
	require.True(t, state.visited[1])
	require.False(t, state.visited[2])
	require.EqualValues(t, 8, state.requiredProviderID)
	require.Contains(t, state.exclusions([]int64{7, 8}), int64(7))
	require.NotContains(t, state.exclusions([]int64{7, 8}), int64(8))
}

// GetHTTPResponseOwner exposes a matching tenant only under the stored response group.
func (*channelContinuationStore) GetHTTPResponseOwner(_ context.Context, groupID int64, _ string) (int64, int64, bool, error) {
	return 1, 9, groupID == 2, nil
}

// GetResponseProvider returns the original provider associated with that verified response.
func (*channelContinuationStore) GetResponseProvider(context.Context, int64, string) (int64, error) {
	return 8, nil
}

// ReserveRequest counts one admitted client request against its Key limits.
func (c *channelRequestLeaseFixture) ReserveRequest(context.Context, int64, string, int, int) (time.Duration, error) {
	c.reserved++
	return 0, nil
}

// RefreshRequest accepts lease renewal while the fixture request remains active.
func (*channelRequestLeaseFixture) RefreshRequest(context.Context, int64, string) error { return nil }

// ReleaseRequest records final cleanup after every automatic attempt has finished.
func (c *channelRequestLeaseFixture) ReleaseRequest(context.Context, int64, string) error {
	c.released++
	return nil
}

// TestAutomaticChannelsRetainOneKeyRequestLease forces two automatic upstream attempts.
// The Key reserves capacity once, keeps its server abort signal active during the
// second attempt, and releases the shared lease after the complete client request.
func TestAutomaticChannelsRetainOneKeyRequestLease(t *testing.T) {
	cache := &channelRequestLeaseFixture{}
	calls := 0
	router := channelTestRouter(t, func(c *gin.Context) {
		calls++
		require.NoError(t, c.Request.Context().Err())
		require.Equal(t, 1, cache.reserved)
		require.Zero(t, cache.released)
		if calls == 1 {
			AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{ProviderID: 1, UpstreamStatusCode: 503})
			c.JSON(503, gin.H{"error": "unavailable"})
			return
		}
		c.JSON(200, gin.H{"ok": true})
	}, cache)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"same-model"}`)))
	require.Equal(t, 2, calls)
	require.Equal(t, 1, cache.reserved)
	require.Equal(t, 1, cache.released)
}

// TestAutomaticChannelsReauthenticateWithOriginalAlias exercises real gateway admission.
// The first channel fails, the second reloads identity and rewrites the original
// alias once, and only its successful response reaches the final client writer.
func TestAutomaticChannelsReauthenticateWithOriginalAlias(t *testing.T) {
	key := &apikey.APIKey{ID: 9, UserID: 1, Key: "sk-channel-auth", Status: "active", IsComposite: true, User: &identity.User{ID: 1, Status: "active", Balance: 100, GroupRestrictionsLoaded: true}, ModelMapping: map[string]string{"friendly": "vendor/model"}}
	for id := int64(1); id <= 2; id++ {
		key.CompositeGroups = append(key.CompositeGroups, apikey.APIKeyCompositeGroup{GroupID: id, Group: &routing.Group{ID: id, Status: "active", AllowedProtocols: []ProtocolID{ProtocolOpenAIChatCompletions}}})
	}
	repo := &channelAuthenticationRepo{key: key}
	service := apikey.NewAPIKeyService(repo, nil, nil, nil, nil, nil, &apikey.Options{ResolveGroupModel: func(_ context.Context, _ int64, model, _, _ string) (bool, error) {
		require.Equal(t, "vendor/model", model)
		return true, nil
	}})
	router := gin.New()
	calls := 0
	router.POST("/v1/chat/completions", WithAutomaticChannelRouting(NewAPIKeyAuthorization(service, nil, APIKeyAuthorizationOptions{}), nil), func(c *gin.Context) {
		calls++
		body, err := ReadAndRestoreRequestBody(c.Request)
		require.NoError(t, err)
		require.Equal(t, "vendor/model", gjson.GetBytes(body, "model").String())
		require.NoError(t, c.Request.Context().Err())
		if calls == 1 {
			AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{ProviderID: 1, UpstreamStatusCode: 503})
			c.JSON(503, gin.H{"error": "unavailable"})
			return
		}
		require.EqualValues(t, 2, *GetExecutionAPIKey(c).GroupID)
		c.JSON(200, gin.H{"model": "vendor/model", "answer": "ready"})
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"friendly"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer sk-channel-auth")
	router.ServeHTTP(recorder, request)
	require.Equal(t, 2, calls)
	require.Equal(t, 2, repo.reads)
	require.Equal(t, 200, recorder.Code)
	require.JSONEq(t, `{"model":"friendly","answer":"ready"}`, recorder.Body.String())
}
