package httpapi

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/intelligence"
)

// intelligenceRouteRepo supplies an empty administrator configuration list for
// route authorization tests.
type intelligenceRouteRepo struct {
	// IntelligenceRepository supplies unimplemented fixture operations. Expected
	// reads are overridden below; an unexpected operation fails through nil embedding.
	intelligence.IntelligenceRepository
}

// ListConfigs returns configured fixture records for administrator and user
// list checks.
func (*intelligenceRouteRepo) ListConfigs(context.Context) ([]intelligence.IntelligenceConfig, error) {
	return []intelligence.IntelligenceConfig{}, nil
}

// TestIntelligenceRoutesAccess mounts administrator commands and user result
// routes with test panel identities. Anonymous and ordinary users cannot issue
// paid checks, while disabled features retain administrator configuration
// access.
func TestIntelligenceRoutesAccess(t *testing.T) {
	svc := intelligence.NewIntelligenceService(&intelligenceRouteRepo{}, nil, nil, nil, nil, func(context.Context) bool { return false }, func(string) error { return nil })
	defer svc.Stop()
	h := NewIntelligenceHandler(svc)
	for _, tc := range []struct {
		name, role, method, path string // name: fixture scenario name; role: injected panel role; method: requested HTTP method; path: requested API path
		status                   int    // expected HTTP status
	}{
		{"匿名用户列表", "", "GET", "/intelligence-tests", 401},
		{"匿名管理配置", "", "POST", "/admin/intelligence-tests", 401},
		{"用户不能列管理配置", "user", "GET", "/admin/intelligence-tests", 403},
		{"用户不能创建配置", "user", "POST", "/admin/intelligence-tests", 403},
		{"用户不能编辑配置", "user", "PUT", "/admin/intelligence-tests/1", 403},
		{"用户不能删除配置", "user", "DELETE", "/admin/intelligence-tests/1", 403},
		{"用户不能触发测试", "user", "POST", "/admin/intelligence-tests/1/run", 403},
		{"用户不能读取管理历史", "user", "GET", "/admin/intelligence-tests/1/runs", 403},
		{"用户不能读取管理详情", "user", "GET", "/admin/intelligence-tests/runs/iq_1", 403},
		{"用户不能签管理预览", "user", "GET", "/admin/intelligence-tests/runs/iq_1/preview", 403},
		{"关闭后用户列表拒绝", "user", "GET", "/intelligence-tests", 403},
		{"关闭后用户详情拒绝", "user", "GET", "/intelligence-tests/runs/iq_1", 403},
		{"关闭后用户预览拒绝", "user", "GET", "/intelligence-tests/runs/iq_1/preview", 403},
		{"关闭后保留配置读取", "admin", "GET", "/admin/intelligence-tests", 200},
		{"关闭后管理员不能触发", "admin", "POST", "/admin/intelligence-tests/1/run", 403},
		{"用户无创建接口", "user", "POST", "/intelligence-tests", 404},
		{"用户无触发接口", "user", "POST", "/intelligence-tests/1/run", 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			r.Use(func(c *gin.Context) {
				if tc.role != "" {
					c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 7})
					c.Set(string(authctx.ContextKeyUserRole), tc.role)
				}
			})
			RegisterUserRoutes(r.Group(""), h)
			RegisterAdminRoutes(r.Group("/admin"), h)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
			require.Equal(t, tc.status, w.Code, w.Body.String())
		})
	}
}
