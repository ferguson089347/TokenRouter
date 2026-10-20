package app

import (
	"database/sql"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/remotepool"
	"github.com/TokenFlux/TokenRouter/internal/remotepool/postgres"
	"github.com/TokenFlux/TokenRouter/internal/remotepool/provider"
	routingpostgres "github.com/TokenFlux/TokenRouter/internal/routing/postgres"
)

// remote_pool.go constructs the remote account directory using shared application
// resources. Local routing groups supply display names, rates, and enabled state,
// while the settings row stores encrypted remote credentials and complete snapshots
// under the configured fixed encryption key. The stable JWT signing secret
// also derives site-local display references from source account identities.

// provideRemotePool constructs remote pool synchronization from shared
// database and groups.
// The application owns these resources. Saved credentials require the fixed key
// recorded in cfg; the service bounds remote requests and serialized snapshot
// updates. Public references use the stable JWT secret supplied by cfg.
func provideRemotePool(db *sql.DB, cipher identity.SecretEncryptor, cfg *config.Config, groups *routingpostgres.GroupStore) *remotepool.RemoteAccountPoolService {
	return remotepool.NewRemoteAccountPoolService(
		postgres.NewRepository(db),
		cipher,
		cfg.Totp.EncryptionKeyConfigured,
		groups,
		provider.NewClient(),
		cfg.JWT.Secret,
	)
}
