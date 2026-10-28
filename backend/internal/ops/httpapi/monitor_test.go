package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/ops/monitor"
)

// monitorHTTPRepository supplies the passive query methods exercised by HTTP
// scope tests. The captured filter exposes the permission inputs reaching storage.
type monitorHTTPRepository struct {
	monitor.ChannelMonitorV2Repository                                // unused repository query methods
	filter                             monitor.ChannelMonitorV2Filter // captured authorized storage scope
}

// GetConfig returns enabled monitoring for the isolated HTTP scope fixture.
func (r *monitorHTTPRepository) GetConfig(context.Context) (*monitor.ChannelMonitorV2Config, error) {
	return &monitor.ChannelMonitorV2Config{Enabled: true}, nil
}

// GetSnapshot captures the server-authorized filter and returns volume-bearing data.
func (r *monitorHTTPRepository) GetSnapshot(_ context.Context, filter monitor.ChannelMonitorV2Filter, cfg monitor.ChannelMonitorV2Config, _ bool) (*monitor.ChannelMonitorV2Snapshot, error) {
	r.filter = filter
	return &monitor.ChannelMonitorV2Snapshot{Config: cfg, Metrics: monitor.ChannelMonitorV2Metric{RequestCount: 123, RPM: 9}}, nil
}

// TestMonitorUserScopeAndRedaction requests an unauthorized group with a verified
// user identity. It checks that server-derived scope reaches storage and ordinary
// response JSON does not expose raw request counts or fleet request rates.
func TestMonitorUserScopeAndRedaction(t *testing.T) {
	repo := &monitorHTTPRepository{}
	h := NewChannelMonitorV2Handler(monitor.NewChannelMonitorV2Service(repo), func(context.Context, int64) ([]int64, error) {
		return []int64{7}, nil
	})
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(authctx.ContextKeyUser, authctx.AuthSubject{UserID: 12})
		c.Set(authctx.ContextKeyUserRole, "user")
	})
	RegisterChannelMonitorUserRoutes(router.Group("/api/v1"), h)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/channel-monitor/v2/snapshot?group_id=99", nil))
	require.Equal(t, http.StatusOK, response.Code)
	require.True(t, repo.filter.RestrictGroups)
	require.Equal(t, []int64{7}, repo.filter.AllowedGroupIDs)
	require.Equal(t, []int64{99}, repo.filter.GroupIDs)
	require.Contains(t, response.Body.String(), `"request_count":0`)
	require.Contains(t, response.Body.String(), `"rpm":0`)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/channel-monitor/v2/snapshot?range=unbounded", nil))
	require.Equal(t, http.StatusBadRequest, response.Code)
}
