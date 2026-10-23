package httpapi

import (
	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/server/adminapi"
)

// routes.go mounts administrator quality-check commands, user submissions, and results.
// The application supplies authenticated panel groups, while preview content uses
// a short-lived signed ticket and independently rechecks the current user's access.

// RegisterUserRoutes mounts shared results and self-tests behind panel authentication.
// The application installs JWT, panel limits and audit middleware first; feature
// availability, Key ownership, and current group visibility are checked by the service.
// @project-doc docs/domains/intelligence_tests.md#intelligence_access
func RegisterUserRoutes(parent *gin.RouterGroup, h *IntelligenceHandler) {
	g := parent.Group("/intelligence-tests")
	g.GET("", h.UserList)
	g.POST("/run", h.UserRun)
	g.GET("/runs/:id", h.UserDetail)
	g.GET("/runs/:id/preview", h.UserPreview)
	g.GET("/runs/:id/thumbnail", h.UserThumbnail)
}

// RegisterAdminRoutes mounts configuration and manual checks behind administrator authentication.
// Configuration remains accessible while the feature is disabled, and the service
// rejects submissions until the stored global switch is enabled again.
// Each route also registers its browser continuation in the MCP operation catalogue.
func RegisterAdminRoutes(parent *gin.RouterGroup, h *IntelligenceHandler) {
	g := parent.Group("/intelligence-tests")
	adminapi.Register(g, ManagementOperations["intelligence.configs.list"], h.AdminList)
	adminapi.Register(g, ManagementOperations["intelligence.configs.create"], h.AdminSave)
	adminapi.Register(g, ManagementOperations["intelligence.runs.get"], h.AdminDetail)
	adminapi.Register(g, ManagementOperations["intelligence.runs.preview"], h.AdminPreview)
	adminapi.Register(g, ManagementOperations["intelligence.runs.thumbnail"], h.AdminThumbnail)
	adminapi.Register(g, ManagementOperations["intelligence.configs.update"], h.AdminSave)
	adminapi.Register(g, ManagementOperations["intelligence.configs.delete"], h.AdminDelete)
	adminapi.Register(g, ManagementOperations["intelligence.configs.run"], h.AdminRun)
	adminapi.Register(g, ManagementOperations["intelligence.runs.list"], h.AdminRuns)
}
