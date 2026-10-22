package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/intelligence"
	"github.com/TokenFlux/TokenRouter/internal/server/httpx"
)

// preview.go issues short-lived drawing tickets and serves isolated HTML
// responses. Tickets bind a run, user, role scope, and expiry using an HMAC
// domain separated from panel JWT use. Every content request rechecks the
// global feature switch, current user state, role, group authorization, and
// retained artifact before returning HTML.
//
// Model-generated markup is active untrusted content. The response supplies a
// sandbox and a policy restricting scripts to the isolated document while
// blocking network, forms, frames, and device permissions. Ticket URLs are
// marked noncacheable and use no-referrer behavior; the shared request loggers
// redact their credential-bearing path component. Authenticated thumbnail reads
// authorize the same observation before serving a stored PNG or requesting a
// bounded capture; image responses remain outside the active HTML content route.

const (
	// Drawing tickets expire quickly, while their content policy confines active
	// model HTML to a script-enabled sandbox that blocks external network access.
	intelligencePreviewTTL = 5 * time.Minute

	intelligencePreviewCSP = "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data: blob:; font-src data:; media-src data: blob:; connect-src 'none'; frame-src 'none'; worker-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'; sandbox allow-scripts"
)

// intelligencePreviewAccess supplies signing and current access checks for
// drawing tickets.
type intelligencePreviewAccess struct {
	// secret uses the configured stable JWT secret with a separate HMAC domain,
	// allowing signed previews across application instances.
	secret  []byte                     // HMAC signing key
	users   identity.UserRepository    // current user reader
	enabled func(context.Context) bool // current global switch
}

// intelligencePreviewClaims binds a saved artifact to one user, scope, and
// expiry.
type intelligencePreviewClaims struct {
	RunID   string `json:"run"`     // authorized drawing run
	UserID  int64  `json:"user"`    // authorized user identity
	Admin   bool   `json:"admin"`   // administrator access scope
	Expires int64  `json:"expires"` // Unix expiry seconds
}

// ProvideIntelligenceHandler constructs panel and preview endpoints using the
// configured stable signing secret. An empty secret leaves preview
// unavailable; callers supply current user reads and the global switch for
// every ticket access.
func ProvideIntelligenceHandler(svc *intelligence.IntelligenceService, secret string, users identity.UserRepository, enabled func(context.Context) bool) *IntelligenceHandler {
	h := NewIntelligenceHandler(svc)
	if secret != "" {
		h.preview = &intelligencePreviewAccess{secret: []byte(secret), users: users, enabled: enabled}
	}
	return h
}

