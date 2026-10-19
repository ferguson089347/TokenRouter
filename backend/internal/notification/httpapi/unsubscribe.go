package httpapi

import (
	"errors"
	"html"
	"mime"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/notification"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
)

// unsubscribe.go displays signed email preference forms and saves confirmed
// recipient choices. GET verifies the link and shows its current state, while
// POST validates a bounded form and persists the selected optional-event
// preference. Both paths apply private response headers and escape recipient text.

// notificationEmailUnsubscribeTokenLimit bounds signed token input before parsing
// or rendering, limiting the data carried through public preference forms.
const notificationEmailUnsubscribeTokenLimit = 4096

// RegisterUnsubscribeRoute mounts signed preference preview and explicit confirmation.
// The GET route renders a form, and POST validates its bounded body before
// saving a choice. Both routes require the signed, unexpired email token.
func RegisterUnsubscribeRoute(group *gin.RouterGroup, endpoint *Handler) {
	group.GET("/email-unsubscribe", endpoint.UnsubscribeNotificationEmail)
	group.POST("/email-unsubscribe", endpoint.ConfirmNotificationEmailUnsubscribe)
}

// UnsubscribeNotificationEmail verifies a signed link and renders its
// confirmation form. GET leaves recipient preferences unchanged, so email
// clients that prefetch links cannot silently unsubscribe the recipient.
func (h *Handler) UnsubscribeNotificationEmail(c *gin.Context) {
	setNotificationEmailUnsubscribeHeaders(c)
	if h.notificationEmailService == nil {
		response.InternalError(c, "notification email service is not configured")
		return
	}
	token := strings.TrimSpace(c.Query("token"))
	if token == "" || len(token) > notificationEmailUnsubscribeTokenLimit || len(c.Request.URL.Query()["token"]) != 1 {
		response.BadRequest(c, "a valid unsubscribe token is required")
		return
	}
	result, err := h.notificationEmailService.PreviewUnsubscribe(c.Request.Context(), token)
	if err != nil {
		response.BadRequest(c, "unsubscribe link is invalid or expired")
		return
	}
	unsubscribed, err := h.notificationEmailService.IsUnsubscribed(c.Request.Context(), result.Email, result.Event)
	if err != nil {
		response.InternalError(c, "notification email preference is unavailable")
		return
	}
	if unsubscribed {
		body := "<h1>已退订 / Unsubscribed</h1><p><strong>" + html.EscapeString(result.Email) + "</strong> 已停止接收此类邮件。These email notifications are currently disabled.</p>"
		if isTicketNotificationEvent(result.Event) {
			// Existing signed ticket links offer a restoration form after unsubscribe.
			// Rendering that form leaves the stored recipient decision unchanged until POST.
			body += "<p>如需继续接收客服回复、完成和撤销工单的邮件，请点击下方按钮。Restore emails about staff replies and ticket closure by confirming below.</p>" + notificationEmailPreferenceForm(token, "subscribe", "恢复工单邮件通知 / Restore ticket emails")
		}
		writeNotificationEmailUnsubscribePage(c, "邮件通知设置 / Email preferences", body)
		return
	}
	// Keep the validated token in the form body for confirmation, so submitting
	// the decision does not propagate the recipient token into another URL.
	body := "<h1>确认退订 / Confirm unsubscribe</h1><p>当前尚未退订。点击下方按钮后才会停止此类邮件通知。</p><p>Confirm that <strong>" + html.EscapeString(result.Email) + "</strong> should stop receiving <strong>" + html.EscapeString(notificationEmailPreferenceLabel(result.Event)) + "</strong>.</p>" +
		notificationEmailPreferenceForm(token, "unsubscribe", "确认退订 / Unsubscribe")
	writeNotificationEmailUnsubscribePage(c, "确认退订 / Confirm unsubscribe", body)
}

