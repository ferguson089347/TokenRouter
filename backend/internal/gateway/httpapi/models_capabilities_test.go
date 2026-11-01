package httpapi

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// capabilityAttributeRepo supplies an immutable group profile for capability
// capture tests without introducing upstream or persistent storage access.
type capabilityAttributeRepo struct {
	routing.ModelAttributeRepository                               // unused profile repository operations
	config                           *routing.ModelAttributeConfig // immutable fixture group profile
}

// ForGroup returns the fixture profile for any requested group ID.
// The profile stays shared and immutable for the duration of each capture;
// the attribute service owns interpretation and result copying.
func (r capabilityAttributeRepo) ForGroup(context.Context, int64) (*routing.ModelAttributeConfig, error) {
	return r.config, nil
}

// TestCaptureModelListMetadataKeepsAliasAttributes supplies resolved group
// attributes under a public target. Capturing its Key alias retains explicit
// false capabilities and token limits instead of guessing from the alias name.
func TestCaptureModelListMetadataKeepsAliasAttributes(t *testing.T) {
	contextSize := 120000
	reasoning := false
	p := &modelsBackendStub{key: prefaceKey()}
	p.key.ModelMapping = map[string]string{"alias": "target"}
	c, w := modelsContext()
	c.Request.URL.RawQuery = "include=capabilities"
	attributes := &routing.ModelAttributeService{Repo: capabilityAttributeRepo{config: &routing.ModelAttributeConfig{
		Status: routing.StatusActive,
		Rules:  []routing.ModelAttributeRule{{Models: []string{"target"}, Attributes: modelcatalog.Attributes{Context: &contextSize, Reasoning: &reasoning}}},
	}}}
	handler := NewModelsHandler(p, &modelsCatalogStub{}, attributes)
	handler.captureModelListMetadata(c, p.key.GroupID, []routing.RequestableModel{
		{ID: "target", Protocols: []protocol.ProtocolID{protocol.ProtocolOpenAIResponses}},
	}, "")
	require.True(t, handler.writeModelsWithCapabilities(c, []string{"alias"}))
	data := gjson.GetBytes(w.Body.Bytes(), "data.0")
	require.Equal(t, "alias", data.Get("id").String())
	require.Equal(t, int64(contextSize), data.Get("attributes.context").Int())
	require.True(t, data.Get("attributes.reasoning").Exists())
	require.False(t, data.Get("attributes.reasoning").Bool())
}

// TestCaptureModelListMetadataDoesNotChainAliases supplies mappings whose
// targets are themselves aliases in an unfavorable model order. Each public
// name must inherit its original one-hop target, independent of assignment order.
func TestCaptureModelListMetadataDoesNotChainAliases(t *testing.T) {
	p := &modelsBackendStub{key: prefaceKey()}
	p.key.ModelMapping = map[string]string{"a": "b", "b": "c"}
	c, w := modelsContext()
	c.Request.URL.RawQuery = "include=capabilities"
	handler := NewModelsHandler(p, &modelsCatalogStub{})
	handler.captureModelListMetadata(c, p.key.GroupID, []routing.RequestableModel{
		{ID: "b", Protocols: []protocol.ProtocolID{protocol.ProtocolAnthropicMessages}},
		{ID: "a", Protocols: []protocol.ProtocolID{protocol.ProtocolOpenAIResponses}},
		{ID: "c", Protocols: []protocol.ProtocolID{protocol.ProtocolGeminiGenerateContent}},
	}, "")
	require.True(t, handler.writeModelsWithCapabilities(c, []string{"a", "b"}))
	data := gjson.GetBytes(w.Body.Bytes(), "data").Array()
	require.Equal(t, "anthropic_messages", data[0].Get("protocols.0").String())
	require.Equal(t, "gemini_generate_content", data[1].Get("protocols.0").String())
}

// TestWriteModelsWithCapabilitiesRequiresOptIn leaves the ordinary query
// unchanged, then requests an explicitly empty catalogue. Only the latter
// writes a JSON response, preserving the existing default model-list contract.
func TestWriteModelsWithCapabilitiesRequiresOptIn(t *testing.T) {
	handler := NewModelsHandler(&modelsBackendStub{}, &modelsCatalogStub{})
	c, w := modelsContext()
	require.False(t, handler.writeModelsWithCapabilities(c, nil))
	require.Empty(t, w.Body.String())
	c, w = modelsContext()
	c.Request.URL.RawQuery = "include=capabilities"
	require.True(t, handler.writeModelsWithCapabilities(c, nil))
	require.JSONEq(t, `{"object":"list","data":[]}`, w.Body.String())
}
