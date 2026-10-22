package httpapi

import "github.com/gin-gonic/gin"

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
func RegisterAdminRoutes(parent *gin.RouterGroup, h *IntelligenceHandler) {
	g := parent.Group("/intelligence-tests")
	g.GET("", h.AdminList)
	g.POST("", h.AdminSave)
	g.GET("/runs/:id", h.AdminDetail)
	g.GET("/runs/:id/preview", h.AdminPreview)
	g.GET("/runs/:id/thumbnail", h.AdminThumbnail)
	g.PUT("/:id", h.AdminSave)
	g.DELETE("/:id", h.AdminDelete)
	g.POST("/:id/run", h.AdminRun)
	g.GET("/:id/runs", h.AdminRuns)
}
