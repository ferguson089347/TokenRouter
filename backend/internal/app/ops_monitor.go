package app

import (
	"context"
	"database/sql"
	"log"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/ops/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/ops/monitor"
	"github.com/TokenFlux/TokenRouter/internal/ops/postgres"
)

// ops_monitor.go assembles passive channel monitoring from application storage
// and current user group permissions. One PostgreSQL advisory lock coordinates
// the background aggregator across instances, and the lifecycle waits for its
// SQL and lock cleanup before shared database shutdown.

// provideChannelMonitor binds the passive repository, permission reader, and
// bounded worker lifetime. Construction performs no upstream requests; startup
// honors the deployment Ops switch and configuration controls later iterations.
func provideChannelMonitor(db *sql.DB, keys *apikey.APIKeyService, manager *lifecycle.Manager, cfg *config.Config) *httpapi.ChannelMonitorV2Handler {
	repo := postgres.NewChannelMonitorV2Repository(db)
	service := monitor.NewChannelMonitorV2Service(repo)
	lock := postgres.NewAdvisory(db)
	aggregator := monitor.NewAggregator(repo, func(ctx context.Context) (func(), bool) {
		return lock.Acquire(ctx, "channel-monitor-v2-aggregator")
	}, log.Printf)
	// Register shutdown with the producer stage so cancellation and database
	// transactions finish before later lifecycle hooks close shared storage.
	manager.Register(lifecycle.Hook{
		Name: "PassiveChannelMonitor", StartOrder: 980, StopOrder: shutdownStageProducers,
		Start: func(context.Context) error {
			if cfg.Ops.Enabled {
				aggregator.Start()
			}
			return nil
		},
		Stop: aggregator.Stop,
	})
	return httpapi.NewChannelMonitorV2Handler(service, func(ctx context.Context, userID int64) ([]int64, error) {
		groups, err := keys.GetAvailableGroups(ctx, userID)
		if err != nil {
			return nil, err
		}
		ids := make([]int64, 0, len(groups))
		for _, group := range groups {
			ids = append(ids, group.ID)
		}
		return ids, nil
	})
}
