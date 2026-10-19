package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// handler_test.go checks pool input validation and authentication before service
// calls. Invalid pagination and oversized configuration bodies must fail locally,
// while an anonymous directory request must stop before any snapshot or source
// credentials can be read through the service.

// TestListValidatesFilters rejects malformed display filters before requesting
// a snapshot.
// It verifies negative pages, excessive page sizes, invalid group IDs, and long
// searches produce a bad-request response without touching service dependencies.
func TestListValidatesFilters(t *testing.T) {
	h := &RemoteAccountPoolHandler{}
	for _, query := range []string{"page=-1", "page_size=501", "group_id=0", "page=bad", "search=" + strings.Repeat("x", 201)} {
		t.Run(query, func(t *testing.T) {
			router := gin.New()
			router.GET("/account-pool", h.List)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/account-pool?"+query, nil))
			require.Equal(t, http.StatusBadRequest, recorder.Code)
		})
	}
}

// TestListRequiresAuthenticatedSubject sends anonymous reads to both user
// directory endpoints.
// The handler must return unauthorized before dereferencing its group-access or pool
// service dependencies, even when the surrounding router omitted its JWT guard.
func TestListRequiresAuthenticatedSubject(t *testing.T) {
	router := gin.New()
	RegisterUserRoutes(router.Group(""), &RemoteAccountPoolHandler{})
	for _, path := range []string{"/account-pool", "/account-pool/options"} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusUnauthorized, recorder.Code)
	}
}

// TestUpdateSettingsRejectsOversizedBody supplies a credential field beyond
// the configuration limit.
// The handler returns bad request before reaching persistence, so oversized JSON
// cannot hold the remote session transaction or consume a refresh token.
func TestUpdateSettingsRejectsOversizedBody(t *testing.T) {
	router := gin.New()
	h := &RemoteAccountPoolHandler{}
	router.PUT("/settings", h.UpdateSettings)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/settings", strings.NewReader(`{"refresh_token":"`+strings.Repeat("x", 65<<10)+`"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
}
