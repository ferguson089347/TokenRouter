package httpapi

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/remotepool"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
)

// handler.go exposes remote pool summaries to authenticated users and configuration
// to administrators. User reads obtain personal group access before the service
// applies current local bindings, so account counts and filters share the same
// visibility rules as the returned records.

// RemoteAccountPoolHandler serves remote summaries and administrator session
// settings.
type RemoteAccountPoolHandler struct {
	service *remotepool.RemoteAccountPoolService // pool session and snapshot service
	apiKeys *apikey.APIKeyService                // personal group permission reader
}

// NewRemoteAccountPoolHandler constructs pool endpoints using the shared
// group-access service.
func NewRemoteAccountPoolHandler(s *remotepool.RemoteAccountPoolService, apiKeys *apikey.APIKeyService) *RemoteAccountPoolHandler {
	return &RemoteAccountPoolHandler{service: s, apiKeys: apiKeys}
}

// GetSettings returns administrator configuration with remote credentials
// omitted from the response.
func (h *RemoteAccountPoolHandler) GetSettings(c *gin.Context) {
	settings, err := h.service.GetSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}

// @project-doc docs/interfaces/remote_account_pool.md#configuration_security
// UpdateSettings validates a bounded configuration body and saves
// administrator settings.
// Callers mount this handler behind administrator authentication and audit
// middleware.
// The audit recorder omits this body because it carries a refresh token.
func (h *RemoteAccountPoolHandler) UpdateSettings(c *gin.Context) {
	var in remotepool.RemotePoolSettingsInput
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "Invalid remote pool settings")
		return
	}
	settings, err := h.service.UpdateSettings(c.Request.Context(), in)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}

// @project-doc docs/interfaces/remote_account_pool.md#group_projection
// List returns a validated, filtered snapshot page for the authenticated caller.
// Group filters use local IDs, and personal group access is checked before account
// filtering and counting. Source synchronization has its own bounded
// persistence budget.
func (h *RemoteAccountPoolHandler) List(c *gin.Context) {
	query := url.Values{}
	// Accept known display filters so unrelated local query parameters cannot
	// influence source requests or carry local credentials remotely.
	for _, key := range []string{"search", "plan_type", "status"} {
		value := strings.TrimSpace(c.Query(key))
		if len(value) > 200 {
			response.BadRequest(c, "Remote pool filter is too long")
			return
		}
		if value != "" {
			query.Set(key, value)
		}
	}
	for _, param := range []struct {
		key      string // accepted numeric filter name
		fallback int    // omitted parameter default value
		min      int    // smallest accepted parameter value
		max      int    // largest accepted parameter value
	}{
		{"page", 1, 1, 1000000}, {"page_size", 20, 1, 500}, {"group_id", 0, 1, 1000000000},
	} {
		value := param.fallback
		if raw := c.Query(param.key); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < param.min || parsed > param.max {
				response.BadRequest(c, "Invalid pagination or group filter")
				return
			}
			value = parsed
		}
		if value != 0 {
			query.Set(param.key, strconv.Itoa(value))
		}
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 65*time.Second)
	defer cancel()
	allowed, ok := h.allowedGroups(c)
	if !ok {
		return
	}
	page, err := h.service.List(ctx, query, allowed)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, page)
}

// Options returns visible local group and subscription-tier choices for pool filters.
func (h *RemoteAccountPoolHandler) Options(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 65*time.Second)
	defer cancel()
	allowed, ok := h.allowedGroups(c)
	if !ok {
		return
	}
	options, err := h.service.Options(ctx, allowed)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, options)
}

// allowedGroups resolves personal group permissions for the authenticated pool
// caller.
// Administrators receive nil to request all enabled bound groups. Ordinary users
// receive a nonnil slice, including an empty slice when no group is available.
func (h *RemoteAccountPoolHandler) allowedGroups(c *gin.Context) ([]routing.Group, bool) {
	if role, ok := authctx.GetUserRoleFromContext(c); ok && role == "admin" {
		return nil, true
	}
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "Authentication required")
		return nil, false
	}
	groups, err := h.apiKeys.GetAvailableGroupsForScopeWithSubscription(c.Request.Context(), subject.UserID, "personal", nil)
	if err != nil {
		response.ErrorFrom(c, err)
		return nil, false
	}
	if groups == nil {
		groups = []routing.Group{}
	}
	return groups, true
}

// SourceGroups returns the source group directory for administrator binding
// configuration.
func (h *RemoteAccountPoolHandler) SourceGroups(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 65*time.Second)
	defer cancel()
	groups, err := h.service.SourceGroups(ctx)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, groups)
}

// RegisterUserRoutes mounts pool reads under an authenticated panel route group.
// The caller installs JWT authentication and panel limits before invoking
// registration;
// configuration and source-directory access are registered separately for
// administrators.
func RegisterUserRoutes(group *gin.RouterGroup, endpoint *RemoteAccountPoolHandler) {
	group.GET("/account-pool", endpoint.List)
	group.GET("/account-pool/options", endpoint.Options)
}

// RegisterAdminRoutes mounts source settings and binding choices under the
// administrator group.
// The caller installs administrator authentication, panel limits, and auditing before
// registration. Settings request bodies are omitted by the shared audit recorder.
func RegisterAdminRoutes(group *gin.RouterGroup, endpoint *RemoteAccountPoolHandler) {
	pool := group.Group("/remote-account-pool")
	pool.GET("/settings", endpoint.GetSettings)
	pool.PUT("/settings", endpoint.UpdateSettings)
	pool.GET("/source-groups", endpoint.SourceGroups)
}
