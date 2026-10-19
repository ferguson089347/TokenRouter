//go:build wireinject

package app

import (
	"github.com/google/wire"

	billinghttp "github.com/TokenFlux/TokenRouter/internal/billing/httpapi"
	opshttp "github.com/TokenFlux/TokenRouter/internal/ops/httpapi"
	poolhttp "github.com/TokenFlux/TokenRouter/internal/remotepool/httpapi"
	sitehttp "github.com/TokenFlux/TokenRouter/internal/site/httpapi"
)

// http_modules_wire.go registers billing, announcement, Ops, and remote pool
// HTTP dependencies with Wire. Announcement handlers consume
// AnnouncementService, and subscription and redeem handlers consume their
// billing use cases. These HTTP constructors provide concrete handler types to
// application route mounting, alongside the Ops handler constructed from
// OpsService for operational queries.

// nativeHTTPProviders supplies billing, announcement, Ops, and remote pool handlers
// with their application-assembled service dependencies for panel route registration.
var nativeHTTPProviders = wire.NewSet(
	provideRemotePool,
	poolhttp.NewRemoteAccountPoolHandler,
	billinghttp.NewPlanHandler,
	billinghttp.NewRedeemHandler,
	billinghttp.NewSubscriptionHandler,
	billinghttp.NewAdminRedeemHandler,
	billinghttp.NewAdminSubscriptionHandler,
	sitehttp.NewAnnouncementHandler,
	sitehttp.NewAdminAnnouncementHandler,
	opshttp.NewOpsHandler,
)
