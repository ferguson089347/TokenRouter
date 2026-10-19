package remotepool

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// service_test.go exercises remote session rotation, complete snapshot publication,
// and local group visibility through the service's public operations. Shared memory
// storage models serialized record callbacks, while scripted source replies reproduce
// failures after refresh credentials have already rotated.

// memoryRepository models serialized settings callbacks shared by multiple
// service instances.
type memoryRepository struct {
	mu    sync.Mutex // saved settings access lock
	value string     // saved remote pool JSON
}

// groupSource supplies mutable local group configuration for visibility assertions.
type groupSource struct {
	groups []routing.Group // local group test records
}

// cipher encodes test credentials so serialized public responses can be
// checked separately.
type cipher struct{}

// sourceClient scripts source operations and exchanges results through JSON decoding.
type sourceClient struct {
	call func(context.Context, string, string, any, string) (any, error) // scripted remote endpoint behavior
}

// GetValue returns the latest saved record under the shared fixture mutex.
func (r *memoryRepository) GetValue(context.Context, string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.value, nil
}

// UpdateMultiple serializes callback execution and commits its returned
// setting value.
// Errors leave the previous record untouched. Both service instances share this
// fixture so concurrent refresh tests exercise the service's second freshness check.
func (r *memoryRepository) UpdateMultiple(_ context.Context, _ []string, change func(map[string]string) (map[string]string, error)) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	values, err := change(map[string]string{remotePoolSettingKey: r.value})
	if err != nil {
		return err
	}
	if value, ok := values[remotePoolSettingKey]; ok {
		r.value = value
	}
	return nil
}

// ListActive returns the fixture groups used by administrator visibility checks.
func (g *groupSource) ListActive(context.Context) ([]routing.Group, error) {
	groups := []routing.Group{}
	for _, group := range g.groups {
		if group.IsActive() {
			groups = append(groups, group)
		}
	}
	return groups, nil
}

// GetByIDLite returns a configured local group or the shared not-found error.
func (g *groupSource) GetByIDLite(_ context.Context, id int64) (*routing.Group, error) {
	for _, group := range g.groups {
		if group.ID == id {
			return &group, nil
		}
	}
	return nil, settings.ErrSettingNotFound
}

// Encrypt encodes fixture credentials before they enter the saved settings record.
func (cipher) Encrypt(value string) (string, error) {
	return base64.StdEncoding.EncodeToString([]byte(value)), nil
}

// Decrypt decodes credentials stored by the fixture's matching Encrypt
// implementation.
func (cipher) Decrypt(value string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(value)
	return string(raw), err
}

