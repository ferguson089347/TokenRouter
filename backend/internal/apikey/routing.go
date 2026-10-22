package apikey

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// routing.go resolves a credential's ordered channels against current administrator
// defaults, payer permissions, and model eligibility. Client model names remain
// intact, including slashes belonging to vendor identifiers. Each request chooses
// the first eligible channel before funding admission, so insufficient funds do
// not silently move a request to a differently priced channel.
//
// Inherited defaults are loaded outside credential caches. Custom bindings retain
// their own order, while every selected group still needs current permission and
// specified-subscription coverage at admission. The model resolver belongs to application
// assembly and shares the catalogue rules used by public model listings. No
// request-local selection mutates a shared credential snapshot or saved setting.

// DefaultRoutingGroupsKey identifies the administrator's ordered channel selection
// persisted independently from individual credentials and read for inherited keys.
const (
	DefaultRoutingGroupsKey = "api_key_default_groups"
)

// KeyRoutingSettings reads and persists the ordered administrator channel defaults.
// Missing keys are represented by an absent entry in GetMultiple; storage errors
// stop inherited routing so an unavailable setting cannot expand access.
type KeyRoutingSettings interface {
	// GetMultiple reads current setting values and reports persistence failures.
	GetMultiple(context.Context, []string) (map[string]string, error)
	// Set replaces one setting after its caller validates the complete value.
	Set(context.Context, string, string) error
	// SetMultiple atomically replaces validated routing defaults and their cooldown.
	SetMultiple(context.Context, map[string]string) error
}

// routingGroupExclusionsKey identifies groups exhausted by the current automatic request.
type routingGroupExclusionsKey struct{}

// RoutingGroupInputs converts ordered identifiers into storage binding inputs.
// Group identifiers supply internal row labels; clients never use those labels
// in model names. Empty input is rejected when validating custom channel writes.
func RoutingGroupInputs(ids []int64) []APIKeyCompositeGroupInput {
	inputs := make([]APIKeyCompositeGroupInput, 0, len(ids))
	for _, id := range ids {
		inputs = append(inputs, APIKeyCompositeGroupInput{GroupID: id, Prefix: strconv.FormatInt(id, 10)})
	}
	return inputs
}

// RoutingDefaults reads the configured channel order, returning an allocated empty
// list when no defaults exist. Invalid stored JSON and persistence failures stop
// resolution instead of treating every active channel as an implicit default.
func (s *APIKeyService) RoutingDefaults(ctx context.Context) ([]int64, error) {
	ids := []int64{}
	if s.cfg == nil || s.cfg.RoutingSettings == nil {
		return ids, nil
	}
	values, err := s.cfg.RoutingSettings.GetMultiple(ctx, []string{DefaultRoutingGroupsKey})
	if err != nil {
		return nil, err
	}
	if raw := values[DefaultRoutingGroupsKey]; raw != "" {
		if err := json.Unmarshal([]byte(raw), &ids); err != nil {
			return nil, fmt.Errorf("decode API key defaults: %w", err)
		}
	}
	if ids == nil {
		ids = []int64{}
	}
	return ids, nil
}

