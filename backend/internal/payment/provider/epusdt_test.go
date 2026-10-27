package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// epusdt_test.go exercises hosted checkout and payment observation against local
// TLS gateway fixtures. Signed callback cases protect fiat-money interpretation,
// merchant identity, numeric canonicalization, and rejection of ambiguous JSON
// representations before the common payment runtime receives a notification.

// TestEPUSDTCreatePayment sends a signed hosted checkout to a TLS fixture.
// It checks merchant association, cashier selection, fiat money, and the gateway deadline.
func TestEPUSDTCreatePayment(t *testing.T) {
	var gatewayURL string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/payments/gmpay/v1/order/create-transaction", r.URL.Path)
		require.NoError(t, r.ParseForm())
		params := map[string]string{}
		for key := range r.Form {
			params[key] = r.Form.Get(key)
		}
		require.Equal(t, epusdtSignature(params, "merchant-secret"), params["signature"])
		require.Equal(t, "100.00", params["amount"])
		require.Equal(t, "1001", params["pid"])
		require.Equal(t, "tr_fixture_order", params["order_id"])
		require.NotContains(t, params, "token")
		require.NotContains(t, params, "network")
		_, _ = fmt.Fprintf(w, `{"status_code":200,"data":{"trade_id":"gateway_trade","order_id":"tr_fixture_order","amount":100,"actual_amount":14.29,"currency":"CNY","status":4,"expiration_time":%d,"payment_url":%q}}`, time.Now().Add(10*time.Minute).Unix(), gatewayURL+"/pay/checkout-counter/gateway_trade")
	}))
	defer server.Close()
	gatewayURL = server.URL
	adapter := newEPUSDTFixture(t, server)
	result, err := adapter.CreatePayment(context.Background(), payment.CreatePaymentRequest{
		OrderID: "tr_fixture_order", Amount: "100.00", Subject: "Balance recharge",
		ReturnURL: "https://site.example/payment/result",
	})
	require.NoError(t, err)
	require.Equal(t, "gateway_trade", result.TradeNo)
	require.Equal(t, "CNY", result.Currency)
	require.WithinDuration(t, time.Now().Add(10*time.Minute), result.ExpiresAt, time.Second)
}

// TestEPUSDTCreateRejectsUnconfirmedResponses models lost or contradictory gateway replies.
// It requires an uncertainty classification so checkout retains an order that might already exist.
func TestEPUSDTCreateRejectsUnconfirmedResponses(t *testing.T) {
	for _, response := range []string{
		`not JSON`,
		`{"status_code":200,"data":{"trade_id":"gateway_trade","order_id":"another_order","amount":100,"currency":"CNY","status":4,"payment_url":"https://evil.example/pay"}}`,
	} {
		t.Run(response, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(response))
			}))
			defer server.Close()
			adapter := newEPUSDTFixture(t, server)
			result, err := adapter.CreatePayment(context.Background(), payment.CreatePaymentRequest{OrderID: "order", Amount: "100.00"})
			require.Nil(t, result)
			var appErr *apperror.ApplicationError
			require.ErrorAs(t, err, &appErr)
			require.Equal(t, payment.CodePaymentCreateUnconfirmed, appErr.Reason)
			require.NotContains(t, err.Error(), "merchant-secret")
		})
	}
}

// TestEPUSDTQueryUsesCurrentStatus reads cashier money and the dedicated status endpoint.
// Token quantities cannot replace the fiat amount used for the local payment confirmation.
func TestEPUSDTQueryUsesCurrentStatus(t *testing.T) {
	for _, state := range []int{epusdtWaiting, epusdtPaid, epusdtExpired, epusdtSelecting, 99} {
		t.Run(fmt.Sprint(state), func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/pay/checkout-counter-resp/") {
					_, _ = w.Write([]byte(`{"status_code":200,"data":{"trade_id":"gateway_trade","amount":100,"actual_amount":14.29,"currency":"CNY","status":1}}`))
					return
				}
				require.Equal(t, "/pay/check-status/gateway_trade", r.URL.Path)
				_, _ = fmt.Fprintf(w, `{"status_code":200,"data":{"trade_id":"gateway_trade","status":%d}}`, state)
			}))
			defer server.Close()
			adapter := newEPUSDTFixture(t, server)
			result, err := adapter.QueryOrder(context.Background(), "gateway_trade")
			if state == 99 {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, 100.0, result.Amount)
			want := map[int]string{
				1: payment.ProviderStatusPending, 2: payment.ProviderStatusSuccess,
				3: payment.ProviderStatusFailed, 4: payment.ProviderStatusPending,
			}
			require.Equal(t, want[state], result.Status)
			require.Equal(t, map[string]string{"pid": "1001", "currency": "CNY"}, result.Metadata)
		})
	}
}