// Request invokes the scripted source and decodes its result into the service
// destination.
// Source errors bypass decoding, letting tests distinguish rejected credentials from
// temporary failures occurring after a successful refresh token exchange.
func (s sourceClient) Request(ctx context.Context, _ string, method, path string, payload any, token string, out any) error {
	value, err := s.call(ctx, method, path, payload, token)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

// poolFixture creates an enabled pool with one bound local group and scripted source.
// The same repository and source can be passed to another service instance to
// exercise cross-instance serialization and reads after a process restart.
func poolFixture(t *testing.T, call func(context.Context, string, string, any, string) (any, error)) (*RemoteAccountPoolService, *memoryRepository, *groupSource) {
	t.Helper()
	repo := &memoryRepository{}
	groups := &groupSource{groups: []routing.Group{{ID: 100, Name: "Local group", RateMultiplier: 2.5, Status: routing.StatusActive}}}
	service := NewRemoteAccountPoolService(repo, cipher{}, true, groups, sourceClient{call: call})
	_, err := service.UpdateSettings(context.Background(), RemotePoolSettingsInput{Enabled: true, BaseURL: "https://pool.example", RefreshToken: "rt-first", Bindings: []RemotePoolBinding{{RemoteGroupID: 25, LocalGroupID: 100}}})
	require.NoError(t, err)
	return service, repo, groups
}

// expireSession makes the next read synchronize again with an expired access token.
// It updates the same saved record as production operations so subsequent service
// instances observe the requested expiration and retain the previous complete
// snapshot.
func expireSession(t *testing.T, s *RemoteAccountPoolService) {
	t.Helper()
	_, err := s.mutate(context.Background(), func(record *remotePoolRecord) (bool, error) {
		record.LastAttempt = time.Time{}
		record.credentials.ExpiresAt = time.Now().Add(-time.Minute)
		return true, nil
	})
	require.NoError(t, err)
}

// TestRemotePoolSessionAndVisibility rotates credentials before serving a
// locally mapped account.
// It verifies stale reads retain the snapshot after RT rejection and recheck current
// group names, rates, enabled state, and user access before counting accounts.
func TestRemotePoolSessionAndVisibility(t *testing.T) {
	var invalid atomic.Bool
	var refreshes atomic.Int32
	s, repo, groups := poolFixture(t, func(_ context.Context, method, path string, payload any, token string) (any, error) {
		switch path {
		case "/auth/refresh":
			refreshes.Add(1)
			if invalid.Load() {
				return nil, &HTTPError{Status: 401}
			}
			require.Equal(t, "POST", method)
			values, ok := payload.(map[string]string)
			require.True(t, ok)
			require.Equal(t, "rt-first", values["refresh_token"])
			return remotePoolTokens{AccessToken: "at-next", RefreshToken: "rt-next", ExpiresIn: 3600}, nil
		case "/account-pool/options":
			require.Equal(t, "at-next", token)
			if invalid.Load() {
				return nil, &HTTPError{Status: 401}
			}
			return RemotePoolOptions{Groups: []RemotePoolGroup{{ID: 25, Name: "Source secret group", RateMultiplier: 0.1}}}, nil
		default:
			require.True(t, strings.HasPrefix(path, "/account-pool?"))
			return map[string]any{"total": 1, "items": []map[string]any{{"id": 1, "display_name": "Account one", "status": "active", "plan_type": "pro", "plan_label": "Pro", "rate_multiplier": 0.1, "credentials": "source-secret", "groups": []RemotePoolGroup{{ID: 25, Name: "Source secret group", RateMultiplier: 0.1}}}}}, nil
		}
	})
	ctx := context.Background()
	page, err := s.List(ctx, url.Values{}, nil)
	require.NoError(t, err)
	require.EqualValues(t, 1, page.Total)
	require.Equal(t, 2.5, page.Items[0].RateMultiplier)
	raw, err := json.Marshal(page)
	require.NoError(t, err)
	for _, secret := range []string{"Source secret group", "source-secret", "at-next", "rt-next"} {
		require.NotContains(t, string(raw), secret)
	}
	saved, err := s.GetSettings(ctx)
	require.NoError(t, err)
	raw, err = json.Marshal(saved)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "rt-next")
	invalid.Store(true)
	expireSession(t, s)
	page, err = s.List(ctx, url.Values{}, nil)
	require.NoError(t, err)
	require.True(t, page.AuthInvalid)
	require.True(t, page.Stale)
	require.Len(t, page.Items, 1)
	other := NewRemoteAccountPoolService(repo, cipher{}, true, groups, s.client)
	page, err = other.List(ctx, url.Values{}, nil)
	require.NoError(t, err)
	require.True(t, page.AuthInvalid)
	require.EqualValues(t, 2, refreshes.Load())
	groups.groups[0].Name = "Renamed local"
	groups.groups[0].RateMultiplier = 4
	page, err = other.List(ctx, url.Values{}, nil)
	require.NoError(t, err)
	require.Equal(t, "Renamed local", page.Items[0].Groups[0].Name)
	require.Equal(t, 4.0, page.Items[0].RateMultiplier)
	page, err = other.List(ctx, url.Values{}, []routing.Group{})
	require.NoError(t, err)
	require.Zero(t, page.Total)
	page, err = other.List(ctx, url.Values{"group_id": {"25"}}, nil)
	require.NoError(t, err)
	require.Zero(t, page.Total)
	groups.groups[0].Status = routing.StatusDisabled
	page, err = other.List(ctx, url.Values{}, nil)
	require.NoError(t, err)
	require.Zero(t, page.Total)
}