// SaveRoutingDefaults validates and persists the complete administrator order.
// Empty input disables inherited routing. Each configured identifier must refer
// to an active group; payer-specific access remains a request-time check.
func (s *APIKeyService) SaveRoutingDefaults(ctx context.Context, ids []int64) error {
	if s.cfg == nil || s.cfg.RoutingSettings == nil {
		return apperror.ServiceUnavailable("KEY_DEFAULTS_UNAVAILABLE", "API key defaults are unavailable.")
	}
	if err := s.validateRoutingDefaults(ctx, ids); err != nil {
		return err
	}
	if ids == nil {
		ids = []int64{}
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	return s.cfg.RoutingSettings.Set(ctx, DefaultRoutingGroupsKey, string(raw))
}

// validateRoutingDefaults checks every group and its uniqueness before persistence.
// Empty input disables automatic routing, while configured targets require active
// groups. Payer-specific permission is resolved separately for every credential read.
func (s *APIKeyService) validateRoutingDefaults(ctx context.Context, ids []int64) error {
	if len(ids) > 0 {
		if _, err := KeyValidateCompositeGroupInputs(RoutingGroupInputs(ids)); err != nil {
			return err
		}
	}
	for _, id := range ids {
		group, err := s.groupRepo.GetByID(ctx, id)
		if err != nil {
			return err
		}
		if group == nil || !group.IsActive() {
			return ErrGroupNotAllowed
		}
	}
	return nil
}

// SaveRoutingConfiguration atomically replaces channel order and the cooldown policy.
// Validation completes before writing either value, so an invalid channel or cooldown
// leaves the administrator's previous routing settings available to every instance.
func (s *APIKeyService) SaveRoutingConfiguration(ctx context.Context, ids []int64, seconds *int) error {
	if err := s.validateRoutingDefaults(ctx, ids); err != nil {
		return err
	}
	if ids == nil {
		ids = []int64{}
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	values := map[string]string{DefaultRoutingGroupsKey: string(raw)}
	if seconds != nil {
		if *seconds < 0 || *seconds > 3600 {
			return apperror.BadRequest("INVALID_ROUTING_COOLDOWN", "Routing cooldown must be between 0 and 3600 seconds")
		}
		values["api_key_routing_cooldown_seconds"] = strconv.Itoa(*seconds)
	}
	return s.cfg.RoutingSettings.SetMultiple(ctx, values)
}

// defaultGroupInputs intersects administrator defaults with payer and plan access.
// Configured order survives filtering. Missing or inactive groups are omitted;
// a storage failure rejects preparation rather than selecting an unchecked group.
// A specified subscription narrows the administrator list during configuration
// writes. Inherited credential reads pass nil and leave subscription effectiveness
// and plan admission to model selection, keeping an expired funding choice editable
// from the console while preventing it from authorizing model consumption.
func (s *APIKeyService) defaultGroupInputs(ctx context.Context, payer *User, subscription *UserSubscription) ([]APIKeyCompositeGroupInput, error) {
	ids, err := s.RoutingDefaults(ctx)
	if err != nil {
		return nil, err
	}
	inputs := make([]APIKeyCompositeGroupInput, 0, len(ids))
	for _, id := range ids {
		group, err := s.groupRepo.GetByID(ctx, id)
		// Deleted defaults can remain in administrator order until the next edit.
		// Skip missing targets so other currently authorized channels remain selectable.
		if errors.Is(err, routing.ErrGroupNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if group == nil || !group.IsActive() || !s.KeyCanUserBindGroupInternal(payer, group) {
			continue
		}
		if subscription != nil && !subscriptionPlanIncludesGroup(subscription.Plan, id) {
			continue
		}
		inputs = append(inputs, RoutingGroupInputs([]int64{id})[0])
	}
	return inputs, nil
}

// ApplyRoutingDefaults refreshes inherited bindings on a request-owned credential.
// Managed execution credentials retain their server-selected group. An empty
// authorized intersection remains an empty channel set and rejects model calls.
// Subscription expiry is checked by admission, allowing management views to stay
// editable when the selected funding source has expired.
//
// Callers supply one request-owned or management-owned view. Groups are loaded
// before publishing the replacement channel slice, so a failed setting or group
// lookup leaves the prior Key fields available to its caller for diagnostics.
func (s *APIKeyService) ApplyRoutingDefaults(ctx context.Context, key *APIKey) error {
	if key == nil || !key.UseDefaultGroups || key.ManagedBy != nil {
		return nil
	}
	payer, err := s.KeyBillingUserForAPIKey(ctx, key)
	if err != nil {
		return err
	}
	inputs, err := s.defaultGroupInputs(ctx, payer, nil)
	if err != nil {
		return err
	}
	bindings := make([]APIKeyCompositeGroup, 0, len(inputs))
	if len(inputs) > 0 {
		bindings, err = s.KeyPrepareCompositeGroups(ctx, payer, inputs)
		if err != nil {
			return err
		}
	}
	key.CompositeGroups = bindings
	key.IsComposite = true
	key.GroupID = nil
	key.Group = nil
	key.User = payer
	return nil
}

// SetGroupModelResolver installs the shared model and protocol eligibility reader.
// Application assembly calls it before serving requests. The callback receives
// the complete client model after one Key redirect, together with forced platform.
func (s *APIKeyService) SetGroupModelResolver(resolve func(context.Context, int64, string, string, string) (bool, error)) {
	if s.cfg == nil {
		s.cfg = &Options{}
	}
	s.cfg.ResolveGroupModel = resolve
}

// SelectModelGroup selects the first permitted channel supporting this request.
// It resolves one Key alias for eligibility while the later rewrite stage applies
// that target to the payload. Each channel must satisfy activity, payer access,
// protocol permission, selected subscription coverage and shared catalogue rules.
//
// Selection stops at the first eligible channel and returns a copied Key view
// with its payer's RPM override. Funding admission follows afterward, so an
// insufficient allowance cannot silently select a differently priced channel.
func (s *APIKeyService) SelectModelGroup(ctx context.Context, key *APIKey, model, clientProtocol, platform string) (*APIKey, error) {
	if key == nil || key.User == nil {
		return nil, ErrAPIKeyNotFound
	}
	model = strings.TrimSpace(model)
	if model == "" {
		return nil, ErrModelRequired
	}
	if s.cfg == nil || s.cfg.ResolveGroupModel == nil {
		return nil, apperror.ServiceUnavailable("MODEL_ROUTING_UNAVAILABLE", "Model routing is unavailable.")
	}
	actual, _ := key.ResolveModelMapping(model)
	var subscription *UserSubscription
	if APIKeyEffectiveBillingMode(key) == APIKeyBillingModeSubscription {
		_, _, resolved, err := s.KeyResolveAPIKeyBillingConfiguration(ctx, key.User.ID, APIKeyBillingModeSubscription, key.PreferredSubscriptionID)
		if err != nil {
			return nil, err
		}
		subscription = resolved
	}
	excluded, _ := ctx.Value(routingGroupExclusionsKey{}).(map[int64]bool)
	for i := range key.CompositeGroups {
		binding := &key.CompositeGroups[i]
		if excluded[binding.GroupID] {
			continue
		}
		group := binding.Group
		if group == nil || !group.IsActive() || !s.KeyCanUserBindGroupInternal(key.User, group) {
			continue
		}
		if clientProtocol != "" && !group.AllowsClientProtocol(protocol.ProtocolID(clientProtocol)) {
			continue
		}
		if subscription != nil && !subscriptionPlanIncludesGroup(subscription.Plan, binding.GroupID) {
			continue
		}
		supported, err := s.cfg.ResolveGroupModel(ctx, binding.GroupID, actual, clientProtocol, platform)
		if err != nil {
			return nil, err
		}
		if !supported {
			continue
		}
		selected, err := s.SelectCompositeGroupForRequest(ctx, key, binding)
		if err != nil {
			return nil, err
		}
		// Bind the current payer's override after model selection because inherited
		// channel rows are assembled outside the cached credential's saved bindings.
		s.KeyRefreshFallbackUserGroupRPMOverride(ctx, selected, binding.GroupID)
		return selected, nil
	}
	return nil, apperror.BadRequest("MODEL_NOT_AVAILABLE", "No enabled channel supports this model and protocol.")
}

// DefaultGroupIDsForScope returns inherited channels that this payer can select.
// Team scope uses its owner for permissions. Returned identifiers preserve
// administrator priority independently of UI sorting and later subscription checks.
func (s *APIKeyService) DefaultGroupIDsForScope(ctx context.Context, userID int64, scope string) ([]int64, error) {
	groups, err := s.GetAvailableGroupsForScopeWithSubscription(ctx, userID, scope, nil)
	if err != nil {
		return nil, err
	}
	ids, err := s.RoutingDefaults(ctx)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(ids, func(id int64) bool {
		return !slices.ContainsFunc(groups, func(g routing.Group) bool { return g.ID == id })
	}), nil
}

// WithExcludedRoutingGroups attaches request-owned visited groups before model selection.
// Callers keep the map private to one serial request coordinator; credential
// snapshots never retain its state across different client requests.
func WithExcludedRoutingGroups(ctx context.Context, groups map[int64]bool) context.Context {
	return context.WithValue(ctx, routingGroupExclusionsKey{}, groups)
}

// RoutingCooldown returns the administrator's configured provider cooldown duration.
// Missing configuration uses sixty seconds; zero disables cross-request cooling.
// Invalid stored values reject routing because their intended policy is unknown.
func (s *APIKeyService) RoutingCooldown(ctx context.Context) (time.Duration, error) {
	if s.cfg == nil || s.cfg.RoutingSettings == nil {
		return time.Minute, nil
	}
	values, err := s.cfg.RoutingSettings.GetMultiple(ctx, []string{"api_key_routing_cooldown_seconds"})
	if err != nil {
		return 0, err
	}
	raw := values["api_key_routing_cooldown_seconds"]
	if raw == "" {
		return time.Minute, nil
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds < 0 || seconds > 3600 {
		return 0, apperror.BadRequest("INVALID_ROUTING_COOLDOWN", "Routing cooldown must be between 0 and 3600 seconds")
	}
	return time.Duration(seconds) * time.Second, nil
}

// SaveRoutingCooldown validates administrator cooldown and persists its seconds value.
func (s *APIKeyService) SaveRoutingCooldown(ctx context.Context, seconds int) error {
	if seconds < 0 || seconds > 3600 {
		return apperror.BadRequest("INVALID_ROUTING_COOLDOWN", "Routing cooldown must be between 0 and 3600 seconds")
	}
	return s.cfg.RoutingSettings.Set(ctx, "api_key_routing_cooldown_seconds", strconv.Itoa(seconds))
}
