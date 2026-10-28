package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/ops/monitor"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
)

// monitor.go exposes passive channel summaries, matrices, and configuration.
// The surrounding router supplies authenticated user or administrator access.
// User queries intersect every group filter with current available groups;
// administrator queries retain diagnostics while ordinary responses hide fleet
// volumes and other users' identities before returning the response envelope.
//
// Read handlers forward cancellation to the database and never call a provider
// test or upstream usage endpoint. Configuration uses optimistic version checks
// so simultaneous editors receive a conflict instead of replacing each other's
// changes. Shared handlers determine administrator access from authenticated
// identity, while route registration keeps configuration writes administrator-only.

// ChannelMonitorV2Handler represents passive monitor handler data used by passive channel queries.
type ChannelMonitorV2Handler struct {
	service         *monitor.ChannelMonitorV2Service              // stored service value
	availableGroups func(context.Context, int64) ([]int64, error) // stored available groups value
}

// NewChannelMonitorV2Handler retains the passive service and permission reader.
// Callers mount user reads behind JWT authentication and configuration writes
// behind administrator authentication; permission reads use the current request.
func NewChannelMonitorV2Handler(svc *monitor.ChannelMonitorV2Service, availableGroups func(context.Context, int64) ([]int64, error)) *ChannelMonitorV2Handler {
	return &ChannelMonitorV2Handler{service: svc, availableGroups: availableGroups}
}

// channelMonitorV2IsAdmin is true when the request already passed admin auth
// (shared Dimensions/Errors handlers serve both user and admin route groups).
func channelMonitorV2IsAdmin(c *gin.Context) bool {
	role, ok := authctx.GetUserRoleFromContext(c)
	return ok && role == "admin"
}

// GetConfig returns versioned monitor settings through the protected
// administrator route. Configuration remains readable when monitoring is
// disabled so the administrator can enable it or adjust its local display and
// scoring settings.
func (h *ChannelMonitorV2Handler) GetConfig(c *gin.Context) {
	cfg, err := h.service.GetConfig(c.Request.Context())
	if err != nil {
		writeChannelMonitorError(c, err)
		return
	}
	response.Success(c, cfg)
}

// UpdateConfig decodes and replaces monitor configuration for the authenticated
// administrator. Invalid input returns bad request, stale versions return
// conflict, and storage errors use the shared application mapping; callers
// cannot bypass administrator route middleware.
func (h *ChannelMonitorV2Handler) UpdateConfig(c *gin.Context) {
	var input monitor.ChannelMonitorV2Config
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "invalid channel monitor v2 config")
		return
	}
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "user not found in context")
		return
	}
	updated, err := h.service.UpdateConfig(c.Request.Context(), input, input.Version, subject.UserID)
	if err != nil {
		if errors.Is(err, monitor.ErrChannelMonitorV2ConfigConflict) {
			response.Error(c, http.StatusConflict, err.Error())
			return
		}
		if errors.Is(err, monitor.ErrChannelMonitorV2InvalidConfig) {
			response.BadRequest(c, err.Error())
			return
		}
		writeChannelMonitorError(c, err)
		return
	}
	response.Success(c, updated)
}

// Dimensions returns authorized filter choices and removes ordinary-viewer
// volume counts. Permission lookup runs before repository access, while
// administrator identity retains complete configured scope and diagnostic counts
// through the protected management route.
func (h *ChannelMonitorV2Handler) Dimensions(c *gin.Context) {
	filter, ok := h.parseFilter(c)
	if !ok {
		return
	}
	admin := channelMonitorV2IsAdmin(c)
	if !h.scopeFilter(c, &filter, admin) {
		return
	}
	result, err := h.service.Dimensions(c.Request.Context(), filter)
	if err != nil {
		writeChannelMonitorError(c, err)
		return
	}
	// Admin and user share this handler; only non-admin responses strip volume.
	if !admin {
		monitor.RedactChannelMonitorV2Dimensions(result)
	}
	response.Success(c, result)
}

