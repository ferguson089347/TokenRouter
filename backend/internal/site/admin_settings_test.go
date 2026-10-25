package site

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// admin_settings_test.go checks client provider namespaces through site settings
// preparation and administrator reads. Registry cases distinguish omitted fields
// from explicit resets, while format cases protect valid identifiers used by
// both Codex and OpenCode exports.

// TestClientProviderIDPreparation covers accepted namespaces, explicit resets,
// and invalid client references. Invalid input returns no prepared writes, and
// accepted input normalizes to the same value returned to administrators.
func TestClientProviderIDPreparation(t *testing.T) {
	for _, tc := range []struct {
		name  string // named input scenario
		input string // submitted client namespace
		want  string // normalized stored namespace
	}{
		{"custom", "my_gateway-2", "my_gateway-2"},
		{"trimmed", "  mygateway  ", "mygateway"},
		{"empty", "", "tokenrouter"},
		{"whitespace", " \t", "tokenrouter"},
		{"maximum length", strings.Repeat("a", 64), strings.Repeat("a", 64)},
		{"uppercase", "MyGateway", ""},
		{"digit first", "1gateway", ""},
		{"separator", "gateway/model", ""},
		{"dot", "gateway.extra", ""},
		{"quote", "gateway\"", ""},
		{"newline", "gateway\nextra", ""},
		{"unicode", "网关", ""},
		{"too long", strings.Repeat("a", 65), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := &AdminSettings{ClientProviderID: tc.input}
			values, err := PrepareAdminSettings(input)
			if tc.want == "" {
				require.ErrorContains(t, err, "Client provider ID")
				require.Nil(t, values)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, values[SettingKeyClientProviderID])
			require.Equal(t, tc.want, ReadAdminSettings(values).ClientProviderID)
		})
	}
}

// TestClientProviderIDPartialUpdates prepares an unrelated site edit after a
// namespace has been saved. Omission retains that namespace, while a supplied
// empty string prepares the documented reset to tokenrouter.
func TestClientProviderIDPartialUpdates(t *testing.T) {
	registry, err := settings.NewRegistry(SettingsParticipant())
	require.NoError(t, err)
	current := map[string]string{SettingKeyClientProviderID: "mygateway"}
	changes, err := registry.Prepare(t.Context(), settings.Fields{
		SettingKeyAPIBaseURL: json.RawMessage(`"https://api.example.com"`),
	}, current)
	require.NoError(t, err)
	require.Len(t, changes, 1)
	require.NotContains(t, changes[0].Values, SettingKeyClientProviderID)
	changes, err = registry.Prepare(t.Context(), settings.Fields{
		SettingKeyClientProviderID: json.RawMessage(`""`),
	}, current)
	require.NoError(t, err)
	require.Equal(t, "tokenrouter", changes[0].Values[SettingKeyClientProviderID])
}
