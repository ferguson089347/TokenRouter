package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/intelligence"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// intelligencePreviewTestRepo supplies retained configurations and results for
// preview authorization tests.
type intelligencePreviewTestRepo struct {
	// IntelligenceRepository supplies unimplemented fixture operations. Expected
	// reads are overridden below; an unexpected operation fails through nil embedding.
	intelligence.IntelligenceRepository
	run    *intelligence.IntelligenceRun    // current fixture observation
	config *intelligence.IntelligenceConfig // current fixture configuration
	runErr error                            // injected observation failure
	png    []byte                           // saved fixture screenshot
}

// intelligencePreviewTestUsers supplies mutable current user lifecycle and
// administrator role values.
type intelligencePreviewTestUsers struct {
	// UserRepository supplies unimplemented fixture methods. Expected operations are
	// overridden below; unexpected calls fail immediately through nil embedding.
	identity.UserRepository
	user *identity.User // current fixture user
	err  error          // injected fixture failure
}

// intelligencePreviewTestGroups supplies mutable personal group visibility for
// preview grants.
type intelligencePreviewTestGroups struct {
	groups []routing.Group // visible fixture groups
	err    error           // injected fixture failure
}

// intelligencePreviewFixture owns an isolated router, handler and mutable
// access fixtures.
type intelligencePreviewFixture struct {
	handler *IntelligenceHandler           // preview fixture handler
	router  *gin.Engine                    // isolated fixture router
	repo    *intelligencePreviewTestRepo   // fixture observation persistence
	users   *intelligencePreviewTestUsers  // fixture user reader
	groups  *intelligencePreviewTestGroups // visible fixture groups
	enabled bool                           // fixture feature switch
}

// GetThumbnail returns a copied screenshot after fixture authorization completes.
func (r *intelligencePreviewTestRepo) GetThumbnail(context.Context, string) ([]byte, error) {
	return append([]byte(nil), r.png...), nil
}

// GetRun returns the fixture observation or the module not-found error.
func (r *intelligencePreviewTestRepo) GetRun(_ context.Context, id string) (*intelligence.IntelligenceRun, error) {
	if r.runErr != nil {
		return nil, r.runErr
	}
	if r.run == nil || r.run.ID != id {
		return nil, intelligence.ErrIntelligenceNotFound
	}
	return r.run, nil
}

// GetConfig returns the fixture configuration or the module not-found error.
func (r *intelligencePreviewTestRepo) GetConfig(_ context.Context, id int64) (*intelligence.IntelligenceConfig, error) {
	if r.config == nil || r.config.ID != id {
		return nil, intelligence.ErrIntelligenceNotFound
	}
	return r.config, nil
}

// GetByID returns the current fixture value for the supplied identity.
func (r *intelligencePreviewTestUsers) GetByID(_ context.Context, id int64) (*identity.User, error) {
	if r.err != nil {
		return nil, r.err
	}
	if r.user == nil || r.user.ID != id {
		return nil, errors.New("user unavailable")
	}
	return r.user, nil
}

// GetAvailableGroups returns the current fixture list of visible routing
// groups.
func (r *intelligencePreviewTestGroups) GetAvailableGroups(context.Context, int64) ([]routing.Group, error) {
	return r.groups, r.err
}

