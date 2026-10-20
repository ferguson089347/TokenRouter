package intelligence

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// settings.go prepares the administrator switch controlling model quality checks.
// The feature requires an affirmative stored value before users can read results
// or administrators can submit requests; submitted remote jobs remain recoverable.

// SettingKeyEnabled stores the global availability switch read before new submissions and user result access.
const SettingKeyEnabled = "intelligence_enabled"

// SettingsParticipant declares the quality-check switch for atomic administrator settings updates.
// Omitted input produces no write; malformed JSON fails preparation before the
// shared settings owner persists any fields from the request.
func SettingsParticipant() settings.Participant {
	return settings.Participant{Module: "intelligence", Fields: []string{SettingKeyEnabled}, Keys: []string{SettingKeyEnabled}, Prepare: func(_ context.Context, input settings.Fields, _ map[string]string) (settings.PreparedChange, error) {
		raw, ok := input[SettingKeyEnabled]
		if !ok {
			return settings.PreparedChange{}, nil
		}
		var enabled bool
		if err := json.Unmarshal(raw, &enabled); err != nil {
			return settings.PreparedChange{}, err
		}
		return settings.PreparedChange{Values: map[string]string{SettingKeyEnabled: strconv.FormatBool(enabled)}}, nil
	}}
}