// Snapshot serves a passive summary with ordinary-viewer scoping and redaction.
func (h *ChannelMonitorV2Handler) Snapshot(c *gin.Context) { h.snapshot(c, false) }

// AdminSnapshot serves a passive summary through the protected administrator
// route.
func (h *ChannelMonitorV2Handler) AdminSnapshot(c *gin.Context) { h.snapshot(c, true) }

// Models serves model statistics with ordinary-viewer scoping and volume
// redaction.
func (h *ChannelMonitorV2Handler) Models(c *gin.Context) { h.models(c, false) }

// AdminModels serves model statistics through the protected administrator route.
func (h *ChannelMonitorV2Handler) AdminModels(c *gin.Context) { h.models(c, true) }

// Matrix serves fixed history buckets with ordinary-viewer scoping and
// redaction.
func (h *ChannelMonitorV2Handler) Matrix(c *gin.Context) { h.matrix(c, false) }

// AdminMatrix serves fixed history buckets through the protected administrator
// route.
func (h *ChannelMonitorV2Handler) AdminMatrix(c *gin.Context) { h.matrix(c, true) }

// Users serves anonymized rankings with the authenticated viewer identified
// separately.
func (h *ChannelMonitorV2Handler) Users(c *gin.Context) { h.users(c, false) }

// AdminUsers serves account rankings through the protected administrator
// monitoring route.
func (h *ChannelMonitorV2Handler) AdminUsers(c *gin.Context) { h.users(c, true) }

// snapshot parses a bounded product window and applies server-derived group
// permissions. The selected service path controls ordinary-viewer redaction;
// storage and configuration failures return through the common monitor error
// mapper.
func (h *ChannelMonitorV2Handler) snapshot(c *gin.Context, admin bool) {
	filter, ok := h.parseFilter(c)
	if !ok {
		return
	}
	if !h.scopeFilter(c, &filter, admin) {
		return
	}
	result, err := h.service.Snapshot(c.Request.Context(), filter, admin)
	if err != nil {
		writeChannelMonitorError(c, err)
		return
	}
	response.Success(c, result)
}

// models parses model scope and applies current authenticated group permissions.
// The service loads passive facts and handles viewer redaction before this
// adapter serializes a successful JSON response or a mapped failure.
func (h *ChannelMonitorV2Handler) models(c *gin.Context, admin bool) {
	filter, ok := h.parseFilter(c)
	if !ok {
		return
	}
	if !h.scopeFilter(c, &filter, admin) {
		return
	}
	result, err := h.service.Models(c.Request.Context(), filter, admin)
	if err != nil {
		writeChannelMonitorError(c, err)
		return
	}
	response.Success(c, result)
}

// matrix validates grouping and time scope before reading passive history.
// Ordinary viewers receive server-derived group restrictions and redacted
// metrics; invalid grouping returns bad request before repository queries are
// attempted.
func (h *ChannelMonitorV2Handler) matrix(c *gin.Context, admin bool) {
	filter, ok := h.parseFilter(c)
	if !ok {
		return
	}
	groupBy, err := monitor.ParseChannelMonitorV2GroupBy(c.Query("group_by"))
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if !h.scopeFilter(c, &filter, admin) {
		return
	}
	result, err := h.service.Matrix(c.Request.Context(), filter, groupBy, admin)
	if err != nil {
		writeChannelMonitorError(c, err)
		return
	}
	response.Success(c, result)
}

// Errors returns categorized failures with administrator-only diagnostic
// samples. Current authenticated group permissions apply to every ordinary
// request, and the service removes counts and details before those results are
// serialized.
func (h *ChannelMonitorV2Handler) Errors(c *gin.Context) {
	filter, ok := h.parseFilter(c)
	if !ok {
		return
	}
	admin := channelMonitorV2IsAdmin(c)
	if !h.scopeFilter(c, &filter, admin) {
		return
	}
	result, err := h.service.ErrorsForViewer(c.Request.Context(), filter, admin)
	if err != nil {
		writeChannelMonitorError(c, err)
		return
	}
	response.Success(c, result)
}