// newIntelligencePreviewFixture constructs retained artwork and mutable access
// state behind an isolated router. Strict parent headers exercise the
// dedicated content response policy without relying on production user
// records.
func newIntelligencePreviewFixture(t *testing.T) *intelligencePreviewFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	f := &intelligencePreviewFixture{enabled: true}
	f.repo = &intelligencePreviewTestRepo{
		run:    &intelligence.IntelligenceRun{ID: "iq_local_result", ConfigID: 1, GroupID: 11, Model: "test-model", Benchmark: "drawing", Status: "completed", Verdict: "passed", HTML: `<!doctype html><html><body><canvas id="art"></canvas><script>document.querySelector('canvas').width=100</script></body></html>`, RemoteID: "remote-private-bearer-id", HasArtifact: true},
		config: &intelligence.IntelligenceConfig{ID: 1, GroupID: 11, Model: "test-model", Benchmark: "drawing", Enabled: true},
	}
	f.users = &intelligencePreviewTestUsers{user: &identity.User{ID: 7, Role: "user", Status: "active"}}
	f.groups = &intelligencePreviewTestGroups{groups: []routing.Group{{ID: 11, Status: "active"}}}
	enabled := func(context.Context) bool { return f.enabled }
	svc := intelligence.NewIntelligenceService(f.repo, nil, nil, f.groups, nil, enabled, func(string) error { return nil }, nil)
	f.handler = NewIntelligenceHandler(svc)
	f.handler.preview = &intelligencePreviewAccess{secret: []byte("unit-preview-secret"), users: f.users, enabled: enabled}
	f.router = gin.New()
	f.router.Use(func(c *gin.Context) {
		// Install strict parent headers before the handler so the isolated content
		// response must supply the policy needed by drawing scripts.
		c.Header("Content-Security-Policy", "default-src 'self'; script-src 'nonce-main'; frame-ancestors 'none'")
		c.Header("X-Frame-Options", "DENY")
		if role := c.GetHeader("X-Test-Role"); role != "" {
			c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 7})
			c.Set(string(authctx.ContextKeyUserRole), role)
		}
	})
	f.router.GET("/runs/:id/preview", f.handler.UserPreview)
	f.router.GET("/admin/runs/:id/preview", f.handler.AdminPreview)
	f.router.GET("/api/v1/intelligence-tests/preview-content/:ticket", f.handler.PreviewContent)
	f.router.GET("/runs/:id/thumbnail", f.handler.UserThumbnail)
	f.router.GET("/admin/runs/:id/thumbnail", f.handler.AdminThumbnail)
	return f
}

// TestThumbnailRechecksFeatureAndGroupAccess serves an already captured PNG
// through the authenticated history endpoint. Feature closure, missing identity,
// removed group access, and an ordinary user on the admin path deny that image.
func TestThumbnailRechecksFeatureAndGroupAccess(t *testing.T) {
	f := newIntelligencePreviewFixture(t)
	f.repo.png = []byte("fixture-png")
	path := "/runs/iq_local_result/thumbnail"
	response := f.request(path, "user")
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "image/png", response.Header().Get("Content-Type"))
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	require.Equal(t, f.repo.png, response.Body.Bytes())
	require.Equal(t, http.StatusUnauthorized, f.request(path, "").Code)
	require.Equal(t, http.StatusForbidden, f.request("/admin"+path, "user").Code)
	f.groups.groups = nil
	require.Equal(t, http.StatusNotFound, f.request(path, "user").Code)
	f.enabled = false
	require.Equal(t, http.StatusForbidden, f.request(path, "user").Code)
}

