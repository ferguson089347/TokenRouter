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
	service := NewRemoteAccountPoolService(repo, cipher{}, true, groups, sourceClient{call: call}, "test-site-identity")
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
	other := NewRemoteAccountPoolService(repo, cipher{}, true, groups, s.client, "test-site-identity")
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
			return remotePoolSourcePage{Items: []RemotePoolAccount{{ID: 1, Groups: []RemotePoolGroup{{ID: 25}}}}, Total: 1}, nil
		}
	})
	other := NewRemoteAccountPoolService(repo, cipher{}, true, groups, s.client, "test-site-identity")
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
			return remotePoolSourcePage{Items: []RemotePoolAccount{{ID: id, Groups: []RemotePoolGroup{{ID: 25}}}}, Total: total}, nil
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
			return remotePoolSourcePage{Items: []RemotePoolAccount{{ID: 1, Groups: []RemotePoolGroup{{ID: 25}}}}, Total: 1}, nil
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
				return remotePoolSourcePage{Items: []RemotePoolAccount{{ID: 2, DisplayName: "Basic two", PlanType: "basic", PlanLabel: "Basic", Status: "paused", Groups: []RemotePoolGroup{{ID: 27}}}}, Total: 1}, nil
			}
			return remotePoolSourcePage{Items: []RemotePoolAccount{{ID: 1, DisplayName: "Pro one", PlanType: "pro", PlanLabel: "Pro", Status: "active", Groups: []RemotePoolGroup{{ID: id}}}}, Total: 1}, nil
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
	require.Equal(t, s.accountReference("https://pool.example", 2), page.Items[0].ID)
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
			return remotePoolSourcePage{Page: page, PageSize: 100, Pages: 6, Total: 501, Items: items}, nil
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
	require.Equal(t, s.accountReference("https://pool.example", 501), last.Items[0].ID)
	require.Equal(t, 26, last.Pages)
	require.EqualValues(t, 6, reads.Load())
}

