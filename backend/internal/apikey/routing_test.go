package apikey

import (
	"context"
	"errors"
	"maps"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// routingSettingsStub retains administrator defaults for sequential routing tests.
type routingSettingsStub struct {
	values map[string]string // Stored settings by name.
	err    error             // Injected persistence failure.
}

// routingGroupsStub supplies current channel state and missing-group errors.
type routingGroupsStub struct {
	GroupRepository                          // Unused methods require fixture setup.
	groups          map[int64]*routing.Group // Current group state by identifier.
}

// GetMultiple returns the fixture values or its injected storage failure.
func (s *routingSettingsStub) GetMultiple(context.Context, []string) (map[string]string, error) {
	return s.values, s.err
}

// Set replaces a setting when the fixture accepts the write.
func (s *routingSettingsStub) Set(_ context.Context, name, value string) error {
	if s.err != nil {
		return s.err
	}
	s.values[name] = value
	return nil
}

// SetMultiple applies a validated fixture batch after its configured failure check.
func (s *routingSettingsStub) SetMultiple(_ context.Context, values map[string]string) error {
	if s.err != nil {
		return s.err
	}
	maps.Copy(s.values, values)
	return nil
}

// GetByID returns the current group or the routing missing-group error.
func (s routingGroupsStub) GetByID(_ context.Context, id int64) (*routing.Group, error) {
	if group := s.groups[id]; group != nil {
		return group, nil
	}
	return nil, routing.ErrGroupNotFound
}

// TestApplyRoutingDefaultsRefreshesOrderedPermissions changes administrator defaults
// while an inherited credential already has bindings. Current order and permissions
// determine each resulting view, and custom credentials keep their saved channels.
func TestApplyRoutingDefaultsRefreshesOrderedPermissions(t *testing.T) {
	ctx := context.Background()
	settings := &routingSettingsStub{values: map[string]string{DefaultRoutingGroupsKey: `[2,1,99,3,4]`}}
	groups := routingGroupsStub{groups: map[int64]*routing.Group{
		1: {ID: 1, Status: "active"}, 2: {ID: 2, Status: "active"},
		3: {ID: 3, Status: "active", IsExclusive: true}, 4: {ID: 4, Status: "disabled"},
	}}
	s := NewAPIKeyService(nil, nil, groups, nil, nil, nil, &Options{RoutingSettings: settings})
	key := &APIKey{ID: 5, UserID: 7, IsComposite: true, UseDefaultGroups: true, User: &identity.User{ID: 7, Status: "active", GroupRestrictionsLoaded: true}}
	require.NoError(t, s.ApplyRoutingDefaults(ctx, key))
	require.Equal(t, int64(2), key.CompositeGroups[0].GroupID)
	require.Equal(t, int64(1), key.CompositeGroups[1].GroupID)
	require.Len(t, key.CompositeGroups, 2)
	prior := CopyAPIKey(key)
	settings.values[DefaultRoutingGroupsKey] = `[1]`
	require.NoError(t, s.ApplyRoutingDefaults(ctx, key))
	require.Len(t, key.CompositeGroups, 1)
	require.Len(t, prior.CompositeGroups, 2)
	key.UseDefaultGroups = false
	settings.values[DefaultRoutingGroupsKey] = `[2]`
	require.NoError(t, s.ApplyRoutingDefaults(ctx, key))
	require.Equal(t, int64(1), key.CompositeGroups[0].GroupID)
	key.UseDefaultGroups = true
	settings.err = errors.New("settings unavailable")
	require.Error(t, s.ApplyRoutingDefaults(ctx, key))
}

// TestSelectModelGroupUsesOriginalIdentifierAndPriority combines protocol permission,
// model availability and an exact Key alias. The first eligible channel wins,
// while the vendor-qualified model and supplied Key remain unchanged.
func TestSelectModelGroupUsesOriginalIdentifierAndPriority(t *testing.T) {
	ctx := context.Background()
	user := &identity.User{ID: 7, Status: "active", GroupRestrictionsLoaded: true}
	key := &APIKey{ID: 5, UserID: 7, IsComposite: true, User: user, ModelMapping: map[string]string{"friendly": "vendor/model"}}
	for id := int64(1); id <= 4; id++ {
		group := &routing.Group{ID: id, Status: "active", AllowedProtocols: []protocol.ProtocolID{protocol.ProtocolOpenAIResponses}}
		if id == 1 {
			group.AllowedProtocols = []protocol.ProtocolID{protocol.ProtocolAnthropicMessages}
		}
		key.CompositeGroups = append(key.CompositeGroups, APIKeyCompositeGroup{GroupID: id, Group: group})
	}
	var examined []int64
	s := NewAPIKeyService(nil, nil, nil, nil, nil, nil, &Options{})
	s.SetGroupModelResolver(func(_ context.Context, id int64, model, clientProtocol, platform string) (bool, error) {
		examined = append(examined, id)
		require.Equal(t, "vendor/model", model)
		require.Equal(t, string(protocol.ProtocolOpenAIResponses), clientProtocol)
		require.Equal(t, "antigravity", platform)
		return id >= 3, nil
	})
	selected, err := s.SelectModelGroup(ctx, key, "friendly", string(protocol.ProtocolOpenAIResponses), "antigravity")
	require.NoError(t, err)
	require.Equal(t, []int64{2, 3}, examined)
	require.Equal(t, int64(3), *selected.GroupID)
	require.Nil(t, key.GroupID)
	require.Equal(t, "vendor/model", key.ModelMapping["friendly"])
	examined = nil
	_, err = s.SelectModelGroup(ctx, key, "vendor/model", string(protocol.ProtocolOpenAIResponses), "antigravity")
	require.NoError(t, err)
	require.Equal(t, []int64{2, 3}, examined)
	s.SetGroupModelResolver(func(context.Context, int64, string, string, string) (bool, error) { return false, nil })
	_, err = s.SelectModelGroup(ctx, key, "vendor/model", string(protocol.ProtocolOpenAIResponses), "")
	require.Error(t, err)
}

// TestSaveRoutingDefaultsValidatesCompleteOrder submits valid, duplicate and inactive
// channels. Rejected writes retain the saved order, and an empty list intentionally
// disables inherited routing until an administrator supplies channels again.
func TestSaveRoutingDefaultsValidatesCompleteOrder(t *testing.T) {
	ctx := context.Background()
	settings := &routingSettingsStub{values: map[string]string{}}
	groups := routingGroupsStub{groups: map[int64]*routing.Group{1: {ID: 1, Status: "active"}, 2: {ID: 2, Status: "active"}, 3: {ID: 3, Status: "disabled"}}}
	s := NewAPIKeyService(nil, nil, groups, nil, nil, nil, &Options{RoutingSettings: settings})
	require.NoError(t, s.SaveRoutingDefaults(ctx, []int64{2, 1}))
	for _, ids := range [][]int64{{1, 1}, {0}, {3}} {
		require.Error(t, s.SaveRoutingDefaults(ctx, ids))
		require.Equal(t, "[2,1]", settings.values[DefaultRoutingGroupsKey])
	}
	require.NoError(t, s.SaveRoutingDefaults(ctx, nil))
	ids, err := s.RoutingDefaults(ctx)
	require.NoError(t, err)
	require.Empty(t, ids)
	require.NotNil(t, ids)
}
