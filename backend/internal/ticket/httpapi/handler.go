package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/server/adminapi"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	support "github.com/TokenFlux/TokenRouter/internal/ticket"
)

// handler.go serves personal tickets, administrator support handling,
// settings, and private files. The authenticated route scope determines the
// actor sent to the service. Administrator accounts using personal URLs
// receive the same owner checks as ordinary users, and the request body cannot
// grant staff access.
//
// Creation and replies accept a JSON payload or multipart form with private
// files. Request limits apply before parsing, temporary files are removed, and
// content validation precedes persistence. Responses expose committed
// conversation results; newly committed staff events are queued afterward.
// Original downloads and revalidated previews share ownership checks and
// restrictive private response headers, keeping attachment bytes behind
// authentication.

// TicketHandler adapts authenticated support requests to ticket operations and
// committed event delivery.
// @project-doc docs/domains/support_tickets.md#ticket_lifecycle
type TicketHandler struct {
	service *support.TicketService       // ticket business operations
	config  *support.TicketConfigService // current ticket configuration reader
	runtime ticketNotificationSender     // committed event queue sender
}

// ticketNotificationSender accepts committed support reply and closure events
// for queued delivery.
type ticketNotificationSender interface {
	NotifyReply(*support.Ticket)
	NotifyClosure(*support.Ticket)
}

// NewTicketHandler binds ticket operations, live configuration, and committed
// event notification.
func NewTicketHandler(s *support.TicketService, cfg *support.TicketConfigService, runtime *support.TicketRuntime) *TicketHandler {
	h := &TicketHandler{service: s, config: cfg}
	if runtime != nil {
		h.runtime = runtime
	}
	return h
}

// ticketActor reads identity and role from the authenticated Gin context.
// Administrator scope requires the administrator role, while personal
// endpoints keep ownership restrictions even when the account itself is an
// administrator.
func ticketActor(c *gin.Context, admin bool) (support.TicketActor, bool) {
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "User not authenticated")
		return support.TicketActor{}, false
	}
	if admin {
		role, _ := authctx.GetUserRoleFromContext(c)
		if role != "admin" {
			response.Forbidden(c, "Administrator access required")
			return support.TicketActor{}, false
		}
	}
	return support.TicketActor{UserID: subject.UserID, IsAdmin: admin}, true
}

// ticketID parses a positive route identifier or writes a bad-request
// response. Callers stop processing when the returned boolean is false,
// preventing an invalid identifier from reaching ticket persistence.
func ticketID(c *gin.Context, key string) (int64, bool) {
	id, err := strconv.ParseInt(c.Param(key), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid ticket identifier")
		return 0, false
	}
	return id, true
}

// Config serves personal ticket limits and current module availability.
func (h *TicketHandler) Config(c *gin.Context) { h.getConfig(c, false) }

// AdminConfig serves administrator ticket settings while allowing disabled-
// module recovery.
func (h *TicketHandler) AdminConfig(c *gin.Context) { h.getConfig(c, true) }

// getConfig reads ticket limits for the authenticated entry scope. The
// configuration endpoint remains available while support is disabled, allowing
// administrator settings to enable the module and personal pages to show
// availability.
func (h *TicketHandler) getConfig(c *gin.Context, admin bool) {
	if _, ok := ticketActor(c, admin); !ok {
		return
	}
	cfg, err := h.config.Get(c.Request.Context())
	if !response.ErrorFrom(c, err) {
		response.Success(c, cfg)
	}
}

// UpdateConfig validates a bounded administrator settings body and persists
// supplied values. Authentication is checked before parsing, and publication
// occurs through the configuration service after its atomic settings batch
// succeeds.
func (h *TicketHandler) UpdateConfig(c *gin.Context) {
	if _, ok := ticketActor(c, true); !ok {
		return
	}
	var input support.UpdateTicketConfigInput
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid ticket settings")
		return
	}
	cfg, err := h.config.Update(c.Request.Context(), &input)
	if !response.ErrorFrom(c, err) {
		response.Success(c, cfg)
	}
}

// List serves the authenticated user ticket list through shared request
// validation.
func (h *TicketHandler) List(c *gin.Context) { h.list(c, false) }

// AdminList serves the administrator support queue through shared filter
// validation.
func (h *TicketHandler) AdminList(c *gin.Context) { h.list(c, true) }

