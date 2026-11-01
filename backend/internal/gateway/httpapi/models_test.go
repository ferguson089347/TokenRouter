package httpapi

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// modelForbiddenBody rejects unexpected body reads during model catalogue
// requests.
type modelForbiddenBody struct{}

// Read panics on any catalogue request-body access by the handler. The model
// tests install this body to detect unwanted input consumption before their
// status and catalogue assertions can pass.
func (modelForbiddenBody) Read([]byte) (int, error) { panic("models endpoint read request body") }

// Close finishes the stateless forbidden body without an error.
func (modelForbiddenBody) Close() error { return nil }

// modelsContext creates a GET model request with a body that panics if read.
// It returns a fresh Gin context and recorder so each test can set route
// parameters and inspect presentation.
func modelsContext() (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	c.Request.Body = modelForbiddenBody{}
	return c, w
}

// TestModelsBoundEmptyDoesNotFallBackOrConsume supplies an authenticated
// bound Key and empty eligibility result. The ordinary list remains empty,
// and the fixture records no upstream model resource query.
func TestModelsBoundEmptyDoesNotFallBackOrConsume(t *testing.T) {
	p := &modelsBackendStub{key: prefaceKey()}
	c, w := modelsContext()
	NewModelsHandler(p, &modelsCatalogStub{}).Models(c)
	require.Equal(t, 200, w.Code)
	require.JSONEq(t, `{"object":"list","data":[]}`, w.Body.String())
	require.Empty(t, p.paths)
}

// TestModelsCapabilitiesPreserveVisibilityAndAliases requests metadata for a
// custom model list with one Key alias. Hidden candidates remain excluded and
// the alias inherits only its eligible target's protocol permissions.
func TestModelsCapabilitiesPreserveVisibilityAndAliases(t *testing.T) {
	p := &modelsBackendStub{key: prefaceKey(), result: routing.RequestableModelsResult{Models: []routing.RequestableModel{
		{ID: "target", Protocols: []protocol.ProtocolID{protocol.ProtocolAnthropicMessages}, NativeProtocols: []protocol.ProtocolID{protocol.ProtocolAnthropicMessages}},
		{ID: "hidden", Protocols: []protocol.ProtocolID{protocol.ProtocolOpenAIResponses}},
	}}}
	p.key.Group.ModelsListConfig = routing.GroupModelsListConfig{Enabled: true, Models: []string{"target"}}
	p.key.ModelMapping = map[string]string{"custom-alias": "target", "forbidden": "missing"}
	c, w := modelsContext()
	c.Request.URL.RawQuery = "include=capabilities"
	NewModelsHandler(p, &modelsCatalogStub{}).Models(c)

	require.Equal(t, http.StatusOK, w.Code)
	data := gjson.GetBytes(w.Body.Bytes(), "data").Array()
	require.Len(t, data, 2)
	require.Equal(t, "target", data[0].Get("id").String())
	require.Equal(t, "custom-alias", data[1].Get("id").String())
	require.Equal(t, "anthropic_messages", data[1].Get("protocols.0").String())
	require.Equal(t, "anthropic_messages", data[1].Get("native_protocols.0").String())
	require.JSONEq(t, `{}`, data[1].Get("attributes").Raw)
	require.NotContains(t, w.Body.String(), "hidden")
	require.NotContains(t, w.Body.String(), "forbidden")
	require.Equal(t, 1, p.resolveCalls)
	require.Empty(t, p.paths)
}