// TestRemotePoolGroupStatisticsRespectAccessFiltersAndPagination serves a saved
// snapshot with shared accounts and two remote bindings for one local group.
// Totals cover every matching page, protect private groups, and distinguish
// active status from occupied or unlimited concurrency slots.
func TestRemotePoolGroupStatisticsRespectAccessFiltersAndPagination(t *testing.T) {
	s, _, groups := poolFixture(t, func(context.Context, string, string, any, string) (any, error) {
		t.Fatal("fresh snapshot statistics must not request remote data")
		return nil, nil
	})
	groups.groups = append(groups.groups,
		routing.Group{ID: 200, Name: "Shared group", RateMultiplier: 3, Status: routing.StatusActive},
		routing.Group{ID: 300, Name: "Private group", RateMultiplier: 4, Status: routing.StatusActive},
	)
	_, err := s.mutate(context.Background(), func(record *remotePoolRecord) (bool, error) {
		now := time.Now()
		record.LastAttempt = now
		record.SnapshotAt = &now
		record.Warning = "retained snapshot"
		record.Bindings = []RemotePoolBinding{{25, 100}, {26, 100}, {27, 200}, {28, 300}}
		record.Snapshot = []RemotePoolAccount{
			{ID: 1, PlanType: "pro", Status: "active", Concurrency: 10, CurrentConcurrency: 10, Groups: []RemotePoolGroup{{ID: 25}, {ID: 26}, {ID: 27}, {ID: 28}}},
			{ID: 2, PlanType: "free", Status: "paused", Concurrency: 4, CurrentConcurrency: 2, Groups: []RemotePoolGroup{{ID: 25}}},
			{ID: 3, PlanType: "pro", Status: "active", Concurrency: 0, CurrentConcurrency: 5, Groups: []RemotePoolGroup{{ID: 27}}},
			{ID: 4, PlanType: "pro", Status: "active", Concurrency: 100, CurrentConcurrency: 90, Groups: []RemotePoolGroup{{ID: 28}}},
		}
		return true, nil
	})
	require.NoError(t, err)
	allowed := groups.groups[:2]
	page, err := s.List(context.Background(), url.Values{"page_size": {"1"}}, allowed)
	require.NoError(t, err)
	require.EqualValues(t, 3, page.Total)
	require.Len(t, page.Items, 1)
	require.True(t, page.Stale)
	require.Equal(t, []RemotePoolGroupStats{
		{RemotePoolGroup: RemotePoolGroup{ID: 100, Name: "Local group", RateMultiplier: 2.5}, TotalAccounts: 2, AvailableAccounts: 1, CurrentConcurrency: 12, Concurrency: 14},
		{RemotePoolGroup: RemotePoolGroup{ID: 200, Name: "Shared group", RateMultiplier: 3}, TotalAccounts: 2, AvailableAccounts: 2, CurrentConcurrency: 15, Concurrency: 10, UnlimitedAccounts: 1},
	}, page.GroupStats)
	second, err := s.List(context.Background(), url.Values{"page_size": {"1"}, "page": {"2"}}, allowed)
	require.NoError(t, err)
	require.Equal(t, page.GroupStats, second.GroupStats)
	for _, scenario := range []struct {
		name     string     // selected list filter scenario
		query    url.Values // filters applied before group totals
		accounts int        // expected matching account count
		groups   int        // expected returned group count
		active   int        // expected first group active count
	}{
		{"local group", url.Values{"group_id": {"100"}}, 2, 1, 1},
		{"active pro", url.Values{"status": {"active"}, "plan_type": {"pro"}}, 2, 2, 1},
		{"paused", url.Values{"status": {"paused"}}, 1, 1, 0},
		{"account reference", url.Values{"search": {s.accountReference("https://pool.example", 1)}}, 1, 2, 1},
		{"private group", url.Values{"group_id": {"300"}}, 0, 0, 0},
		{"empty match", url.Values{"search": {"no-match"}}, 0, 0, 0},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			filtered, err := s.List(context.Background(), scenario.query, allowed)
			require.NoError(t, err)
			require.EqualValues(t, scenario.accounts, filtered.Total)
			require.Len(t, filtered.GroupStats, scenario.groups)
			if len(filtered.GroupStats) > 0 {
				require.Equal(t, scenario.active, filtered.GroupStats[0].AvailableAccounts)
			}
		})
	}
	denied, err := s.List(context.Background(), nil, []routing.Group{})
	require.NoError(t, err)
	require.Empty(t, denied.GroupStats)
	raw, err := json.Marshal(denied)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"group_stats":[]`)
	_, err = s.mutate(context.Background(), func(record *remotePoolRecord) (bool, error) {
		record.Snapshot[2].CurrentConcurrency = -5
		record.Snapshot[2].Concurrency = -1
		return true, nil
	})
	require.NoError(t, err)
	page, err = s.List(context.Background(), url.Values{"group_id": {"200"}}, allowed)
	require.NoError(t, err)
	require.EqualValues(t, 10, page.GroupStats[0].CurrentConcurrency)
	require.Equal(t, 1, page.GroupStats[0].UnlimitedAccounts)
}

// TestRemotePoolPublicAccountReferences serves a cached source identity containing
// its original numeric ID and generated name. Public rows, search, and restarts
// retain site-local references while the persisted synchronization snapshot keeps
// the source identity needed for future collection and group deduplication.
func TestRemotePoolPublicAccountReferences(t *testing.T) {
	s, repo, groups := poolFixture(t, func(context.Context, string, string, any, string) (any, error) {
		t.Fatal("cached snapshots must not contact the source")
		return nil, nil
	})
	_, err := s.mutate(context.Background(), func(record *remotePoolRecord) (bool, error) {
		record.LastAttempt = time.Now()
		record.Snapshot = []RemotePoolAccount{{ID: 58248, DisplayName: "promax Account #58248", Platform: "openai", PlanType: "promax", PlanLabel: "ProMax", Groups: []RemotePoolGroup{{ID: 25}}}}
		return true, nil
	})
	require.NoError(t, err)
	page, err := s.List(context.Background(), url.Values{}, nil)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	reference := page.Items[0].ID
	require.Regexp(t, `^AC-[A-Z2-7]{13}$`, reference)
	require.Equal(t, "ProMax", page.Items[0].DisplayName)
	encoded, err := json.Marshal(page)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "58248")
	require.NotContains(t, string(encoded), "promax Account #")
	var public struct {
		Items []struct {
			ID string `json:"id"` // public account reference
		} `json:"items"` // visible account summaries
	}
	require.NoError(t, json.Unmarshal(encoded, &public))
	require.Equal(t, reference, public.Items[0].ID)
	for _, query := range []string{reference, strings.ToLower(reference), "promax"} {
		result, err := s.List(context.Background(), url.Values{"search": {query}}, nil)
		require.NoError(t, err)
		require.EqualValues(t, 1, result.Total)
	}
	result, err := s.List(context.Background(), url.Values{"search": {"58248"}}, nil)
	require.NoError(t, err)
	require.Zero(t, result.Total)
	restarted := NewRemoteAccountPoolService(repo, cipher{}, true, groups, s.client, "test-site-identity")
	result, err = restarted.List(context.Background(), url.Values{}, nil)
	require.NoError(t, err)
	require.Equal(t, reference, result.Items[0].ID)
	another := NewRemoteAccountPoolService(repo, cipher{}, true, groups, s.client, "another-site-identity")
	result, err = another.List(context.Background(), url.Values{}, nil)
	require.NoError(t, err)
	require.NotEqual(t, reference, result.Items[0].ID)
	require.NotEqual(t, reference, s.accountReference("https://another.example", 58248))
	record, err := s.load(context.Background())
	require.NoError(t, err)
	require.EqualValues(t, 58248, record.Snapshot[0].ID)
	require.Equal(t, "promax Account #58248", record.Snapshot[0].DisplayName)
}
