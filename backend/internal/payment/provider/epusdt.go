package provider

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/shopspring/decimal"

	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// epusdt.go creates GMPay hosted checkouts and authenticates their payment
// notifications. The adapter keeps fiat order amounts separate from the token
// quantity selected by the cashier. Requests use the configured merchant PID
// and HMAC-SHA256 secret, while queries address the opaque trade identifier
// returned by successful checkout creation. HTTP requests retain their context,
// reject redirects, and bound response bodies before interpreting gateway data.
//
// Callback signatures cover sorted scalar fields rather than raw JSON. The
// decoder rejects duplicate keys, unknown fields, nested values, and delimiter
// ambiguity before reproducing the gateway's numeric spelling. The payment
// runtime separately checks the signed merchant order against its saved
// merchant and transaction binding. An uncertain creation response leaves the
// committed local order available for a later signed notification; requests
// are never retried automatically because the gateway rejects duplicate orders.

// EPUSDT transport bounds limit one provider operation and its decoded response;
// gateway statuses distinguish waiting, paid, expired, and asset-selection orders.
const (
	epusdtHTTPTimeout = 15 * time.Second // individual HTTP request budget
	epusdtMaxBody     = 1 << 20          // maximum response body bytes
	epusdtWaiting     = 1                // awaiting token transfer
	epusdtPaid        = 2                // gateway confirmed payment
	epusdtExpired     = 3                // gateway expired order
	epusdtSelecting   = 4                // awaiting cashier asset selection
)

// Adapter assertions keep checkout and merchant identity capabilities checked
// whenever the common payment interfaces change during application development.
var (
	_ payment.Provider                 = (*EPUSDT)(nil)
	_ payment.MerchantIdentityProvider = (*EPUSDT)(nil)
)

// EPUSDT implements GMPay checkout and verification for one configured merchant.
type EPUSDT struct {
	baseURL    string       // validated HTTPS service prefix
	pid        string       // configured merchant PID
	secret     string       // merchant HMAC signing secret
	currency   string       // fiat order currency
	notifyURL  string       // configured callback destination
	httpClient *http.Client // shared bounded transport client
}

// epusdtReply decodes the gateway envelope and common checkout observation fields.
type epusdtReply struct {
	Code int `json:"status_code"` // gateway business response code
	Data struct {
		TradeID    string      `json:"trade_id"`        // gateway parent trade identifier
		OrderID    string      `json:"order_id"`        // creation merchant order identifier
		Amount     json.Number `json:"amount"`          // fiat order amount
		Currency   string      `json:"currency"`        // fiat currency reported by gateway
		Status     int         `json:"status"`          // gateway payment state
		PaymentURL string      `json:"payment_url"`     // hosted cashier launch URL
		Expiration int64       `json:"expiration_time"` // creation expiration Unix seconds
	} `json:"data"` // creation or query payload
}

