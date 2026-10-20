package httpapi

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/intelligence"
	"github.com/TokenFlux/TokenRouter/internal/server/httpx"
)

// handler.go serves administrator quality-check commands and user result
// reads. Each endpoint verifies the current panel identity before invoking the
// service, and administrator commands also require the administrator role.
// Submitted configuration JSON is bounded before credential validation.

// IntelligenceHandler serves administrator commands, authorized user
// histories, and sandboxed drawing previews.
type IntelligenceHandler struct {
	svc     *intelligence.IntelligenceService // quality check service
	preview *intelligencePreviewAccess        // drawing ticket access
}

// NewIntelligenceHandler constructs panel endpoints around the quality-check
// service implementation.
func NewIntelligenceHandler(svc *intelligence.IntelligenceService) *IntelligenceHandler {
	return &IntelligenceHandler{svc: svc}
}

// intelligenceActor obtains the current panel user and checks the required
// role. Missing identities write an unauthorized response; administrator
// requests from ordinary users write forbidden before the service can access
// storage.
func intelligenceActor(c *gin.Context, admin bool) (int64, bool) {
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		httpx.Unauthorized(c, "User not authenticated")
		return 0, false
	}
	if admin {
		role, _ := authctx.GetUserRoleFromContext(c)
		if role != "admin" {
			httpx.Forbidden(c, "Administrator access required")
			return 0, false
		}
	}
	return subject.UserID, true
}

// intelligenceConfigID parses the positive decimal configuration ID from the
// route. Invalid or nonpositive input writes a bad-request response and
// returns false, letting handlers stop before accessing storage.
func intelligenceConfigID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.BadRequest(c, "Invalid intelligence test identifier")
		return 0, false
	}
	return id, true
}

// AdminList returns saved configurations after verifying the current
// administrator identity.
func (h *IntelligenceHandler) AdminList(c *gin.Context) {
	if _, ok := intelligenceActor(c, true); !ok {
		return
	}
	data, err := h.svc.AdminList(c.Request.Context())
	if !httpx.ErrorFrom(c, err) {
		httpx.Success(c, data)
	}
}

// AdminSave creates or edits a configuration after administrator and route
// validation. Request bodies are limited before JSON binding, and creation
// returns 201 while successful edits use the ordinary response envelope.
func (h *IntelligenceHandler) AdminSave(c *gin.Context) {
	if _, ok := intelligenceActor(c, true); !ok {
		return
	}
	var id int64
	if c.Param("id") != "" {
		var ok bool
		id, ok = intelligenceConfigID(c)
		if !ok {
			return
		}
	}
	var in intelligence.IntelligenceConfigInput
	// Bound administrator payloads before decoding credential-bearing configuration input.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16*1024)
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.BadRequest(c, "Invalid intelligence test configuration")
		return
	}
	data, err := h.svc.SaveConfig(c.Request.Context(), id, in)
	if !httpx.ErrorFrom(c, err) {
		if id == 0 {
			httpx.Created(c, data)
		} else {
			httpx.Success(c, data)
		}
	}
}

// AdminDelete deletes an idle configuration after verifying administrator
// identity.
func (h *IntelligenceHandler) AdminDelete(c *gin.Context) {
	if _, ok := intelligenceActor(c, true); !ok {
		return
	}
	id, ok := intelligenceConfigID(c)
	if !ok {
		return
	}
	if !httpx.ErrorFrom(c, h.svc.DeleteConfig(c.Request.Context(), id)) {
		httpx.Success(c, gin.H{"deleted": true})
	}
}

// AdminRun queues one check and returns the accepted job to administrators.
func (h *IntelligenceHandler) AdminRun(c *gin.Context) {
	if _, ok := intelligenceActor(c, true); !ok {
		return
	}
	id, ok := intelligenceConfigID(c)
	if !ok {
		return
	}
	run, err := h.svc.Run(c.Request.Context(), id)
	if !httpx.ErrorFrom(c, err) {
		httpx.Accepted(c, run)
	}
}

// AdminRuns returns lightweight configuration history to the authenticated
// administrator caller.
func (h *IntelligenceHandler) AdminRuns(c *gin.Context) {
	if _, ok := intelligenceActor(c, true); !ok {
		return
	}
	id, ok := intelligenceConfigID(c)
	if !ok {
		return
	}
	runs, err := h.svc.AdminRuns(c.Request.Context(), id)
	if !httpx.ErrorFrom(c, err) {
		httpx.Success(c, runs)
	}
}

// AdminDetail returns the complete saved observation for an authenticated
// administrator.
func (h *IntelligenceHandler) AdminDetail(c *gin.Context) {
	if _, ok := intelligenceActor(c, true); !ok {
		return
	}
	run, err := h.svc.AdminDetail(c.Request.Context(), c.Param("id"))
	if !httpx.ErrorFrom(c, err) {
		httpx.Success(c, run)
	}
}

// UserList returns enabled check summaries for the authenticated user's
// available groups.
func (h *IntelligenceHandler) UserList(c *gin.Context) {
	id, ok := intelligenceActor(c, false)
	if !ok {
		return
	}
	data, err := h.svc.UserList(c.Request.Context(), id)
	if !httpx.ErrorFrom(c, err) {
		httpx.Success(c, data)
	}
}

// UserDetail returns a saved observation after service-level current group
// authorization.
func (h *IntelligenceHandler) UserDetail(c *gin.Context) {
	id, ok := intelligenceActor(c, false)
	if !ok {
		return
	}
	data, err := h.svc.UserDetail(c.Request.Context(), id, c.Param("id"))
	if !httpx.ErrorFrom(c, err) {
		httpx.Success(c, data)
	}
}