// TestRemotePoolConcurrentInstances synchronizes two instances against the
// same saved record.
// Concurrent readers must consume the refresh token once and observe the complete
// snapshot produced by the first admitted update.
func TestRemotePoolConcurrentInstances(t *testing.T) {
	var refreshes atomic.Int32
	s, repo, groups := poolFixture(t, func(_ context.Context, _ string, path string, _ any, _ string) (any, error) {
		switch path {
		case "/auth/refresh":
			refreshes.Add(1)
			return remotePoolTokens{AccessToken: "at", RefreshToken: "rt-next", ExpiresIn: 3600}, nil
		case "/account-pool/options":
			return RemotePoolOptions{Groups: []RemotePoolGroup{}}, nil
		default:
			return RemotePoolPage{Items: []RemotePoolAccount{{ID: 1, Groups: []RemotePoolGroup{{ID: 25}}}}, Total: 1}, nil
		}
	})
	other := NewRemoteAccountPoolService(repo, cipher{}, true, groups, s.client)
	var wg sync.WaitGroup
	for _, instance := range []*RemoteAccountPoolService{s, other, s, other} {
		wg.Go(func() {
			_, err := instance.List(context.Background(), url.Values{}, nil)
			require.NoError(t, err)
		})
	}
	wg.Wait()
	require.EqualValues(t, 1, refreshes.Load())
}

// TestRemotePoolPartialCollectionRetainsRotatedToken fails the second page
// after a successful refresh.
// It verifies the previous full snapshot remains visible while the newly rotated
// refresh credential is saved for the next synchronization attempt.
func TestRemotePoolPartialCollectionRetainsRotatedToken(t *testing.T) {
	var fail atomic.Bool
	s, _, _ := poolFixture(t, func(_ context.Context, _ string, path string, _ any, _ string) (any, error) {
		switch path {
		case "/auth/refresh":
			token := "rt-first"
			if fail.Load() {
				token = "rt-second"
			}
			return remotePoolTokens{AccessToken: "at", RefreshToken: token, ExpiresIn: 3600}, nil
		case "/account-pool/options":
			return RemotePoolOptions{Groups: []RemotePoolGroup{}}, nil
		default:
			if fail.Load() && strings.Contains(path, "page=2&") {
				return nil, &HTTPError{Status: 503}
			}
			id, total := int64(1), int64(1)
			if fail.Load() {
				id, total = 2, 501
			}
			return RemotePoolPage{Items: []RemotePoolAccount{{ID: id, Groups: []RemotePoolGroup{{ID: 25}}}}, Total: total}, nil
		}
	})
	first, err := s.List(context.Background(), url.Values{}, nil)
	require.NoError(t, err)
	fail.Store(true)
	expireSession(t, s)
	page, err := s.List(context.Background(), url.Values{}, nil)
	require.NoError(t, err)
	require.True(t, page.Stale)
	require.False(t, page.AuthInvalid)
	require.Equal(t, first.Items, page.Items)
	record, err := s.load(context.Background())
	require.NoError(t, err)
	require.Equal(t, "rt-second", record.credentials.RefreshToken)
}