// list extracts page and filter query values for an authenticated actor. The
// service normalizes pagination and checks module availability before querying
// the owner-scoped or administrator ticket list.
func (h *TicketHandler) list(c *gin.Context, admin bool) {
	actor, ok := ticketActor(c, admin)
	if !ok {
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	result, err := h.service.List(c.Request.Context(), actor, support.TicketListFilter{Page: page, PageSize: size, Type: c.Query("type"), Status: c.Query("status"), Priority: c.Query("priority"), Search: c.Query("q")})
	if !response.ErrorFrom(c, err) {
		response.Success(c, result)
	}
}

// Get serves one personal conversation through shared ticket identifier
// validation.
func (h *TicketHandler) Get(c *gin.Context) { h.get(c, false) }

// AdminGet serves one administrator conversation through shared ticket
// identifier validation.
func (h *TicketHandler) AdminGet(c *gin.Context) { h.get(c, true) }

// get reads a positive ticket identifier for the authenticated entry scope.
// The service checks module availability and persistence applies ownership,
// returning not found for inaccessible personal conversations.
func (h *TicketHandler) get(c *gin.Context, admin bool) {
	actor, ok := ticketActor(c, admin)
	if !ok {
		return
	}
	id, ok := ticketID(c, "id")
	if !ok {
		return
	}
	ticket, err := h.service.Get(c.Request.Context(), actor, id)
	if !response.ErrorFrom(c, err) {
		response.Success(c, ticket)
	}
}

// Create parses a bounded personal submission and forwards its Idempotency-Key. Files and JSON payload are validated together before persistence, and
// the authenticated actor supplies ownership regardless of submitted fields.
func (h *TicketHandler) Create(c *gin.Context) {
	actor, ok := ticketActor(c, false)
	if !ok {
		return
	}
	var input support.CreateTicketInput
	files, ok := h.readInput(c, &input)
	if !ok {
		return
	}
	input.Attachments = files
	input.IdempotencyKey = c.GetHeader("Idempotency-Key")
	ticket, err := h.service.Create(c.Request.Context(), actor, &input)
	if !response.ErrorFrom(c, err) {
		response.Created(c, ticket)
	}
}

// Reply serves a personal conversation reply through shared submission
// handling.
func (h *TicketHandler) Reply(c *gin.Context) { h.reply(c, false) }

// AdminReply serves a support conversation reply through shared submission
// handling.
func (h *TicketHandler) AdminReply(c *gin.Context) { h.reply(c, true) }

// reply parses a bounded message submission and forwards the authenticated
// entry scope. The returned creation marker identifies newly committed
// messages, so staff notification is requested only after successful
// persistence.
func (h *TicketHandler) reply(c *gin.Context, admin bool) {
	actor, ok := ticketActor(c, admin)
	if !ok {
		return
	}
	id, ok := ticketID(c, "id")
	if !ok {
		return
	}
	var input support.ReplyTicketInput
	files, ok := h.readInput(c, &input)
	if !ok {
		return
	}
	input.Attachments = files
	input.IdempotencyKey = c.GetHeader("Idempotency-Key")
	ticket, err := h.service.Reply(c.Request.Context(), actor, id, &input)
	if response.ErrorFrom(c, err) {
		return
	}
	if admin && ticket.ReplyCreated && h.runtime != nil {
		h.runtime.NotifyReply(ticket)
	}
	response.Success(c, ticket)
}

// Cancel serves a personal ticket withdrawal through shared terminal-state
// handling.
func (h *TicketHandler) Cancel(c *gin.Context) { h.close(c, false, support.TicketStatusCancelled) }

// AdminCancel serves an administrator ticket withdrawal through shared
// terminal-state handling.
func (h *TicketHandler) AdminCancel(c *gin.Context) { h.close(c, true, support.TicketStatusCancelled) }

// Complete serves a personal ticket completion through shared terminal-state
// handling.
func (h *TicketHandler) Complete(c *gin.Context) { h.close(c, false, support.TicketStatusCompleted) }

// AdminComplete serves an administrator ticket completion through shared
// terminal-state handling.
func (h *TicketHandler) AdminComplete(c *gin.Context) {
	h.close(c, true, support.TicketStatusCompleted)
}

// close applies completion or withdrawal under the authenticated entry scope.
// Only first committed administrator closures enqueue notification; personal
// actions and terminal-state retries return their results without creating
// another event.
func (h *TicketHandler) close(c *gin.Context, admin bool, status string) {
	actor, ok := ticketActor(c, admin)
	if !ok {
		return
	}
	id, ok := ticketID(c, "id")
	if !ok {
		return
	}
	ticket, err := h.service.Close(c.Request.Context(), actor, id, status)
	if response.ErrorFrom(c, err) {
		return
	}
	// Enqueue after a first committed administrator action; personal closures and
	// terminal retries have no new staff event to deliver to the owner.
	if admin && ticket.ClosureCreated && h.runtime != nil {
		h.runtime.NotifyClosure(ticket)
	}
	response.Success(c, ticket)
}

// Update accepts a bounded administrator priority change for an identified
// ticket. The service validates its priority, and the repository locks the
// ticket before checking that the conversation remains open.
func (h *TicketHandler) Update(c *gin.Context) {
	actor, ok := ticketActor(c, true)
	if !ok {
		return
	}
	id, ok := ticketID(c, "id")
	if !ok {
		return
	}
	var input support.UpdateTicketInput
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid ticket update")
		return
	}
	ticket, err := h.service.Update(c.Request.Context(), actor, id, &input)
	if !response.ErrorFrom(c, err) {
		response.Success(c, ticket)
	}
}

