package httpapi

import (
	"maps"
	"net/http"
	"slices"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// models_capabilities.go adds protocol and attribute metadata to explicitly
// requested model lists. Plugin clients need the protocols permitted by the
// authenticated Key because a public name can be an alias or contain a
// composite binding prefix. Neither spelling identifies the underlying model
// or proves that a particular generation endpoint is allowed.
//
// Metadata is captured from the same resolved routes as the ordinary model
// list. Key aliases inherit their target's metadata within one group, then
// composite prefixes separate bindings. The final writer consumes only IDs
// that survived the existing visibility and permission checks. Attributes
// describe common route capabilities and contain no upstream credentials or
// addresses; missing attribute observations stay unknown.

const (
	// modelCapabilitiesContextKey keeps optional metadata within the current
	// request, preventing independent Key or composite binding observations from mixing.
	modelCapabilitiesContextKey = "gateway_model_capabilities"
)

// modelListCapability contains public protocol permissions and common model
// attributes for one resolved request identity.
type modelListCapability struct {
	// protocols and native are copied from the same group's resolved model.
	// Aliases reuse these client permissions, while generation requests independently
	// enforce their current protocol and model admission rules.
	protocols []capability.ProtocolID // allowed client entry protocols
	native    []capability.ProtocolID // protocols requiring no conversion
	// attributes holds common route metadata for response serialization. Nested
	// values remain shared with the captured resolution and stay read-only until
	// response serialization completes.
	attributes modelcatalog.Attributes // common route capability metadata
}

// captureModelListMetadata resolves display attributes or optional capabilities
// for the supplied group. Callers pass eligible models before visibility
// filtering; the writer receives the final visible IDs.
//
// Exact Key aliases inherit their one-hop target before a composite prefix is
// attached. Existing prefixed entries win when bindings repeat a public name,
// matching the catalogue's first occurrence rule. Alias assignment reads the
// original target observations, preventing chained mappings from accidentally
// inheriting a second hop. Requests without include=capabilities retain the
// ordinary display path. Missing attribute reads leave capabilities unknown.
func (h *ModelsHandler) captureModelListMetadata(c *gin.Context, groupID *int64, models []routing.RequestableModel, prefix string) {
	if c.Query("include") != "capabilities" {
		h.captureModelAttributes(c, groupID, models, prefix)
		return
	}
	var attributes map[string]routing.EffectiveModelAttributes
	if h.attributes != nil && groupID != nil {
		// Attribute lookup is optional display enrichment. A failed profile read
		// leaves metadata unknown while preserving the independently resolved protocol permissions.
		if resolved, err := h.attributes.ResolveModels(c.Request.Context(), *groupID, models); err == nil {
			attributes = resolved
		}
	}
	values, _ := c.Get(modelCapabilitiesContextKey)
	stored, _ := values.(map[string]modelListCapability)
	if stored == nil {
		stored = make(map[string]modelListCapability)
	}
	local := make(map[string]modelListCapability, len(models))
	for _, model := range models {
		local[model.ID] = modelListCapability{
			protocols:  slices.Clone(model.Protocols),
			native:     slices.Clone(model.NativeProtocols),
			attributes: attributes[model.ID].Attributes,
		}
	}
	if key, ok := h.backend.Access(c); ok && key != nil {
		// Expand within this group's eligible names before prefixing, so an alias
		// never inherits protocol permissions from a different composite binding.
		aliases := make(map[string]modelListCapability)
		for _, alias := range apikey.AppendAPIKeyModelAliases(routing.RequestableModelIDs(models), key.ModelMapping) {
			if target, changed := key.ResolveModelMapping(alias); changed {
				aliases[alias] = local[target]
			}
		}
		maps.Copy(local, aliases)
	}
	for id, value := range local {
		if _, exists := stored[prefix+id]; !exists {
			stored[prefix+id] = value
		}
	}
	c.Set(modelCapabilitiesContextKey, stored)
}

// writeModelsWithCapabilities writes an OpenAI list when the caller requests
// include=capabilities and reports whether it handled the response. Callers
// supply the final authorized IDs, including aliases and composite prefixes.
//
// Protocol arrays remain present even when empty, allowing plugins to
// distinguish no supported endpoint from an older server omitting metadata.
// Missing attributes serialize as an empty object. This method never adds
// candidates, consults upstreams, or uses metadata as authorization; generation
// requests still pass the gateway's normal admission checks.
// @project-doc docs/interfaces/magpie_import.md#plugin_models
func (h *ModelsHandler) writeModelsWithCapabilities(c *gin.Context, ids []string) bool {
	if c.Query("include") != "capabilities" {
		return false
	}
	raw, _ := c.Get(modelCapabilitiesContextKey)
	values, _ := raw.(map[string]modelListCapability)
	items := make([]gin.H, 0, len(ids))
	for _, id := range ids {
		value := values[id]
		name := id
		if value.attributes.DisplayName != nil {
			name = *value.attributes.DisplayName
		}
		items = append(items, gin.H{
			"id":               id,
			"object":           "model",
			"display_name":     name,
			"protocols":        append([]capability.ProtocolID{}, value.protocols...),
			"native_protocols": append([]capability.ProtocolID{}, value.native...),
			"attributes":       value.attributes,
		})
	}
	c.JSON(http.StatusOK, gin.H{"object": "list", "data": items})
	return true
}