// NewEPUSDT constructs a merchant adapter with a bounded HTTPS client.
// The base URL must exclude user information, query parameters, and fragments;
// CNY and USD are supported fiat currencies, with CNY selected when omitted.
// A nonempty HTTPS notification destination is required before enabling checkout.
func NewEPUSDT(_ string, config map[string]string) (*EPUSDT, error) {
	base, err := url.Parse(strings.TrimRight(strings.TrimSpace(config["apiBase"]), "/"))
	if err != nil || base.Scheme != "https" || base.Hostname() == "" ||
		base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return nil, fmt.Errorf("epusdt apiBase must be an absolute HTTPS URL")
	}
	currency, err := payment.NormalizePaymentCurrency(config["currency"])
	if err != nil || (currency != "CNY" && currency != "USD") {
		return nil, fmt.Errorf("epusdt currency must be CNY or USD")
	}
	notifyURL, err := url.Parse(strings.TrimSpace(config["notifyUrl"]))
	if err != nil || notifyURL.Scheme != "https" || notifyURL.Hostname() == "" ||
		notifyURL.User != nil || notifyURL.Fragment != "" {
		return nil, fmt.Errorf("epusdt notifyUrl must be an absolute HTTPS URL")
	}
	pid := strings.TrimSpace(config["pid"])
	if !epusdtSafeValue(pid) || strings.TrimSpace(config["secretKey"]) == "" {
		return nil, fmt.Errorf("epusdt merchant PID and secret are required")
	}
	return &EPUSDT{
		baseURL:   strings.TrimRight(base.String(), "/"),
		pid:       pid,
		secret:    config["secretKey"],
		currency:  currency,
		notifyURL: notifyURL.String(),
		httpClient: &http.Client{
			Timeout: epusdtHTTPTimeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

// Name returns the payment adapter's administrator-facing product display name.
func (e *EPUSDT) Name() string { return "EPUSDT / GMPay" }

// ProviderKey returns the persisted identity used for GMPay provider dispatch.
func (e *EPUSDT) ProviderKey() string { return payment.TypeEPUSDT }

// SupportedTypes returns a fresh collection containing the cryptocurrency checkout method.
func (e *EPUSDT) SupportedTypes() []payment.PaymentType {
	return []payment.PaymentType{payment.TypeEPUSDT}
}

// MerchantIdentityMetadata returns merchant and fiat currency attributes for order validation.
func (e *EPUSDT) MerchantIdentityMetadata() map[string]string {
	return map[string]string{"pid": e.pid, "currency": e.currency}
}

// CreatePayment signs one hosted checkout request using the stored merchant number.
// Token and network are omitted together so the cashier selects the payment
// asset. Ambiguous transport or response failures return PAYMENT_CREATE_UNCONFIRMED,
// allowing the caller to retain its committed order for callback recovery.
// Returned merchant identity, fiat amount, currency, and cashier origin must
// match this request before its reference is accepted. The returned deadline
// comes from the gateway, which controls the cashier payment window.
func (e *EPUSDT) CreatePayment(ctx context.Context, req payment.CreatePaymentRequest) (*payment.CreatePaymentResponse, error) {
	amount, err := strconv.ParseFloat(req.Amount, 64)
	if err != nil || math.IsNaN(amount) || math.IsInf(amount, 0) || amount <= 0.01 ||
		!epusdtSafeValue(req.OrderID) || len(req.OrderID) > 32 {
		return nil, fmt.Errorf("epusdt checkout order or amount is invalid")
	}
	notifyURL := req.NotifyURL
	if notifyURL == "" {
		notifyURL = e.notifyURL
	}
	params := map[string]string{
		"pid": e.pid, "order_id": req.OrderID, "amount": req.Amount,
		"currency": strings.ToLower(e.currency), "notify_url": notifyURL,
		"redirect_url": req.ReturnURL, "name": req.Subject,
	}
	params["signature"] = epusdtSignature(params, e.secret)
	form := url.Values{}
	for key, value := range params {
		form.Set(key, value)
	}
	reply, status, err := e.request(ctx, http.MethodPost, "/payments/gmpay/v1/order/create-transaction", form.Encode())
	if err != nil {
		return nil, apperror.ServiceUnavailable(payment.CodePaymentCreateUnconfirmed, "payment creation could not be confirmed")
	}
	if reply.Code != http.StatusOK {
		if status >= 400 && status < 500 && reply.Code >= 10000 {
			return nil, fmt.Errorf("epusdt rejected checkout (code %d)", reply.Code)
		}
		return nil, apperror.ServiceUnavailable(payment.CodePaymentCreateUnconfirmed, "payment creation could not be confirmed")
	}
	paymentURL, parseErr := url.Parse(reply.Data.PaymentURL)
	baseURL, _ := url.Parse(e.baseURL)
	if parseErr != nil || paymentURL.Scheme != "https" || paymentURL.Host != baseURL.Host ||
		paymentURL.User != nil || !epusdtTradeReference(reply.Data.TradeID) ||
		reply.Data.OrderID != req.OrderID || !strings.EqualFold(reply.Data.Currency, e.currency) ||
		!epusdtEqualAmount(reply.Data.Amount.String(), req.Amount) ||
		(reply.Data.Status != epusdtWaiting && reply.Data.Status != epusdtSelecting) ||
		reply.Data.Expiration <= time.Now().Unix() {
		return nil, apperror.ServiceUnavailable(payment.CodePaymentCreateUnconfirmed, "payment creation response could not be validated")
	}
	// The gateway owns its payment deadline and returns creation timestamps in
	// seconds; persist that deadline so the local waiting screen follows its cashier.
	return &payment.CreatePaymentResponse{
		TradeNo:   reply.Data.TradeID,
		PayURL:    paymentURL.String(),
		Currency:  e.currency,
		ExpiresAt: time.Unix(reply.Data.Expiration, 0),
	}, nil
}

// QueryOrder observes the saved opaque gateway trade identifier over HTTPS.
// Cashier details provide fiat amount and currency, while the dedicated status
// endpoint supplies current payment progress. Missing or unrecognizable references
// remain errors, so an uncertain order cannot be silently classified as unpaid.
// Both responses must repeat the requested trade identifier, and invalid fiat
// amounts are rejected before the runtime can interpret a paid observation.
func (e *EPUSDT) QueryOrder(ctx context.Context, tradeNo string) (*payment.QueryOrderResponse, error) {
	if !epusdtTradeReference(tradeNo) {
		return nil, fmt.Errorf("epusdt gateway trade reference is unavailable")
	}
	details, _, err := e.request(ctx, http.MethodGet, "/pay/checkout-counter-resp/"+url.PathEscape(tradeNo), "")
	if err != nil || details.Code != http.StatusOK || details.Data.TradeID != tradeNo ||
		!strings.EqualFold(details.Data.Currency, e.currency) {
		return nil, fmt.Errorf("epusdt order details could not be confirmed")
	}
	current, _, err := e.request(ctx, http.MethodGet, "/pay/check-status/"+url.PathEscape(tradeNo), "")
	if err != nil || current.Code != http.StatusOK || current.Data.TradeID != tradeNo {
		return nil, fmt.Errorf("epusdt payment status could not be confirmed")
	}
	amount, err := details.Data.Amount.Float64()
	if err != nil || amount <= 0 || math.IsNaN(amount) || math.IsInf(amount, 0) {
		return nil, fmt.Errorf("epusdt order amount is invalid")
	}
	status := payment.ProviderStatusPending
	switch current.Data.Status {
	case epusdtPaid:
		status = payment.ProviderStatusSuccess
	case epusdtExpired:
		status = payment.ProviderStatusFailed
	case epusdtWaiting, epusdtSelecting:
	default:
		return nil, fmt.Errorf("epusdt payment status is unsupported")
	}
	return &payment.QueryOrderResponse{
		TradeNo:  tradeNo,
		Amount:   amount,
		Status:   status,
		Metadata: e.MerchantIdentityMetadata(),
	}, nil
}

// VerifyNotification authenticates GMPay scalar JSON before interpreting payment success.
// The signed amount is fiat money; actual_amount is a token quantity and
// never determines credited balance. Merchant currency comes from protected instance
// configuration because GMPay callbacks do not include a signed currency field.
// @project-doc docs/domains/payments_and_entitlements.md#epusdt_payments
func (e *EPUSDT) VerifyNotification(_ context.Context, rawBody string, _ map[string]string) (*payment.PaymentNotification, error) {
	params, err := epusdtNotificationParams(rawBody)
	if err != nil {
		return nil, err
	}
	expected := epusdtSignature(params, e.secret)
	if !hmac.Equal([]byte(expected), []byte(params["signature"])) || params["pid"] != e.pid {
		return nil, fmt.Errorf("epusdt notification signature is invalid")
	}
	amount, err := strconv.ParseFloat(params["amount"], 64)
	if err != nil || amount <= 0 || math.IsNaN(amount) || math.IsInf(amount, 0) ||
		params["status"] != "2" || !epusdtSafeValue(params["order_id"]) || !epusdtTradeReference(params["trade_id"]) {
		return nil, fmt.Errorf("epusdt notification payment fields are invalid")
	}
	return &payment.PaymentNotification{
		OrderID:  params["order_id"],
		TradeNo:  params["trade_id"],
		Amount:   amount,
		Status:   payment.ProviderStatusSuccess,
		RawData:  rawBody,
		Metadata: e.MerchantIdentityMetadata(),
	}, nil
}

// Refund rejects automatic transfers because GMPay exposes no merchant refund operation.
func (e *EPUSDT) Refund(_ context.Context, _ payment.RefundRequest) (*payment.RefundResponse, error) {
	return nil, fmt.Errorf("epusdt automatic refunds are not supported")
}

// request performs one gateway operation and decodes a bounded JSON envelope.
// Requests never follow redirects. Transport errors discard their original URL-bearing
// text because a signed creation form and merchant details must not appear
// in higher-level diagnostics; callers classify creation uncertainty separately.
func (e *EPUSDT) request(ctx context.Context, method, path, body string) (*epusdtReply, int, error) {
	req, err := http.NewRequestWithContext(ctx, method, e.baseURL+path, strings.NewReader(body))
	if err != nil {
		return nil, 0, fmt.Errorf("epusdt request could not be constructed")
	}
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	response, err := e.httpClient.Do(req)
	if err != nil {
		// Request errors can contain signed destinations and credentials. Return a
		// fixed diagnostic while the caller retains the local order for reconciliation.
		return nil, 0, fmt.Errorf("epusdt request could not be completed")
	}
	// Closing releases the response body on every parsing path. Close errors do
	// not replace the transport, body-read, or decoding result returned to callers.
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, epusdtMaxBody+1))
	if err != nil || len(data) > epusdtMaxBody || response.StatusCode >= 500 ||
		response.StatusCode < 200 || response.StatusCode >= 300 && response.StatusCode < 400 {
		return nil, response.StatusCode, fmt.Errorf("epusdt response could not be read")
	}
	var reply epusdtReply
	if err := json.Unmarshal(data, &reply); err != nil {
		return nil, response.StatusCode, fmt.Errorf("epusdt response is not valid JSON")
	}
	if response.StatusCode != http.StatusOK && reply.Code == http.StatusOK {
		return nil, response.StatusCode, fmt.Errorf("epusdt HTTP status conflicts with its response")
	}
	return &reply, response.StatusCode, nil
}