// signIntelligencePreview signs a single-artifact access grant using a
// distinct HMAC domain. Claims are JSON encoded and URL-safe base64 encoded
// before signing, so consumers can validate the exact payload before decoding
// it.
func signIntelligencePreview(secret []byte, claims intelligencePreviewClaims) string {
	data, _ := json.Marshal(claims)
	payload := base64.RawURLEncoding.EncodeToString(data)
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte("intelligence-preview-v1:" + payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// verifyIntelligencePreview authenticates a bounded ticket and validates its
// run, user, and expiry. Verification compares the HMAC before decoding
// claims; expiry must lie within the configured lifetime plus clock-rounding
// tolerance.
func verifyIntelligencePreview(secret []byte, ticket string, now time.Time) (intelligencePreviewClaims, error) {
	var claims intelligencePreviewClaims
	invalid := errors.New("invalid or expired preview ticket")
	// Bound ticket parsing before allocating decoded claim data.
	if len(secret) == 0 || len(ticket) > 1024 {
		return claims, invalid
	}
	parts := strings.Split(ticket, ".")
	if len(parts) != 2 {
		return claims, invalid
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return claims, invalid
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte("intelligence-preview-v1:" + parts[0]))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return claims, invalid
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || json.Unmarshal(payload, &claims) != nil || claims.RunID == "" || claims.UserID <= 0 || claims.Expires <= now.Unix() || claims.Expires > now.Add(intelligencePreviewTTL+time.Second).Unix() {
		return intelligencePreviewClaims{}, invalid
	}
	return claims, nil
}

// UserPreview issues a drawing ticket using the current user's group
// permissions.
func (h *IntelligenceHandler) UserPreview(c *gin.Context) { h.issuePreview(c, false) }

// AdminPreview issues a drawing ticket after checking the current
// administrator role.
func (h *IntelligenceHandler) AdminPreview(c *gin.Context) { h.issuePreview(c, true) }

// UserThumbnail serves a saved PNG after checking the user's current group access.
func (h *IntelligenceHandler) UserThumbnail(c *gin.Context) { h.serveThumbnail(c, false) }

// AdminThumbnail serves a saved PNG after checking the current administrator identity.
func (h *IntelligenceHandler) AdminThumbnail(c *gin.Context) { h.serveThumbnail(c, true) }

// serveThumbnail authorizes a drawing observation before reading its PNG snapshot.
// Missing screenshots are generated from retained HTML through the service's
// bounded renderer. Global feature closure and revoked permissions deny cached
// screenshots as well as new renders, and successful responses disable browser caching.
func (h *IntelligenceHandler) serveThumbnail(c *gin.Context, admin bool) {
	c.Header("Cache-Control", "no-store")
	userID, ok := intelligenceActor(c, admin)
	if !ok {
		return
	}
	if h.preview == nil || !h.preview.enabled(c.Request.Context()) {
		httpx.Forbidden(c, "Intelligence preview is disabled")
		return
	}
	var run *intelligence.IntelligenceRun
	var err error
	if admin {
		run, err = h.svc.AdminDetail(c.Request.Context(), c.Param("id"))
	} else {
		run, err = h.svc.UserDetail(c.Request.Context(), userID, c.Param("id"))
	}
	if httpx.ErrorFrom(c, err) {
		return
	}
	png, err := h.svc.Thumbnail(c.Request.Context(), run)
	if httpx.ErrorFrom(c, err) {
		return
	}
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Security-Policy", "default-src 'none'; sandbox")
	c.Data(http.StatusOK, "image/png", png)
}

// previewRun reads a drawing using the access scope recorded in the ticket.
// User scope delegates to current group authorization, while administrator
// scope uses administrator detail after the caller validates role.
func (h *IntelligenceHandler) previewRun(ctx context.Context, claims intelligencePreviewClaims) (*intelligence.IntelligenceRun, error) {
	if claims.Admin {
		return h.svc.AdminDetail(ctx, claims.RunID)
	}
	return h.svc.UserDetail(ctx, claims.UserID, claims.RunID)
}

// issuePreview issues a noncacheable ticket after identity, feature, and
// artifact checks. Administrator scope requires the administrator role, and
// only retained drawing HTML can receive a grant with the configured short
// lifetime.
func (h *IntelligenceHandler) issuePreview(c *gin.Context, admin bool) {
	userID, ok := intelligenceActor(c, admin)
	if !ok {
		return
	}
	if h.preview == nil || !h.preview.enabled(c.Request.Context()) {
		httpx.Forbidden(c, "Intelligence preview is disabled")
		return
	}
	claims := intelligencePreviewClaims{RunID: c.Param("id"), UserID: userID, Admin: admin, Expires: time.Now().Add(intelligencePreviewTTL).Unix()}
	run, err := h.previewRun(c.Request.Context(), claims)
	if httpx.ErrorFrom(c, err) {
		return
	}
	if run == nil || run.Benchmark != "drawing" || run.HTML == "" {
		httpx.NotFound(c, "Drawing is unavailable")
		return
	}
	c.Header("Cache-Control", "no-store")
	httpx.Success(c, gin.H{"url": "/api/v1/intelligence-tests/preview-content/" + signIntelligencePreview(h.preview.secret, claims), "expires_at": time.Unix(claims.Expires, 0).UTC()})
}

// PreviewContent validates a ticket and rechecks current access before serving
// retained drawing HTML. Disabled users, revoked administrator roles,
// unavailable group access, and removed artifacts make even unexpired tickets
// unusable. The returned document receives its own sandbox, restricted CSP,
// no-store cache policy, and no-referrer behavior.
//
// The content endpoint uses the signed ticket instead of a panel bearer header
// because browsers navigate an iframe URL directly. Device permissions and
// external network operations remain blocked while inline drawing scripts can
// execute inside the isolated frame.
// @project-doc docs/domains/intelligence_tests.md#intelligence_preview
func (h *IntelligenceHandler) PreviewContent(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Referrer-Policy", "no-referrer")
	if h.preview == nil || h.preview.users == nil || !h.preview.enabled(c.Request.Context()) {
		httpx.NotFound(c, "Preview unavailable")
		return
	}
	claims, err := verifyIntelligencePreview(h.preview.secret, c.Param("ticket"), time.Now())
	if err != nil {
		httpx.NotFound(c, "Preview unavailable")
		return
	}
	// Recheck user lifecycle and role before reading content so a previously
	// issued ticket stops working after current access is revoked.
	user, err := h.preview.users.GetByID(c.Request.Context(), claims.UserID)
	if err != nil || user == nil || !user.IsActive() || (claims.Admin && !user.IsAdmin()) {
		httpx.NotFound(c, "Preview unavailable")
		return
	}
	run, err := h.previewRun(c.Request.Context(), claims)
	if err != nil || run == nil || run.Benchmark != "drawing" || run.HTML == "" {
		httpx.NotFound(c, "Preview unavailable")
		return
	}
	c.Header("Content-Security-Policy", intelligencePreviewCSP)
	c.Header("X-Frame-Options", "SAMEORIGIN")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Disposition", "inline")
	c.Header("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(run.HTML))
}
