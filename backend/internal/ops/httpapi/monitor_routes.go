package httpapi

import (
	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/ops/monitor"
	"github.com/TokenFlux/TokenRouter/internal/server/adminapi"
)

// monitor_routes.go mounts passive channel reads and versioned configuration.
// User routes inherit JWT authentication and obtain group scope from the
// handler's permission reader. Administrator routes use registered management
// operations so HTTP and MCP share permission identities and mutation metadata.

// ChannelMonitorOperations describes passive reads and the administrator-only
// configuration replacement. Configuration edits change local presentation and
// background aggregation settings; no operation dispatches a provider request.
var ChannelMonitorOperations = channelMonitorOperations()

// RegisterChannelMonitorUserRoutes mounts scoped reads on an authenticated user
// group. The caller installs JWT and panel limits before registration; configuration
// writes are available exclusively through the separate administrator registrar.
func RegisterChannelMonitorUserRoutes(group *gin.RouterGroup, h *ChannelMonitorV2Handler) {
	routes := group.Group("/channel-monitor/v2")
	routes.GET("/snapshot", h.Snapshot)
	routes.GET("/dimensions", h.Dimensions)
	routes.GET("/models", h.Models)
	routes.GET("/matrix", h.Matrix)
	routes.GET("/errors", h.Errors)
	routes.GET("/users", h.Users)
}

// RegisterChannelMonitorAdminRoutes mounts registered reads and configuration on
// a protected administrator group. Callers install administrator authentication,
// panel limits, and audit capture before invoking this registrar once per router.
func RegisterChannelMonitorAdminRoutes(group *gin.RouterGroup, h *ChannelMonitorV2Handler) {
	routes := group.Group("/channel-monitor/v2")
	adminapi.Register(routes, ChannelMonitorOperations["snapshot"], h.AdminSnapshot)
	adminapi.Register(routes, ChannelMonitorOperations["dimensions"], h.Dimensions)
	adminapi.Register(routes, ChannelMonitorOperations["models"], h.AdminModels)
	adminapi.Register(routes, ChannelMonitorOperations["matrix"], h.AdminMatrix)
	adminapi.Register(routes, ChannelMonitorOperations["errors"], h.Errors)
	adminapi.Register(routes, ChannelMonitorOperations["users"], h.AdminUsers)
	adminapi.Register(routes, ChannelMonitorOperations["config"], h.GetConfig)
	adminapi.Register(routes, ChannelMonitorOperations["update"], h.UpdateConfig)
}

// channelMonitorOperations builds fixed management descriptors for the passive
// monitor endpoints. Query parameters remain separate from the versioned JSON
// configuration body; only its PUT descriptor permits a state-changing dispatch.
func channelMonitorOperations() map[string]*adminapi.Operation {
	operations := make(map[string]*adminapi.Operation)
	for _, endpoint := range []string{"snapshot", "dimensions", "models", "matrix", "errors", "users", "config", "update"} {
		method := "GET"
		path := "/api/v1/admin/channel-monitor/v2/" + endpoint
		var body any
		if endpoint == "update" {
			method = "PUT"
			path = "/api/v1/admin/channel-monitor/v2/config"
			body = (*monitor.ChannelMonitorV2Config)(nil)
		}
		operations[endpoint] = adminapi.Define(adminapi.Operation{
			ID:     "ops.channel_monitor." + endpoint,
			Module: "ops", Title: "Channel monitor " + endpoint,
			Description: "Query passive channel metrics or replace versioned local monitor configuration.",
			Method:      method, Path: path, ReadOnly: method == "GET", Response: "json",
			BrowserPath: "/admin/channel-monitor",
			Input:       adminapi.Arguments(path, []string{"range", "platform", "group_id", "model", "group_by"}, body),
		})
	}
	return operations
}