// epusdtSignature reproduces GMPay's sorted, nonempty scalar HMAC-SHA256 input.
// Callers canonicalize JSON numeric values before invoking this helper; form
// requests retain their exact transmitted scalar spelling during signing.
func epusdtSignature(params map[string]string, secret string) string {
	keys := make([]string, 0, len(params))
	for key, value := range params {
		if key != "signature" && value != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+params[key])
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(strings.Join(parts, "&")))
	return hex.EncodeToString(mac.Sum(nil))
}

// epusdtNotificationParams decodes an unambiguous scalar notification signature input.
// Numbers use the gateway's float64 fixed-format spelling and strings retain
// their received text. Required identities and paid status are checked by the
// caller after authentication; malformed fields return before signature comparison.
//
// Reading object tokens before assigning values detects duplicate JSON keys
// that ordinary map unmarshalling would overwrite. Unknown names and delimiter-bearing
// strings are rejected because the signing protocol joins unescaped key-value
// pairs; accepting arbitrary field content would allow distinct JSON objects
// to describe the same canonical string. Both representation checks protect
// the identity consumed later by the payment runtime.
func epusdtNotificationParams(raw string) (map[string]string, error) {
	allowed := map[string]bool{
		"pid": true, "trade_id": true, "order_id": true, "receive_address": true,
		"token": true, "block_transaction_id": true, "signature": true,
		"amount": true, "actual_amount": true, "status": true,
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return nil, fmt.Errorf("epusdt notification must be a JSON object")
	}
	params := make(map[string]string)
	for decoder.More() {
		keyToken, err := decoder.Token()
		key, valid := keyToken.(string)
		if err != nil || !valid || !allowed[key] {
			return nil, fmt.Errorf("epusdt notification field is unsupported")
		}
		if _, exists := params[key]; exists {
			return nil, fmt.Errorf("epusdt notification field is duplicated")
		}
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, fmt.Errorf("epusdt notification field is invalid")
		}
		numeric := key == "amount" || key == "actual_amount" || key == "status"
		if number, ok := value.(json.Number); ok && numeric {
			parsed, err := number.Float64()
			if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
				return nil, fmt.Errorf("epusdt notification number is invalid")
			}
			// GMPay signs decoded float64 values using fixed notation without trailing
			// zeros, so numeric JSON spelling alone cannot be used as signature input.
			params[key] = strconv.FormatFloat(parsed, 'f', -1, 64)
			continue
		}
		text, ok := value.(string)
		if !ok || numeric || text != "" && !epusdtSafeValue(text) {
			return nil, fmt.Errorf("epusdt notification scalar is invalid")
		}
		params[key] = text
	}
	if _, err := decoder.Token(); err != nil {
		return nil, fmt.Errorf("epusdt notification object is incomplete")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, fmt.Errorf("epusdt notification has trailing content")
	}
	return params, nil
}

// epusdtSafeValue rejects empty, ambiguous, or control-bearing protocol field text.
func epusdtSafeValue(value string) bool {
	return value != "" && strings.TrimSpace(value) == value &&
		!strings.ContainsAny(value, "&=") && !strings.ContainsFunc(value, unicode.IsControl)
}

// epusdtEqualAmount compares decimal fiat values without a token conversion or tolerance.
func epusdtEqualAmount(left, right string) bool {
	a, errA := decimal.NewFromString(left)
	b, errB := decimal.NewFromString(right)
	return errA == nil && errB == nil && a.Equal(b)
}

// epusdtTradeReference validates one opaque gateway identifier before constructing query paths.
func epusdtTradeReference(value string) bool {
	// Limit identifiers to the gateway namespace and a bounded URL segment.
	if value == "" || len(value) > 128 {
		return false
	}
	for _, ch := range value {
		if ch != '_' && ch != '-' && (ch < 'a' || ch > 'z') &&
			(ch < 'A' || ch > 'Z') && (ch < '0' || ch > '9') {
			return false
		}
	}
	return true
}
