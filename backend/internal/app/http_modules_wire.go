//go:build wireinject

package app

import (
	"github.com/google/wire"

	billinghttp "github.com/TokenFlux/TokenRouter/internal/billing/httpapi"
	opshttp "github.com/TokenFlux/TokenRouter/internal/ops/httpapi"
	poolhttp "github.com/TokenFlux/TokenRouter/internal/remotepool/httpapi"
	sitehttp "github.com/TokenFlux/TokenRouter/internal/site/httpapi"
	tickethttp "github.com/TokenFlux/TokenRouter/internal/ticket/httpapi"
)

// http_modules_wire.go registers billing, announcement, Ops, remote pool, and
// ticket HTTP dependencies with Wire. Announcement handlers consume
// AnnouncementService, and subscription and redeem handlers consume their
// billing use cases. These HTTP constructors provide concrete handler types to
// application route mounting, alongside the Ops handler constructed from
// OpsService for operational queries.

// nativeHTTPProviders supplies panel handlers and ticket runtime dependencies
// so route registration receives services assembled from the shared application resources.
var nativeHTTPProviders = wire.NewSet(
	provideRemotePool,
	provideIntelligence,
	provideTicketConfig,
	provideTickets,
	provideTicketRuntime,
	tickethttp.NewTicketHandler,
	poolhttp.NewRemoteAccountPoolHandler,
	billinghttp.NewPlanHandler,
	billinghttp.NewRedeemHandler,
	billinghttp.NewSubscriptionHandler,
	billinghttp.NewAdminRedeemHandler,
	billinghttp.NewAdminSubscriptionHandler,
	sitehttp.NewAnnouncementHandler,
	sitehttp.NewAdminAnnouncementHandler,
	opshttp.NewOpsHandler,
	provideChannelMonitor,
)
