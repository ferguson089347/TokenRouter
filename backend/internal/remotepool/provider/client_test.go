package provider

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/remotepool"
)

// client_test.go validates remote response decoding and credential handling against
// local HTTP servers. These cases exercise the transport separately from session
// state so redirects, oversized bodies, and source error messages cannot bypass
// the directory service's sanitized failure handling.

// TestRequestRejectsRedirectsAndSanitizesErrors returns a redirect containing
// source error text.
// The request must retain the redirect status, omit that text from the error,
// and avoid sending bearer credentials to the redirect target.
func TestRequestRejectsRedirectsAndSanitizesErrors(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("redirect target received credentials")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(target.Close)
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer secret-at", r.Header.Get("Authorization"))
		w.Header().Set("Location", target.URL)
		w.WriteHeader(http.StatusFound)
		_, _ = w.Write([]byte(`{"code":302,"message":"private-source-details"}`))
	}))
	t.Cleanup(source.Close)
	err := NewClient().Request(context.Background(), source.URL, "GET", "/account-pool", nil, "secret-at", new(any))
	var remote *remotepool.HTTPError
	require.True(t, errors.As(err, &remote))
	require.Equal(t, http.StatusFound, remote.Status)
	require.NotContains(t, err.Error(), "private-source-details")
}

// TestRequestValidatesEnvelopeAndSize serves successful, malformed, oversized,
// and failed responses.
// It verifies valid data decodes and unusable responses return bounded errors with
// remote authentication failures classified by their application status.
func TestRequestValidatesEnvelopeAndSize(t *testing.T) {
	for _, test := range []struct {
		name   string // response scenario label
		body   string // remote JSON response body
		fail   bool   // expected request failure
		status int    // expected application status
	}{
		{name: "success", body: `{"code":0,"data":{"id":25}}`},
		{name: "missing data", body: `{"code":0}`, fail: true},
		{name: "null data", body: `{"code":0,"data":null}`, fail: true},
		{name: "malformed", body: `bad-json`, fail: true},
		{name: "oversized", body: strings.Repeat("x", (2<<20)+1), fail: true},
		{name: "application auth", body: `{"code":401,"message":"private-token"}`, fail: true, status: 401},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(test.body)) }))
			t.Cleanup(server.Close)
			var out map[string]int
			err := NewClient().Request(context.Background(), server.URL, "GET", "/account-pool", nil, "", &out)
			if !test.fail {
				require.NoError(t, err)
				require.Equal(t, 25, out["id"])
				return
			}
			require.Error(t, err)
			require.NotContains(t, err.Error(), "private-token")
			if test.status != 0 {
				var remote *remotepool.HTTPError
				require.ErrorAs(t, err, &remote)
				require.Equal(t, test.status, remote.Status)
			}
		})
	}
}