// TestRemotePoolSettingsValidation rejects invalid bindings, URLs, and missing
// fixed encryption keys.
// It verifies token replacement resets rejected-session state while a source-site
// change clears credentials and accounts belonging to the previous source.
func TestRemotePoolSettingsValidation(t *testing.T) {
	s, _, _ := poolFixture(t, func(context.Context, string, string, any, string) (any, error) { return nil, errors.New("unavailable") })
	ctx := context.Background()
	for _, base := range []string{"file:///tmp/pool", "https://user:pass@pool.example", "https://pool.example?token=secret", "https://pool.example/#fragment"} {
		_, err := s.UpdateSettings(ctx, RemotePoolSettingsInput{BaseURL: base})
		require.Error(t, err)
	}
	for _, bindings := range [][]RemotePoolBinding{{{RemoteGroupID: 25, LocalGroupID: 999}}, {{RemoteGroupID: 25, LocalGroupID: 100}, {RemoteGroupID: 25, LocalGroupID: 100}}} {
		_, err := s.UpdateSettings(ctx, RemotePoolSettingsInput{Enabled: true, BaseURL: "https://pool.example", Bindings: bindings})
		require.Error(t, err)
	}
	_, err := s.mutate(ctx, func(r *remotePoolRecord) (bool, error) {
		r.AuthInvalid = true
		r.Snapshot = []RemotePoolAccount{{ID: 1}}
		return true, nil
	})
	require.NoError(t, err)
	value, err := s.UpdateSettings(ctx, RemotePoolSettingsInput{Enabled: true, BaseURL: "https://pool.example/api/v1/", RefreshToken: "replacement"})
	require.NoError(t, err)
	require.False(t, value.AuthInvalid)
	record, err := s.load(ctx)
	require.NoError(t, err)
	require.Len(t, record.Snapshot, 1)
	_, err = s.UpdateSettings(ctx, RemotePoolSettingsInput{Enabled: true, BaseURL: "https://other.example"})
	require.Error(t, err)
	_, err = s.UpdateSettings(ctx, RemotePoolSettingsInput{Enabled: true, BaseURL: "https://other.example", RefreshToken: "different"})
	require.NoError(t, err)
	record, err = s.load(ctx)
	require.NoError(t, err)
	require.Empty(t, record.Snapshot)
	s.keyConfigured = false
	_, err = s.UpdateSettings(ctx, RemotePoolSettingsInput{BaseURL: "https://other.example"})
	require.ErrorContains(t, err, "TOTP_ENCRYPTION_KEY")
}

// TestRemotePoolBrowserCancellation cancels the browser context after a token
// refresh starts.
// It verifies the bounded collection continues with a detached context and commits
// the rotated refresh token alongside the completed account snapshot.
func TestRemotePoolBrowserCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, _, _ := poolFixture(t, func(syncCtx context.Context, _ string, path string, _ any, _ string) (any, error) {
		switch path {
		case "/auth/refresh":
			cancel()
			require.NoError(t, syncCtx.Err())
			return remotePoolTokens{AccessToken: "at", RefreshToken: "rotated", ExpiresIn: 3600}, nil
		case "/account-pool/options":
			require.NoError(t, syncCtx.Err())
			return RemotePoolOptions{Groups: []RemotePoolGroup{}}, nil
		default:
			return RemotePoolPage{Items: []RemotePoolAccount{{ID: 1, Groups: []RemotePoolGroup{{ID: 25}}}}, Total: 1}, nil
		}
	})
	page, err := s.List(ctx, url.Values{}, nil)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	record, err := s.load(context.Background())
	require.NoError(t, err)
	require.Equal(t, "rotated", record.credentials.RefreshToken)
}