// ConfirmNotificationEmailUnsubscribe accepts a bounded, explicit preference
// form submission. Its signed token identifies the recipient and event;
// restoration is limited to ticket events, and query parameters cannot
// substitute for form confirmation.
func (h *Handler) ConfirmNotificationEmailUnsubscribe(c *gin.Context) {
	setNotificationEmailUnsubscribeHeaders(c)
	if h.notificationEmailService == nil {
		response.InternalError(c, "notification email service is not configured")
		return
	}
	mediaType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || mediaType != "application/x-www-form-urlencoded" {
		response.Error(c, http.StatusUnsupportedMediaType, "unsubscribe confirmation requires a form submission")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8*1024)
	if err := c.Request.ParseForm(); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			response.Error(c, http.StatusRequestEntityTooLarge, "unsubscribe confirmation is too large")
		} else {
			response.BadRequest(c, "invalid unsubscribe confirmation form")
		}
		return
	}
	form := c.Request.PostForm
	token := strings.TrimSpace(form.Get("token"))
	confirmation := form.Get("confirm")
	if len(form["token"]) != 1 || token == "" || len(token) > notificationEmailUnsubscribeTokenLimit || len(form["confirm"]) != 1 || (confirmation != "unsubscribe" && confirmation != "subscribe") {
		response.BadRequest(c, "an explicit unsubscribe confirmation is required")
		return
	}
	var result notification.NotificationEmailUnsubscribeResult
	if confirmation == "subscribe" {
		result, err = h.notificationEmailService.ResubscribeTicketNotifications(c.Request.Context(), token)
	} else {
		result, err = h.notificationEmailService.Unsubscribe(c.Request.Context(), token)
	}
	if err != nil {
		response.BadRequest(c, "unsubscribe link is invalid or expired")
		return
	}
	if confirmation == "subscribe" {
		body := "<h1>已恢复工单邮件通知 / Ticket emails restored</h1><p><strong>" + html.EscapeString(result.Email) + "</strong> 将按站点工单通知设置接收后续客服回复及结单邮件。Future staff replies and ticket closure emails will follow the site's notification settings.</p>"
		writeNotificationEmailUnsubscribePage(c, "已恢复工单邮件通知 / Ticket emails restored", body)
		return
	}
	body := "<h1>已退订 / Unsubscribed</h1><p>已停止此类邮件通知。You have unsubscribed <strong>" + html.EscapeString(result.Email) + "</strong> from <strong>" + html.EscapeString(notificationEmailPreferenceLabel(result.Event)) + "</strong>.</p>"
	writeNotificationEmailUnsubscribePage(c, "已退订 / Unsubscribed", body)
}

// isTicketNotificationEvent identifies the three events sharing the support
// email preference.
func isTicketNotificationEvent(event string) bool {
	switch event {
	case notification.NotificationEmailEventTicketStaffReply, notification.NotificationEmailEventTicketCompleted, notification.NotificationEmailEventTicketCancelled:
		return true
	default:
		return false
	}
}

// notificationEmailPreferenceLabel names the complete preference affected by the
// confirmation form.
func notificationEmailPreferenceLabel(event string) string {
	if isTicketNotificationEvent(event) {
		return "工单邮件通知（客服回复、完成与撤销） / Ticket emails (staff replies and closure)"
	}
	return event + " emails"
}

// notificationEmailPreferenceForm renders escaped hidden identity and an
// explicit action button. A relative form destination supports reverse proxy
// prefixes, and the signed token remains in the POST body after confirmation.
func notificationEmailPreferenceForm(token, confirmation, label string) string {
	return `<form method="post" action="email-unsubscribe"><input type="hidden" name="token" value="` + html.EscapeString(token) + `"><button type="submit" name="confirm" value="` + html.EscapeString(confirmation) + `">` + html.EscapeString(label) + `</button></form>`
}

// setNotificationEmailUnsubscribeHeaders applies private caching, origin, and
// form restrictions to token pages.
func setNotificationEmailUnsubscribeHeaders(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	c.Header("Referrer-Policy", "no-referrer")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("X-Frame-Options", "DENY")
	c.Header("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
}

// writeNotificationEmailUnsubscribePage wraps trusted form markup and escaped
// display text in HTML. Callers escape recipient and event content before
// passing body; the page permits inline styles and same-origin form submission.
func writeNotificationEmailUnsubscribePage(c *gin.Context, title, body string) {
	page := `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>` + html.EscapeString(title) + `</title></head><body style="font-family:system-ui,sans-serif;padding:24px;overflow-wrap:anywhere">` + body + "</body></html>"
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(page))
}