// TestModelsCapabilitiesKeepCompositeBindingsSeparate resolves the same model
// under two authorized prefixes with different protocols. Disabled bindings
// disappear, and each alias retains its own group's permitted entry point.
func TestModelsCapabilitiesKeepCompositeBindingsSeparate(t *testing.T) {
	groups := []*routing.Group{
		{ID: 1, Status: "active"},
		{ID: 2, Status: "active"},
		{ID: 3, Status: "disabled"},
	}
	p := &modelsBackendStub{key: &apikey.APIKey{
		IsComposite:  true,
		User:         &identity.User{ID: 7},
		ModelMapping: map[string]string{"alias": "same-model"},
		CompositeGroups: []apikey.APIKeyCompositeGroup{
			{GroupID: 1, Prefix: "first", Group: groups[0]},
			{GroupID: 2, Prefix: "second", Group: groups[1]},
			{GroupID: 3, Prefix: "disabled", Group: groups[2]},
		},
	}, byGroup: map[int64]routing.RequestableModelsResult{
		1: {Models: []routing.RequestableModel{{ID: "same-model", Protocols: []protocol.ProtocolID{protocol.ProtocolAnthropicMessages}}}},
		2: {Models: []routing.RequestableModel{{ID: "same-model", Protocols: []protocol.ProtocolID{protocol.ProtocolOpenAIResponses}}}},
	}}
	c, w := modelsContext()
	c.Request.URL.RawQuery = "include=capabilities"
	NewModelsHandler(p, &modelsCatalogStub{}).Models(c)

	require.Equal(t, http.StatusOK, w.Code)
	data := gjson.GetBytes(w.Body.Bytes(), "data").Array()
	require.Len(t, data, 4)
	protocols := make(map[string]string, len(data))
	for _, item := range data {
		protocols[item.Get("id").String()] = item.Get("protocols.0").String()
	}
	require.Equal(t, "anthropic_messages", protocols["first/alias"])
	require.Equal(t, "openai_responses", protocols["second/alias"])
	require.NotContains(t, w.Body.String(), "disabled")
	require.Equal(t, 2, p.resolveCalls)
}

// TestGeminiModelsEmptyCapabilityDoesNotQueryUpstream supplies an empty
// capability catalogue beside a configured upstream response. Gemini returns
// an empty local list and never asks that upstream reader for resources.
func TestGeminiModelsEmptyCapabilityDoesNotQueryUpstream(t *testing.T) {
	p := &modelsBackendStub{key: prefaceKey(), response: &ModelHTTPResponse{StatusCode: 200, Body: []byte(`{"models":[{"name":"models/phantom"}]}`)}}
	c, w := modelsContext()
	NewModelsHandler(p, &modelsCatalogStub{}).GeminiV1BetaListModels(c)
	require.Equal(t, 200, w.Code)
	require.JSONEq(t, `{"models":[]}`, w.Body.String())
	require.Empty(t, p.paths)
}

// TestGeminiModelsUseUnifiedProtocolCapabilities mixes Responses-only,
// concrete Gemini-capable and wildcard candidates with Key mappings. The list
// contains the concrete Gemini model and its eligible exact alias, with no
// upstream query.
func TestGeminiModelsUseUnifiedProtocolCapabilities(t *testing.T) {
	p := &modelsBackendStub{key: prefaceKey(), selectErr: errors.New("must not select an upstream"), result: routing.RequestableModelsResult{Models: []routing.RequestableModel{
		{ID: "gpt-5.4", Protocols: []protocol.ProtocolID{protocol.ProtocolOpenAIResponses}},
		{ID: "gemini-2.5-flash", Protocols: []protocol.ProtocolID{protocol.ProtocolGeminiGenerateContent, protocol.ProtocolOpenAIResponses}},
		{ID: "gemini-*", Protocols: []protocol.ProtocolID{protocol.ProtocolGeminiGenerateContent}},
	}}}
	p.key.ModelMapping = map[string]string{"my-model": "gemini-2.5-flash", "hidden": "gpt-5.4", "wild-*": "gemini-2.5-flash"}
	c, w := modelsContext()
	NewModelsHandler(p, &modelsCatalogStub{}).GeminiV1BetaListModels(c)
	require.Equal(t, 200, w.Code)
	require.JSONEq(t, `{"models":[{"name":"models/gemini-2.5-flash"},{"name":"models/my-model","displayName":"my-model"}]}`, w.Body.String())
	require.Empty(t, p.paths)
}