// Download serves an authorized personal attachment through private original-
// file delivery.
func (h *TicketHandler) Download(c *gin.Context) { h.download(c, false) }

// AdminDownload serves an authorized administrator attachment through private
// original-file delivery.
func (h *TicketHandler) AdminDownload(c *gin.Context) { h.download(c, true) }

// Preview serves an authorized personal attachment through revalidated inline
// preview delivery.
func (h *TicketHandler) Preview(c *gin.Context) { h.serveAttachment(c, false, true) }

// AdminPreview serves an authorized administrator attachment through
// revalidated inline preview delivery.
func (h *TicketHandler) AdminPreview(c *gin.Context) { h.serveAttachment(c, true, true) }

// download delegates private original-file delivery using the selected
// authenticated scope.
func (h *TicketHandler) download(c *gin.Context, admin bool) {
	h.serveAttachment(c, admin, false)
}

// serveAttachment authorizes the ticket and returns private original or
// revalidated preview bytes. Responses disable storage, sniffing, framing, and
// active content, while preview data uses a format-derived name and MIME.
// @project-doc docs/domains/support_tickets.md#ticket_attachments
func (h *TicketHandler) serveAttachment(c *gin.Context, admin, preview bool) {
	c.Header("Cache-Control", "private, no-store")
	actor, ok := ticketActor(c, admin)
	if !ok {
		return
	}
	id, ok := ticketID(c, "id")
	if !ok {
		return
	}
	attachmentID, ok := ticketID(c, "attachmentId")
	if !ok {
		return
	}
	file, err := h.service.Attachment(c.Request.Context(), actor, id, attachmentID)
	if response.ErrorFrom(c, err) {
		return
	}
	name, contentType, data := file.Filename, file.ContentType, file.Data
	disposition := "attachment"
	if preview {
		result, err := support.PreviewTicketAttachment(file)
		if response.ErrorFrom(c, err) {
			return
		}
		name, contentType, data = result.Name, result.ContentType, result.Data
		disposition = "inline"
	}
	c.Header("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": name}))
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Cache-Control", "private, no-store")
	// Apply sandbox and anti-sniffing headers so a browser treating downloaded
	// content as a document cannot execute it with the application origin.
	c.Header("Content-Security-Policy", "sandbox; default-src 'none'; base-uri 'none'; form-action 'none'")
	c.Header("X-Frame-Options", "DENY")
	c.Header("Referrer-Policy", "no-referrer")
	c.Data(http.StatusOK, contentType, data)
}

