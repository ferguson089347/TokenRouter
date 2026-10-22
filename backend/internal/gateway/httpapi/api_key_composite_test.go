package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// SystemOne composite-prefix failures use the decision endpoint's model error
// envelope, including its machine-readable code. Endpoint matching accepts a
// trailing slash while keeping unrelated SystemOne-like paths out of that response choice.

// TestSystemOneCompositeErrorUsesDecisionEndpoint exercises the decision path
// with and without a trailing slash. Both requests must abort with a model
// parameter error and the composite-prefix-required code, while other paths
// retain their own endpoint classification.
func TestSystemOneCompositeErrorUsesDecisionEndpoint(t *testing.T) {
	for _, path := range []string{EndpointSystemOne, EndpointSystemOne + "/"} {
		require.True(t, IsSystemOneEndpoint(path))
		require.False(t, IsOpenAICompositeEndpoint(path))
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, path, nil)
		AbortCompositeKeyError(c, apikey.ErrModelRequired)
		require.True(t, c.IsAborted())
		require.Equal(t, http.StatusBadRequest, rec.Code)
		require.Contains(t, rec.Body.String(), `"code":"MODEL_REQUIRED"`)
		require.Contains(t, rec.Body.String(), `"param":"model"`)
	}
	require.False(t, IsSystemOneEndpoint("/v1/chat/completions"))
	require.False(t, IsSystemOneEndpoint("/other/systemone"))
	require.True(t, IsOpenAICompositeEndpoint("/v1/chat/completions"))
}

// TestCompositeRequestProtocolMatchesDedicatedGuards covers text, token-count,
// Gemini, Codex and media aliases. Channel selection receives the protocol that
// its later route guard enforces, including display-only catalogue entries.
func TestCompositeRequestProtocolMatchesDedicatedGuards(t *testing.T) {
	for _, tc := range []struct {
		path     string     // Registered model request path.
		protocol ProtocolID // Required channel protocol.
	}{
		{"/v1/messages", ProtocolAnthropicMessages},
		{"/messages/count_tokens", ProtocolAnthropicMessages},
		{"/antigravity/v1/messages", ProtocolAnthropicMessages},
		{"/v1/chat/completions", ProtocolOpenAIChatCompletions},
		{"/chat/completions", ProtocolOpenAIChatCompletions},
		{"/responses", ProtocolOpenAIResponses},
		{"/v1/responses/input_tokens", ProtocolOpenAIResponses},
		{"/backend-api/codex/responses", ProtocolOpenAIResponses},
		{"/v1/responses/compact", ProtocolResponsesCompact},
		{"/v1beta/models/vendor/model:generateContent", ProtocolGeminiGenerateContent},
		{"/antigravity/v1beta/models/vendor/model:countTokens", ProtocolGeminiGenerateContent},
		{"/v1/images/edits", ProtocolImagesEdits},
	} {
		require.Equal(t, tc.protocol, compositeRequestProtocol(http.MethodPost, tc.path), tc.path)
	}
}

// TestResolveModelChannelSkipsIncompatibleTextProtocol submits a model available
// through two channels with different text protocols. The request chooses its
// Chat-compatible channel even when the Messages-only channel has higher priority.
func TestResolveModelChannelSkipsIncompatibleTextProtocol(t *testing.T) {
	key := &apikey.APIKey{UserID: 1, IsComposite: true, User: &identity.User{ID: 1, GroupRestrictionsLoaded: true}, CompositeGroups: []apikey.APIKeyCompositeGroup{
		{GroupID: 1, Group: &routing.Group{ID: 1, Status: "active", AllowedProtocols: []ProtocolID{ProtocolAnthropicMessages}}},
		{GroupID: 2, Group: &routing.Group{ID: 2, Status: "active", AllowedProtocols: []ProtocolID{ProtocolOpenAIChatCompletions}}},
	}}
	service := apikey.NewAPIKeyService(nil, nil, nil, nil, nil, nil, &apikey.Options{ResolveGroupModel: func(_ context.Context, _ int64, model, clientProtocol, _ string) (bool, error) {
		require.Equal(t, "vendor/model", model)
		require.Equal(t, string(ProtocolOpenAIChatCompletions), clientProtocol)
		return true, nil
	}})
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"vendor/model"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	selected, err := ResolveCompositeAPIKeyRequest(c, service, key)
	require.NoError(t, err)
	require.Equal(t, int64(2), *selected.GroupID)
	require.Nil(t, key.GroupID)
}
