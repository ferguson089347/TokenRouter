package app

import (
	"context"
	"database/sql"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/config"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/intelligence"
	intelligencehttp "github.com/TokenFlux/TokenRouter/internal/intelligence/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/intelligence/postgres"
	"github.com/TokenFlux/TokenRouter/internal/intelligence/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// intelligence.go assembles quality checks using shared credentials, group access,
// settings, and SQL resources. The service registers workers with application
// lifecycle so polling finishes before shared stores close, and preview tickets
// use the configured JWT secret across restarts and application instances.

// provideIntelligence constructs quality-check endpoints and registers the owned workers.
// The global switch requires the stored string true; read failures disable new
// work. Lifecycle cancellation drains workers before database resources close.
func provideIntelligence(db *sql.DB, keys apikey.APIKeyRepository, groups routing.GroupRepository, available *apikey.APIKeyService, users *identitypostgres.UserStore, store *settings.Store, cfg *config.Config, manager *lifecycle.Manager) *intelligencehttp.IntelligenceHandler {
	enabled := func(ctx context.Context) bool {
		value, err := store.GetValue(ctx, intelligence.SettingKeyEnabled)
		return err == nil && value == "true"
	}
	service := intelligence.NewIntelligenceService(
		postgres.NewIntelligenceRepository(db),
		keys,
		groups,
		available,
		provider.NewClient(nil),
		enabled,
		httpclient.ValidateResolvedIP,
		provider.NewThumbnailRenderer(),
	)
	manager.Register(lifecycle.Hook{
		Name:       "IntelligenceTests",
		StartOrder: 980,
		StopOrder:  20,
		Start: func(context.Context) error {
			service.Start()
			return nil
		},
		Stop: func(context.Context) error {
			service.Stop()
			return nil
		},
	})
	return intelligencehttp.ProvideIntelligenceHandler(service, cfg.JWT.Secret, users, enabled)
}