// TestEPUSDTNotificationAuthenticatesFiatMoney checks a signed exponent-form JSON amount.
// The verifier reproduces GMPay canonical numbers and reports fiat money independently of token quantities.
func TestEPUSDTNotificationAuthenticatesFiatMoney(t *testing.T) {
	adapter, err := NewEPUSDT("fixture", map[string]string{
		"apiBase": "https://pay.example", "pid": "1001",
		"secretKey": "merchant-secret", "notifyUrl": "https://site.example/notify",
	})
	require.NoError(t, err)
	params := map[string]string{"pid": "1001", "order_id": "tr_order", "trade_id": "gateway_trade", "amount": "100", "actual_amount": "14.29", "status": "2"}
	raw := fmt.Sprintf(`{"pid":"1001","order_id":"tr_order","trade_id":"gateway_trade","amount":1e2,"actual_amount":14.2900,"status":2,"signature":%q}`, epusdtSignature(params, "merchant-secret"))
	notification, err := adapter.VerifyNotification(context.Background(), raw, nil)
	require.NoError(t, err)
	require.Equal(t, 100.0, notification.Amount)
	require.Equal(t, "tr_order", notification.OrderID)
	require.Equal(t, "1001", notification.Metadata["pid"])
}

// TestEPUSDTNotificationRejectsAmbiguity submits signed but structurally invalid payment messages.
// Duplicate keys, unknown names, merchant changes, and delimiter tricks cannot authenticate payment observations.
func TestEPUSDTNotificationRejectsAmbiguity(t *testing.T) {
	adapter, err := NewEPUSDT("fixture", map[string]string{
		"apiBase": "https://pay.example", "pid": "1001",
		"secretKey": "merchant-secret", "notifyUrl": "https://site.example/notify",
	})
	require.NoError(t, err)
	for _, raw := range []string{
		`{"pid":"1001","pid":"1001"}`,
		`{"unknown":""}`,
		`{"amount":"100"}`,
		`{"amount":1e999}`,
		`{"pid":"1001&status=2"}`,
		`{"pid":"1001"} {}`,
		`{"pid":{"value":"1001"}}`,
	} {
		_, err := adapter.VerifyNotification(context.Background(), raw, nil)
		require.Error(t, err, raw)
	}
	for _, change := range []map[string]any{
		{"pid": "another_merchant"}, {"status": 1}, {"amount": -100}, {"trade_id": "../other"},
	} {
		payload := map[string]any{"pid": "1001", "order_id": "tr_order", "trade_id": "gateway_trade", "amount": 100, "status": 2}
		maps.Copy(payload, change)
		params := map[string]string{}
		for key, value := range payload {
			params[key] = fmt.Sprint(value)
		}
		payload["signature"] = epusdtSignature(params, "merchant-secret")
		raw, err := json.Marshal(payload)
		require.NoError(t, err)
		_, err = adapter.VerifyNotification(context.Background(), string(raw), nil)
		require.Error(t, err)
	}
}

// TestEPUSDTRejectsUnsafeConfiguration checks merchant secrets and endpoint transport requirements.
// The constructor refuses cleartext URLs and URL-carried credentials before any provider request can occur.
func TestEPUSDTRejectsUnsafeConfiguration(t *testing.T) {
	for _, base := range []string{
		"http://pay.example", "https://user:secret@pay.example",
		"https://pay.example?secret=value", "https://pay.example#fragment",
	} {
		_, err := NewEPUSDT("fixture", map[string]string{
			"apiBase": base, "pid": "1001", "secretKey": "merchant-secret",
			"notifyUrl": "https://site.example/notify",
		})
		require.Error(t, err)
	}
}

// newEPUSDTFixture constructs an adapter trusting only the supplied local TLS server.
// The production timeout and redirect policy remain attached to the fixture client.
func newEPUSDTFixture(t *testing.T, server *httptest.Server) *EPUSDT {
	t.Helper()
	adapter, err := NewEPUSDT("fixture", map[string]string{
		"apiBase": server.URL, "pid": "1001", "secretKey": "merchant-secret", "currency": "CNY",
		"notifyUrl": "https://site.example/api/v1/payment/webhook/epusdt",
	})
	require.NoError(t, err)
	adapter.httpClient.Transport = server.Client().Transport
	return adapter
}
