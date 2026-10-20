package intelligence

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// TestSettingsParticipantPresence distinguishes omitted, enabled and disabled
// updates. It protects partial saves from changing the stored feature switch and
// rejects malformed values before the shared transaction can persist settings.
func TestSettingsParticipantPresence(t *testing.T) {
	participant := SettingsParticipant()
	for _, tc := range []struct {
		name     string            // input scenario name
		input    settings.Fields   // submitted setting values
		expected map[string]string // prepared persistence values
		invalid  bool              // expected validation failure
	}{
		{name: "omitted", input: settings.Fields{}},
		{name: "enabled", input: settings.Fields{SettingKeyEnabled: json.RawMessage(`true`)}, expected: map[string]string{SettingKeyEnabled: "true"}},
		{name: "disabled", input: settings.Fields{SettingKeyEnabled: json.RawMessage(`false`)}, expected: map[string]string{SettingKeyEnabled: "false"}},
		{name: "invalid", input: settings.Fields{SettingKeyEnabled: json.RawMessage(`"yes"`)}, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prepared, err := participant.Prepare(context.Background(), tc.input, map[string]string{SettingKeyEnabled: "true"})
			if tc.invalid {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.expected, prepared.Values)
		})
	}
}