// users loads ranked metrics under the authenticated identity and group scope. A
// missing subject returns unauthorized; the service retains the current viewer
// while anonymizing other identities for ordinary queries.
func (h *ChannelMonitorV2Handler) users(c *gin.Context, admin bool) {
	filter, ok := h.parseFilter(c)
	if !ok {
		return
	}
	subject, exists := authctx.GetAuthSubjectFromContext(c)
	if !exists {
		response.Error(c, http.StatusUnauthorized, "user not found in context")
		return
	}
	if !h.scopeFilter(c, &filter, admin) {
		return
	}
	result, err := h.service.Users(c.Request.Context(), filter, subject.UserID, admin)
	if err != nil {
		writeChannelMonitorError(c, err)
		return
	}
	response.Success(c, result)
}

// scopeFilter applies the authenticated user's server-derived group scope to
// every read endpoint. Administrators retain the complete configured scope;
// ordinary viewers fail closed when the authorization dependency is absent.
func (h *ChannelMonitorV2Handler) scopeFilter(c *gin.Context, filter *monitor.ChannelMonitorV2Filter, admin bool) bool {
	if admin {
		return true
	}
	if h.availableGroups == nil {
		response.Error(c, http.StatusInternalServerError, "channel monitor group authorization unavailable")
		return false
	}
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "user not found in context")
		return false
	}
	groups, err := h.availableGroups(c.Request.Context(), subject.UserID)
	if err != nil {
		writeChannelMonitorError(c, err)
		return false
	}
	filter.RestrictGroups = true
	filter.AllowedGroupIDs = make([]int64, 0, len(groups))
	filter.AllowedGroupIDs = append(filter.AllowedGroupIDs, groups...)
	return true
}

// parseFilter parses repeated platform, model, and positive group identifiers
// into a bounded window. Invalid group values or unsupported range labels write
// bad request and return false before permission or database queries.
func (h *ChannelMonitorV2Handler) parseFilter(c *gin.Context) (monitor.ChannelMonitorV2Filter, bool) {
	groups, err := parseChannelMonitorV2GroupIDs(monitorQueryList(c, "group_id"))
	if err != nil {
		response.BadRequest(c, "invalid group_id")
		return monitor.ChannelMonitorV2Filter{}, false
	}
	filter, err := h.service.ParseFilter(c.Query("range"), monitorQueryList(c, "platform"), monitorQueryList(c, "model"), groups)
	if err != nil {
		response.BadRequest(c, err.Error())
		return monitor.ChannelMonitorV2Filter{}, false
	}
	return filter, true
}

// monitorQueryList collects repeated and comma-separated query values while
// dropping empty entries.
func monitorQueryList(c *gin.Context, key string) []string {
	values := c.QueryArray(key)
	result := make([]string, 0, len(values))
	for _, value := range values {
		for part := range strings.SplitSeq(value, ",") {
			if part = strings.TrimSpace(part); part != "" {
				result = append(result, part)
			}
		}
	}
	return result
}

// parseChannelMonitorV2GroupIDs parses positive decimal group identifiers for
// the scope filter. Any malformed or nonpositive entry rejects the list, keeping
// invalid input distinguishable from an unrestricted empty administrator filter.
func parseChannelMonitorV2GroupIDs(values []string) ([]int64, error) {
	result := make([]int64, 0, len(values))
	for _, value := range values {
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil || id <= 0 {
			return nil, errors.New("invalid group id")
		}
		result = append(result, id)
	}
	return result, nil
}

// writeChannelMonitorError maps disabled monitoring to a readable unavailable
// response. Other failures use the common application error mapping so database
// causes are logged and private details remain outside ordinary error messages.
func writeChannelMonitorError(c *gin.Context, err error) {
	if errors.Is(err, monitor.ErrChannelMonitorDisabled) {
		response.Error(c, http.StatusServiceUnavailable, "channel monitoring is disabled")
		return
	}
	response.ErrorFrom(c, err)
}