// request sends a fixture request with the supplied synthetic panel role.
func (f *intelligencePreviewFixture) request(path, role string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if role != "" {
		req.Header.Set("X-Test-Role", role)
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

// issue requests and decodes one signed preview URL from the fixture. It
// requires a successful response before returning the path, so later
// assertions exercise an authenticated grant.
func (f *intelligencePreviewFixture) issue(t *testing.T, admin bool) string {
	t.Helper()
	path, role := "/runs/iq_local_result/preview", "user"
	if admin {
		path = "/admin/runs/iq_local_result/preview"
		role = "admin"
		f.users.user.Role = "admin"
	}
	rec := f.request(path, role)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	require.NotContains(t, rec.Body.String(), f.repo.run.RemoteID)
	var envelope struct {
		// Data holds the decoded preview-grant response returned by this request.
		Data struct {
			URL       string    `json:"url"`        // signed artifact URL
			ExpiresAt time.Time `json:"expires_at"` // ticket expiration timestamp
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
	require.True(t, strings.HasPrefix(envelope.Data.URL, "/api/v1/intelligence-tests/preview-content/"))
	require.WithinDuration(t, time.Now().Add(intelligencePreviewTTL), envelope.Data.ExpiresAt, 2*time.Second)
	return envelope.Data.URL
}

// TestIntelligencePreviewTicketValidation modifies signed claims, expiry and
// ticket structure around the accepted preview grant. Verification accepts the
// authentic bounded ticket and rejects forged, oversized, expired or future-
// dated access.
func TestIntelligencePreviewTicketValidation(t *testing.T) {
	secret := []byte("test-signing-key")
	now := time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)
	claims := intelligencePreviewClaims{RunID: "iq_123", UserID: 7, Expires: now.Add(4 * time.Minute).Unix()}
	ticket := signIntelligencePreview(secret, claims)
	got, err := verifyIntelligencePreview(secret, ticket, now)
	require.NoError(t, err)
	require.Equal(t, claims, got)

	parts := strings.Split(ticket, ".")
	changedClaims := claims
	changedClaims.Admin = true
	changedParts := strings.Split(signIntelligencePreview(secret, changedClaims), ".")
	cases := []struct {
		name, ticket string // name: fixture scenario name; ticket: supplied preview ticket
		key          []byte // supplied test credential
	}{
		{"changed privilege", changedParts[0] + "." + parts[1], secret},
		{"wrong key", ticket, []byte("other-key")},
		{"missing key", ticket, nil},
		{"missing separator", "no-ticket", secret},
		{"too many parts", ticket + ".extra", secret},
		{"malformed signature", parts[0] + ".!", secret},
		{"oversized", strings.Repeat("x", 1025), secret},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { _, err := verifyIntelligencePreview(tc.key, tc.ticket, now); require.Error(t, err) })
	}
	for _, tc := range []struct {
		name   string                           // fixture scenario name
		change func(*intelligencePreviewClaims) // scenario mutation callback
	}{
		{"expired", func(c *intelligencePreviewClaims) { c.Expires = now.Unix() }},
		{"excessive lifetime", func(c *intelligencePreviewClaims) { c.Expires = now.Add(intelligencePreviewTTL + 2*time.Second).Unix() }},
		{"missing run", func(c *intelligencePreviewClaims) { c.RunID = "" }},
		{"missing user", func(c *intelligencePreviewClaims) { c.UserID = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			invalid := claims
			tc.change(&invalid)
			_, err := verifyIntelligencePreview(secret, signIntelligencePreview(secret, invalid), now)
			require.Error(t, err)
		})
	}
}

// TestIntelligencePreviewContentHasIndependentSandboxHeaders serves saved
// drawing HTML beneath a strict parent policy fixture. The content response
// supplies its sandbox and restricted CSP while remaining noncacheable and
// excluding panel bearer authentication.
func TestIntelligencePreviewContentHasIndependentSandboxHeaders(t *testing.T) {
	f := newIntelligencePreviewFixture(t)
	url := f.issue(t, false)
	// Navigate the content URL without a panel bearer header, exercising the
	// single-artifact authorization supplied by its signed ticket.
	rec := f.request(url, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, f.repo.run.HTML, rec.Body.String())
	require.Equal(t, intelligencePreviewCSP, rec.Header().Get("Content-Security-Policy"))
	require.Contains(t, rec.Header().Get("Content-Security-Policy"), "sandbox allow-scripts")
	require.NotContains(t, rec.Header().Get("Content-Security-Policy"), "allow-same-origin")
	require.Contains(t, rec.Header().Get("Content-Security-Policy"), "connect-src 'none'")
	require.Contains(t, rec.Header().Get("Content-Security-Policy"), "frame-ancestors 'self'")
	require.Equal(t, "SAMEORIGIN", rec.Header().Get("X-Frame-Options"))
	require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	require.Equal(t, "no-referrer", rec.Header().Get("Referrer-Policy"))
	require.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	require.Equal(t, "inline", rec.Header().Get("Content-Disposition"))
	require.Contains(t, rec.Header().Get("Content-Type"), "text/html")
	require.Contains(t, rec.Header().Get("Permissions-Policy"), "camera=()")
	require.NotContains(t, rec.Body.String(), f.repo.run.RemoteID)
}

// TestIntelligencePreviewRechecksAccessAfterIssuance changes user, role,
// feature and group access after a drawing ticket is issued. Previously valid
// grants stop serving content whenever current authorization or retained
// artwork becomes unavailable.
func TestIntelligencePreviewRechecksAccessAfterIssuance(t *testing.T) {
	for _, tc := range []struct {
		name   string                            // fixture scenario name
		admin  bool                              // requested administrator scope
		change func(*intelligencePreviewFixture) // scenario mutation callback
	}{
		{"global disabled", false, func(f *intelligencePreviewFixture) { f.enabled = false }},
		{"user disabled", false, func(f *intelligencePreviewFixture) { f.users.user.Status = "disabled" }},
		{"user deleted", false, func(f *intelligencePreviewFixture) { f.users.user = nil }},
		{"user lookup unavailable", false, func(f *intelligencePreviewFixture) { f.users.err = errors.New("database unavailable") }},
		{"group access revoked", false, func(f *intelligencePreviewFixture) { f.groups.groups = nil }},
		{"group query unavailable", false, func(f *intelligencePreviewFixture) { f.groups.err = errors.New("database unavailable") }},
		{"config disabled", false, func(f *intelligencePreviewFixture) { f.repo.config.Enabled = false }},
		{"config deleted", false, func(f *intelligencePreviewFixture) { f.repo.config = nil }},
		{"group identity mismatch", false, func(f *intelligencePreviewFixture) { f.repo.config.GroupID = 22 }},
		{"run deleted", false, func(f *intelligencePreviewFixture) { f.repo.run = nil }},
		{"old artwork pruned", false, func(f *intelligencePreviewFixture) { f.repo.run.HTML = ""; f.repo.run.HasArtifact = false }},
		{"wrong benchmark", false, func(f *intelligencePreviewFixture) { f.repo.run.Benchmark = "candy" }},
		{"admin downgraded", true, func(f *intelligencePreviewFixture) { f.users.user.Role = "user" }},
		{"admin disabled", true, func(f *intelligencePreviewFixture) { f.users.user.Status = "disabled" }},
		{"admin global disabled", true, func(f *intelligencePreviewFixture) { f.enabled = false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newIntelligencePreviewFixture(t)
			url := f.issue(t, tc.admin)
			tc.change(f)
			rec := f.request(url, "")
			require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
			require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
			require.NotContains(t, rec.Body.String(), "<canvas")
			require.NotContains(t, rec.Body.String(), "remote-private")
		})
	}
}

// TestIntelligencePreviewIssuanceRequiresRoleAndAvailableArtwork requests user
// and administrator preview tickets with unavailable content and different
// identities. The handler enforces role, feature and retained-drawing checks
// before returning a usable grant.
func TestIntelligencePreviewIssuanceRequiresRoleAndAvailableArtwork(t *testing.T) {
	for _, tc := range []struct {
		name, path, role string                            // name: fixture scenario name; path: requested API path; role: injected panel role
		status           int                               // expected HTTP status
		change           func(*intelligencePreviewFixture) // scenario mutation callback
	}{
		{"anonymous", "/runs/iq_local_result/preview", "", 401, nil},
		{"user cannot mint admin", "/admin/runs/iq_local_result/preview", "user", 403, nil},
		{"disabled feature", "/runs/iq_local_result/preview", "user", 403, func(f *intelligencePreviewFixture) { f.enabled = false }},
		{"unavailable preview signer", "/runs/iq_local_result/preview", "user", 403, func(f *intelligencePreviewFixture) { f.handler.preview = nil }},
		{"unavailable artwork", "/runs/iq_local_result/preview", "user", 404, func(f *intelligencePreviewFixture) { f.repo.run.HTML = "" }},
		{"user hidden group", "/runs/iq_local_result/preview", "user", 404, func(f *intelligencePreviewFixture) { f.groups.groups = nil }},
		{"unknown run", "/runs/iq_not_existing/preview", "user", 404, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newIntelligencePreviewFixture(t)
			if tc.change != nil {
				tc.change(f)
			}
			rec := f.request(tc.path, tc.role)
			require.Equal(t, tc.status, rec.Code, rec.Body.String())
			require.NotContains(t, rec.Body.String(), "preview-content/")
		})
	}
}

// TestIntelligencePreviewContentRejectsForgedAndExpiredTickets requests
// content using altered and expired access grants. The handler returns an
// unavailable response and keeps the isolated artwork inaccessible through
// invalid bearer tickets.
func TestIntelligencePreviewContentRejectsForgedAndExpiredTickets(t *testing.T) {
	f := newIntelligencePreviewFixture(t)
	url := f.issue(t, false)
	parts := strings.Split(url, "/")
	ticket := parts[len(parts)-1]
	claims := intelligencePreviewClaims{RunID: "iq_local_result", UserID: 7, Expires: time.Now().Add(-time.Second).Unix()}
	for _, bad := range []string{"invalid", ticket + ".invalid", signIntelligencePreview([]byte("wrong-key"), claims), signIntelligencePreview(f.handler.preview.secret, claims)} {
		rec := f.request("/api/v1/intelligence-tests/preview-content/"+bad, "")
		require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
		require.NotContains(t, rec.Body.String(), "<canvas")
	}
}

// TestIntelligencePreviewAdminScopeIsExplicit issues user-scoped preview
// access while the caller holds an administrator role. The ticket continues to
// require user group visibility and cannot silently gain administrator
// artifact access.
func TestIntelligencePreviewAdminScopeIsExplicit(t *testing.T) {
	f := newIntelligencePreviewFixture(t)
	f.users.user.Role = "admin"
	f.groups.groups = nil
	// Issue through the user endpoint as an administrator so the test checks that
	// ticket scope still requires personal group visibility.
	rec := f.request("/runs/iq_local_result/preview", "admin")
	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	url := f.issue(t, true)
	rec = f.request(url, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, f.repo.run.HTML, rec.Body.String())
}
