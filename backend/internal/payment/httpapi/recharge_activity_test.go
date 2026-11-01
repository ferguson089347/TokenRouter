package httpapi

import (
	"context"
	"maps"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/payment/testkit"
	"github.com/TokenFlux/TokenRouter/internal/testutil/sqlite"
)

// recharge_activity_test.go checks the published activity's anonymous response
// and visibility rules. Production configuration and instance readers operate
// against SQLite fixtures, while captured settings supply public tiers and page
// content separately from provider credentials that must remain private.

// rechargeActivitySettings stores test configuration values for the production reader.
type rechargeActivitySettings map[string]string

// GetValue returns the requested test setting or its empty missing value.
func (s rechargeActivitySettings) GetValue(_ context.Context, key string) (string, error) {
	return s[key], nil
}

// GetMultiple copies requested test settings into an independent response map.
func (s rechargeActivitySettings) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	values := make(map[string]string, len(keys))
	for _, key := range keys {
		values[key] = s[key]
	}
	return values, nil
}

// SetMultiple updates the supplied test keys while retaining other captured configuration.
func (s rechargeActivitySettings) SetMultiple(_ context.Context, values map[string]string) error {
	maps.Copy(s, values)
	return nil
}

// TestPublicRechargeActivity serves a configured promotion through its anonymous route.
// Only published matching slugs are visible, and public data must exclude the
// enabled payment provider's stored merchant identity and credential material.
func TestPublicRechargeActivity(t *testing.T) {
	client := sqlite.NewClient(t)
	_, err := client.PaymentProviderInstance.Create().SetName("private-merchant-name").SetProviderKey(payment.TypeEasyPay).
		SetConfig(`{"pid":"private-merchant-id","secretKey":"private-merchant-secret","currency":"CNY"}`).
		SetSupportedTypes("alipay").SetPaymentMode("redirect").SetEnabled(true).Save(t.Context())
	require.NoError(t, err)
	settings := rechargeActivitySettings{
		payment.SettingPaymentEnabled:        "true",
		payment.SettingRechargeBonusTiers:    `[{"min_amount":100,"bonus_amount":11}]`,
		payment.SettingRechargeBonusActivity: `{"campaign_id":"event","page_enabled":true,"slug":"double11","title":"Recharge event","timezone":"Asia/Shanghai"}`,
	}
	handler := NewPaymentHandler(nil, testkit.Configuration(client, settings, nil), nil)
	router := gin.New()
	router.GET("/activities/:slug", handler.GetRechargeActivity)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/activities/double11", nil))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"min_amount":100`)
	require.Contains(t, recorder.Body.String(), `"title":"Recharge event"`)
	require.NotContains(t, recorder.Body.String(), "private-merchant")
	require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	for _, path := range []string{"/activities/unknown", "/activities/double11"} {
		if path == "/activities/double11" {
			settings[payment.SettingRechargeBonusActivity] = `{"page_enabled":false}`
		}
		recorder = httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusNotFound, recorder.Code)
	}
}