// readInput parses a bounded JSON or multipart ticket payload and its uploaded
// files. It checks current configuration before reading upload bodies. JSON
// accepts one complete object; multipart requires one JSON payload field and
// repeated files fields.
//
// The total request limit derives from current attachment limits and the fixed
// combined cap. Multipart temporary files are removed before returning, while
// individual streams are closed after bounded reads. Invalid payloads, excess
// files, unexpected fields, and size overruns write one error response and
// reject the complete submission. Returned files still undergo content
// validation in the service.
// @project-doc docs/domains/support_tickets.md#ticket_attachments
func (h *TicketHandler) readInput(c *gin.Context, dst any) ([]support.TicketAttachmentUpload, bool) {
	cfg, err := h.config.Get(c.Request.Context())
	if response.ErrorFrom(c, err) {
		return nil, false
	}
	// Reject disabled uploads before body parsing, preventing unavailable support
	// endpoints from consuming temporary storage and document processing resources
	// for submitted files.
	if !cfg.Enabled {
		response.ErrorFrom(c, support.ErrTicketDisabled)
		return nil, false
	}
	mediaType, _, _ := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if mediaType == "application/json" {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
		decoder := json.NewDecoder(c.Request.Body)
		if err := decoder.Decode(dst); err != nil {
			response.BadRequest(c, "Invalid ticket payload")
			return nil, false
		}
		// Require EOF after the first JSON object so extra objects or oversized
		// trailing bytes cannot be accepted as an apparently bounded ticket request.
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			response.BadRequest(c, "Invalid ticket payload")
			return nil, false
		}
		return nil, true
	}
	if mediaType != "multipart/form-data" {
		response.BadRequest(c, "Expected multipart/form-data")
		return nil, false
	}
	// Derive the parser limit from configured attachment capacity before reading
	// files, so disabled or reduced upload settings also reduce accepted body
	// allocation.
	maxFiles := min(int64(cfg.MaxAttachments)*int64(cfg.MaxAttachmentSizeMB)<<20, support.TicketMaxTotalAttachmentBytes)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxFiles+(1<<20))
	err = c.Request.ParseMultipartForm(8 << 20)
	if c.Request.MultipartForm != nil {
		// Multipart spill files belong to this request even when validation fails.
		// Report cleanup errors so an administrator can identify any retained temporary files.
		defer func() {
			if err := c.Request.MultipartForm.RemoveAll(); err != nil {
				slog.Error("ticket temporary upload cleanup failed", "error", err)
			}
		}()
	}
	if err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			response.ErrorWithDetails(c, 413, "工单上传内容超过大小限制", "TICKET_ATTACHMENT_TOO_LARGE", nil)
		} else {
			response.BadRequest(c, "Invalid multipart upload")
		}
		return nil, false
	}
	form := c.Request.MultipartForm
	payloads := form.Value["payload"]
	if len(payloads) != 1 || len(payloads[0]) > 1<<20 || json.Unmarshal([]byte(payloads[0]), dst) != nil {
		response.BadRequest(c, "Invalid ticket payload")
		return nil, false
	}
	for name := range form.File {
		if name != "files" {
			response.BadRequest(c, "Unexpected attachment field")
			return nil, false
		}
	}
	headers := form.File["files"]
	if len(headers) > cfg.MaxAttachments {
		response.ErrorWithDetails(c, 400, "附件数量超过限制", "TICKET_ATTACHMENT_LIMIT", nil)
		return nil, false
	}
	files := make([]support.TicketAttachmentUpload, 0, len(headers))
	var total int64
	limit := int64(cfg.MaxAttachmentSizeMB) << 20
	for _, header := range headers {
		if header.Size > limit {
			response.ErrorWithDetails(c, 400, "附件大小超过限制", "TICKET_ATTACHMENT_TOO_LARGE", nil)
			return nil, false
		}
		file, err := header.Open()
		if err != nil {
			response.BadRequest(c, "Cannot read attachment")
			return nil, false
		}
		data, readErr := io.ReadAll(io.LimitReader(file, limit+1))
		_ = file.Close()
		total += int64(len(data))
		if readErr != nil || int64(len(data)) > limit || total > support.TicketMaxTotalAttachmentBytes {
			response.ErrorWithDetails(c, 400, "附件大小超过限制", "TICKET_ATTACHMENT_TOO_LARGE", nil)
			return nil, false
		}
		files = append(files, support.TicketAttachmentUpload{Name: strings.TrimSpace(header.Filename), ContentType: header.Header.Get("Content-Type"), Data: data})
	}
	return files, true
}

// RegisterUserRoutes mounts personal ticket endpoints behind panel JWT authentication.
// Callers install authentication and request limits before registration. Administrator
// accounts using this group receive personal ownership checks for every operation.
func RegisterUserRoutes(parent *gin.RouterGroup, h *TicketHandler) {
	g := parent.Group("/tickets")
	g.GET("/config", h.Config)
	g.GET("", h.List)
	g.POST("", h.Create)
	g.GET("/:id", h.Get)
	g.POST("/:id/replies", h.Reply)
	g.POST("/:id/cancel", h.Cancel)
	g.POST("/:id/complete", h.Complete)
	g.GET("/:id/attachments/:attachmentId", h.Download)
	g.GET("/:id/attachments/:attachmentId/preview", h.Preview)
}

// RegisterAdminRoutes mounts support handling and ticket settings behind administrator authentication.
// The parent supplies authentication, auditing, and request limits. Settings remain
// accessible when the module is disabled so administrators can enable it again.
// Each route also registers its browser continuation in the MCP operation catalogue.
func RegisterAdminRoutes(parent *gin.RouterGroup, h *TicketHandler) {
	g := parent.Group("/tickets")
	adminapi.Register(g, ManagementOperations["tickets.settings.get"], h.AdminConfig)
	adminapi.Register(g, ManagementOperations["tickets.settings.update"], h.UpdateConfig)
	adminapi.Register(g, ManagementOperations["tickets.list"], h.AdminList)
	adminapi.Register(g, ManagementOperations["tickets.get"], h.AdminGet)
	adminapi.Register(g, ManagementOperations["tickets.update"], h.Update)
	adminapi.Register(g, ManagementOperations["tickets.reply"], h.AdminReply)
	adminapi.Register(g, ManagementOperations["tickets.complete"], h.AdminComplete)
	adminapi.Register(g, ManagementOperations["tickets.cancel"], h.AdminCancel)
	adminapi.Register(g, ManagementOperations["tickets.attachments.download"], h.AdminDownload)
	adminapi.Register(g, ManagementOperations["tickets.attachments.preview"], h.AdminPreview)
}
