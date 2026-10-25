package settings_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/settings/composite"
	settingskit "github.com/TokenFlux/TokenRouter/internal/settings/testkit"
	"github.com/TokenFlux/TokenRouter/internal/site"
)

// client_provider_test.go exercises the client namespace across composite settings
// saves, administrator reads, and public browser settings. A map-backed repository
// supplies the same stored value to each module, exposing missing field transfers
// in the combined settings implementation.

// clientProviderRepo adds public batch reads to the shared settings save fixture.
type clientProviderRepo struct {
	settingUpdateRepoStub // captured settings write fixture
}

// GetMultiple returns owned copies of requested values for public settings reads.
// Tests serialize writes and reads; absent keys remain absent so the public
// service can apply the same defaults used with real repository responses.
func (r *clientProviderRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	values := make(map[string]string)
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			values[key] = value
		}
	}
	return values, nil
}

// TestClientProviderIDRoundTrip saves a custom namespace through the combined
// settings service. Administrator and public readers receive that normalized
// namespace, and invalid replacements leave the previously captured write intact.
func TestClientProviderIDRoundTrip(t *testing.T) {
	repo := &clientProviderRepo{}
	svc := settingskit.NewComposite(repo, nil)
	err := svc.Save(t.Context(), &composite.Snapshot{ClientProviderID: "  my_gateway-2  "})
	require.NoError(t, err)
	require.Equal(t, "my_gateway-2", repo.updates[site.SettingKeyClientProviderID])
	admin, err := svc.Runtime.GetAllSettings(t.Context())
	require.NoError(t, err)
	require.Equal(t, "my_gateway-2", admin.ClientProviderID)
	public, err := svc.Public.GetPublicSettings(t.Context())
	require.NoError(t, err)
	require.Equal(t, "my_gateway-2", public.ClientProviderID)
	err = svc.Save(t.Context(), &composite.Snapshot{ClientProviderID: "invalid/model"})
	require.Error(t, err)
	require.Equal(t, "my_gateway-2", repo.values[site.SettingKeyClientProviderID])
}