// TestGeminiModelGetRequiresRequestableModel requests known, unknown and
// unsafe model URL names. Their statuses distinguish available resources,
// missing eligibility and invalid segments, and no case queries upstream
// model resources.
func TestGeminiModelGetRequiresRequestableModel(t *testing.T) {
	for _, name := range []string{"known", "unknown", "bad/model"} {
		t.Run(name, func(t *testing.T) {
			p := &modelsBackendStub{key: prefaceKey(), result: routing.RequestableModelsResult{Models: []routing.RequestableModel{{ID: "known", Protocols: []protocol.ProtocolID{protocol.ProtocolGeminiGenerateContent}}}}}
			c, w := modelsContext()
			c.Params = gin.Params{{Key: "model", Value: "/" + name}}
			NewModelsHandler(p, &modelsCatalogStub{}).GeminiV1BetaGetModel(c)
			want := map[string]int{"known": 200, "unknown": 404, "bad/model": 400}[name]
			require.Equal(t, want, w.Code)
			require.Empty(t, p.paths)
		})
	}
}

// TestGeminiModelsCompositePermissionsAndForcedPlatform combines a permitted
// Gemini binding with a text-only binding under forced Antigravity. List and
// resource lookup retain the permitted prefix, and platform forcing cannot
// bypass denied group protocol permission.
func TestGeminiModelsCompositePermissionsAndForcedPlatform(t *testing.T) {
	allowed := &routing.Group{ID: 1, Status: "active", AllowedProtocols: []protocol.ProtocolID{protocol.ProtocolGeminiGenerateContent}}
	disabled := &routing.Group{ID: 2, Status: "active", AllowedProtocols: []protocol.ProtocolID{protocol.ProtocolOpenAIResponses}}
	p := &modelsBackendStub{forced: "antigravity", key: &apikey.APIKey{IsComposite: true, User: &identity.User{ID: 7}, CompositeGroups: []apikey.APIKeyCompositeGroup{{GroupID: 1, Prefix: "mixed", Group: allowed}, {GroupID: 2, Prefix: "text", Group: disabled}}}, byGroup: map[int64]routing.RequestableModelsResult{1: {Models: []routing.RequestableModel{{ID: "gemini-2.5-flash", Protocols: []protocol.ProtocolID{protocol.ProtocolGeminiGenerateContent}}}}, 2: {Models: []routing.RequestableModel{{ID: "gpt-5.4", Protocols: []protocol.ProtocolID{protocol.ProtocolOpenAIResponses}}}}}}
	c, w := modelsContext()
	handler := NewModelsHandler(p, &modelsCatalogStub{})
	handler.GeminiV1BetaListModels(c)
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), "models/mixed/gemini-2.5-flash")
	require.NotContains(t, w.Body.String(), "models/text/")
	require.Equal(t, []string{"antigravity"}, p.resolvedPlatforms)
	c, w = modelsContext()
	c.Params = gin.Params{{Key: "model", Value: "/mixed/gemini-2.5-flash"}}
	handler.GeminiV1BetaGetModel(c)
	require.Equal(t, 200, w.Code)
	p.key = &apikey.APIKey{Group: disabled}
	c, w = modelsContext()
	handler.GeminiV1BetaListModels(c)
	require.Equal(t, 403, w.Code, "强制提供商平台不能绕过分组协议权限")
}

// TestGeminiListPropagatesAttributeError supplies failed catalogue resolution.
// The list endpoint returns unavailable so clients cannot mistake a configuration
// outage for a successfully resolved empty model list.
func TestGeminiListPropagatesAttributeError(t *testing.T) {
	p := &modelsBackendStub{key: prefaceKey(), result: routing.RequestableModelsResult{Restricted: true, Err: errors.New("attribute configuration unavailable")}}
	c, w := modelsContext()
	NewModelsHandler(p, &modelsCatalogStub{}).GeminiV1BetaListModels(c)
	require.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
}