// TestRemotePoolMergesMembershipsBeforeFiltering receives repeated source accounts under separate bindings.
// It verifies local groups are deduplicated, pagination counts visible accounts once,
// and plan choices are limited to accounts accessible through the caller's groups.
func TestRemotePoolMergesMembershipsBeforeFiltering(t *testing.T) {
	s, _, groups := poolFixture(t, func(_ context.Context, _ string, path string, _ any, _ string) (any, error) {
		switch path {
		case "/auth/refresh":
			return remotePoolTokens{AccessToken: "at", RefreshToken: "next", ExpiresIn: 3600}, nil
		case "/account-pool/options":
			return RemotePoolOptions{Groups: []RemotePoolGroup{}}, nil
		default:
			u, err := url.Parse(path)
			require.NoError(t, err)
			id := int64(25)
			if u.Query().Get("group_id") == "26" {
				id = 26
			}
			if u.Query().Get("group_id") == "27" {
				return RemotePoolPage{Items: []RemotePoolAccount{{ID: 2, DisplayName: "Basic two", PlanType: "basic", PlanLabel: "Basic", Status: "paused", Groups: []RemotePoolGroup{{ID: 27}}}}, Total: 1}, nil
			}
			return RemotePoolPage{Items: []RemotePoolAccount{{ID: 1, DisplayName: "Pro one", PlanType: "pro", PlanLabel: "Pro", Status: "active", Groups: []RemotePoolGroup{{ID: id}}}}, Total: 1}, nil
		}
	})
	groups.groups = append(groups.groups, routing.Group{ID: 200, Name: "Exclusive local", Status: routing.StatusActive, RateMultiplier: 3})
	_, err := s.UpdateSettings(context.Background(), RemotePoolSettingsInput{Enabled: true, BaseURL: "https://pool.example", Bindings: []RemotePoolBinding{{RemoteGroupID: 25, LocalGroupID: 100}, {RemoteGroupID: 26, LocalGroupID: 200}, {RemoteGroupID: 27, LocalGroupID: 100}}})
	require.NoError(t, err)
	page, err := s.List(context.Background(), url.Values{"page_size": {"1"}}, nil)
	require.NoError(t, err)
	require.EqualValues(t, 2, page.Total)
	require.Len(t, page.Items, 1)
	require.Len(t, page.Items[0].Groups, 2)
	page, err = s.List(context.Background(), url.Values{"page_size": {"1"}, "page": {"2"}}, nil)
	require.NoError(t, err)
	require.EqualValues(t, 2, page.Items[0].ID)
	page, err = s.List(context.Background(), url.Values{"search": {"pro"}, "status": {"active"}, "plan_type": {"pro"}, "group_id": {"100"}}, nil)
	require.NoError(t, err)
	require.EqualValues(t, 1, page.Total)
	choices, err := s.Options(context.Background(), []routing.Group{groups.groups[1]})
	require.NoError(t, err)
	require.Len(t, choices.Groups, 1)
	require.EqualValues(t, 200, choices.Groups[0].ID)
	require.Equal(t, []RemotePoolPlan{{Value: "pro", Label: "Pro"}}, choices.PlanTypes)
}

// TestRemotePoolCollectsSourceClampedPages models a source that caps requests at
// one hundred rows despite a five-hundred-row request. All five hundred and one
// accounts remain available, including the final account beyond the first two pages.
func TestRemotePoolCollectsSourceClampedPages(t *testing.T) {
	var reads atomic.Int32
	s, _, _ := poolFixture(t, func(_ context.Context, _ string, path string, _ any, _ string) (any, error) {
		switch path {
		case "/auth/refresh":
			return remotePoolTokens{AccessToken: "at", RefreshToken: "next", ExpiresIn: 3600}, nil
		case "/account-pool/options":
			return RemotePoolOptions{Groups: []RemotePoolGroup{}}, nil
		default:
			reads.Add(1)
			parsed, err := url.Parse(path)
			require.NoError(t, err)
			require.Equal(t, "500", parsed.Query().Get("page_size"))
			page, err := strconv.Atoi(parsed.Query().Get("page"))
			require.NoError(t, err)
			items := []RemotePoolAccount{}
			for id := (page-1)*100 + 1; id <= min(page*100, 501); id++ {
				items = append(items, RemotePoolAccount{ID: int64(id), DisplayName: strconv.Itoa(id), Groups: []RemotePoolGroup{{ID: 25}}})
			}
			return RemotePoolPage{Page: page, PageSize: 100, Pages: 6, Total: 501, Items: items}, nil
		}
	})
	page, err := s.List(context.Background(), url.Values{}, nil)
	require.NoError(t, err)
	require.EqualValues(t, 501, page.Total)
	require.Equal(t, 20, page.PageSize)
	require.Len(t, page.Items, 20)
	require.EqualValues(t, 6, reads.Load())
	last, err := s.List(context.Background(), url.Values{"page": {"26"}}, nil)
	require.NoError(t, err)
	require.Len(t, last.Items, 1)
	require.EqualValues(t, 501, last.Items[0].ID)
	require.Equal(t, 26, last.Pages)
	require.EqualValues(t, 6, reads.Load())
}
