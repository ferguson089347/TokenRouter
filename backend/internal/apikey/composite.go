package apikey

import (
	"context"
	"regexp"
	"sort"
	"strings"
)

// composite.go validates ordered channel bindings and their internal row labels.
// The persistent relationship records a group identifier, a unique label and a
// sort position for each channel. User-facing routing supplies generated labels
// from group identifiers, and the gateway uses complete model names to select
// channels through the eligibility rules in routing.go.
//
// Structural validation rejects empty or excessive lists, duplicate groups and
// conflicting normalized labels before group permission lookup begins. Preparing
// bindings loads their groups and keeps the submitted order for persistence and
// model enumeration. Runtime scope checks require a channel membership for user
// Keys, while fixed server execution views separately validate their selected
// task group and payer permission.

const (
	// Composite configuration bounds constrain serialized authentication data
	// and accepted prefix syntax before group permission or model-list work.
	MaxCompositeAPIKeyGroups = 20 // maximum selectable group bindings
	MaxCompositeKeyPrefixLen = 32 // maximum ASCII prefix bytes
)

// KeyCompositeKeyPrefixPattern defines ASCII prefix characters accepted for
// persisted channel row labels.
var KeyCompositeKeyPrefixPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// APIKeyCompositeGroupInput supplies a target group and its internal storage
// label.
type APIKeyCompositeGroupInput struct {
	GroupID int64  `json:"group_id"` // requested target group identifier
	Prefix  string `json:"prefix"`   // internal channel row label
}

// AllowsRuntimeGroup reports whether a positive target ID fits this Key's
// binding scope. Nil keys reject selection; ordinary keys accept positive IDs,
// and composite keys require a matching saved group before separate payer
// checks.
func (k *APIKey) AllowsRuntimeGroup(groupID int64) bool {
	if k == nil || groupID <= 0 {
		return false
	}
	if !k.IsComposite {
		return true
	}
	for _, binding := range k.CompositeGroups {
		if binding.GroupID == groupID {
			return true
		}
	}
	return false
}

// NormalizeCompositeKeyPrefix trims prefix and returns display and lowercase
// lookup text when valid. Empty, oversized, or non-ASCII-prefix input returns
// empty results with false; callers use the normalized result for duplicate
// checks and lookup.
func NormalizeCompositeKeyPrefix(prefix string) (display string, normalized string, ok bool) {
	display = strings.TrimSpace(prefix)
	if display == "" || len(display) > MaxCompositeKeyPrefixLen || !KeyCompositeKeyPrefixPattern.MatchString(display) {
		return "", "", false
	}
	return display, strings.ToLower(display), true
}

// KeyValidateCompositeGroupInputs validates count, positive group IDs, and
// normalized prefix uniqueness for the complete list. It returns new binding
// records in submitted order with SortOrder indices; empty input is rejected,
// and group permission is checked by KeyPrepareCompositeGroups afterward.
func KeyValidateCompositeGroupInputs(inputs []APIKeyCompositeGroupInput) ([]APIKeyCompositeGroup, error) {
	if len(inputs) == 0 {
		return nil, ErrCompositeKeyGroupsRequired
	}
	if len(inputs) > MaxCompositeAPIKeyGroups {
		return nil, ErrCompositeKeyTooManyGroups
	}
	groups := make([]APIKeyCompositeGroup, 0, len(inputs))
	seenGroups := make(map[int64]struct{}, len(inputs))
	seenPrefixes := make(map[string]struct{}, len(inputs))
	for index, input := range inputs {
		if input.GroupID <= 0 {
			return nil, ErrGroupNotAllowed
		}
		if _, exists := seenGroups[input.GroupID]; exists {
			return nil, ErrCompositeKeyGroupDuplicate
		}
		display, normalized, ok := NormalizeCompositeKeyPrefix(input.Prefix)
		if !ok {
			return nil, ErrCompositeKeyPrefixInvalid
		}
		if _, exists := seenPrefixes[normalized]; exists {
			return nil, ErrCompositeKeyPrefixDuplicate
		}
		seenGroups[input.GroupID] = struct{}{}
		seenPrefixes[normalized] = struct{}{}
		groups = append(groups, APIKeyCompositeGroup{
			GroupID:          input.GroupID,
			Prefix:           display,
			NormalizedPrefix: normalized,
			SortOrder:        index,
		})
	}
	return groups, nil
}

// KeyPrepareCompositeGroups validates binding input and loads each target group
// for the supplied payer. A failed lookup or binding permission rejects the
// whole result; successful records carry the loaded group pointers and retain
// submitted order.
func (s *APIKeyService) KeyPrepareCompositeGroups(
	ctx context.Context,
	user *User,
	inputs []APIKeyCompositeGroupInput,
) ([]APIKeyCompositeGroup, error) {
	bindings, err := KeyValidateCompositeGroupInputs(inputs)
	if err != nil {
		return nil, err
	}
	for i := range bindings {
		binding := &bindings[i]
		group, err := s.groupRepo.GetByID(ctx, binding.GroupID)
		if err != nil {
			return nil, err
		}
		if !s.KeyCanUserBindGroup(ctx, user, group) {
			return nil, ErrGroupNotAllowed
		}
		binding.Group = group
	}
	sort.SliceStable(bindings, func(i, j int) bool { return bindings[i].SortOrder < bindings[j].SortOrder })
	return bindings, nil
}

// KeyCloneCompositeBindings copies the binding slice while retaining group and
// RPM override pointers. Nil and empty inputs both return nil; callers treat
// referenced group and override data as shared after this copy.
func KeyCloneCompositeBindings(bindings []APIKeyCompositeGroup) []APIKeyCompositeGroup {
	if len(bindings) == 0 {
		return nil
	}
	out := make([]APIKeyCompositeGroup, len(bindings))
	copy(out, bindings)
	return out
}
