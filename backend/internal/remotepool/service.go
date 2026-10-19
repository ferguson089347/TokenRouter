package remotepool

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// service.go synchronizes remote account summaries and presents them through current
// local group permissions. Refresh tokens can rotate during collection, so each
// update holds the settings row lock until credentials and synchronization status
// are saved. A complete account snapshot replaces the previous snapshot only after
// every bound group and page succeeds. Failed collections retain the last complete
// snapshot together with the updated credentials and a warning.
//
// Source group identities remain in stored snapshots so every response can resolve
// current local names, multipliers, enabled state, and caller access. Account IDs
// shared by multiple source groups are merged before publication. Browser
// cancellation cannot interrupt the bounded persistence of a consumed refresh token.

// remotePoolSettingKey identifies the settings record shared by remote
// sessions, bindings, and complete snapshots.
const remotePoolSettingKey = "remote_account_pool"

// Repository reads settings and serializes read-modify-write callbacks across
// instances.
type Repository interface {
	// GetValue reads the saved record; absent keys return settings.ErrSettingNotFound.
	GetValue(context.Context, string) (string, error)
	// UpdateMultiple locks the supplied keys before invoking change and commits its
	// returned values atomically. Callback errors roll back all changes; callers keep
	// network operations bounded while the callback holds the database lock.
	UpdateMultiple(context.Context, []string, func(map[string]string) (map[string]string, error)) error
}

// SecretEncryptor encrypts stored session credentials and decodes saved ciphertext.
type SecretEncryptor interface {
	// Encrypt returns ciphertext suitable for saving in the settings record.
	Encrypt(string) (string, error)
	// Decrypt recovers plaintext credentials from a previously saved ciphertext value.
	Decrypt(string) (string, error)
}

// GroupSource supplies current local group configuration for bindings and visibility.
type GroupSource interface {
	// GetByIDLite reads the local group used to validate a submitted binding.
	GetByIDLite(context.Context, int64) (*routing.Group, error)
	// ListActive returns enabled groups for the administrator's current pool view.
	ListActive(context.Context) ([]routing.Group, error)
}

// Client exchanges bounded JSON requests with the configured remote instance.
type Client interface {
	// Request sends the optional bearer token to baseURL and decodes its response.
	// Implementations reject redirects and return HTTPError for unsuccessful
	// HTTP responses,
	// keeping remote response bodies out of caller-visible errors.
	Request(context.Context, string, string, string, any, string, any) error
}

// RemotePoolBinding maps one source group to one local display group.
type RemotePoolBinding struct {
	RemoteGroupID int64 `json:"remote_group_id"` // source group binding identifier
	LocalGroupID  int64 `json:"local_group_id"`  // local group binding identifier
}

// RemotePoolSettings reports saved configuration and session availability with
// credentials omitted.
type RemotePoolSettings struct {
	Enabled                bool                `json:"enabled"`                  // remote pool enabled flag
	BaseURL                string              `json:"base_url"`                 // normalized source site URL
	RefreshTokenConfigured bool                `json:"refresh_token_configured"` // saved refresh token present
	AccessTokenConfigured  bool                `json:"access_token_configured"`  // saved access token present
	AuthInvalid            bool                `json:"auth_invalid"`             // refresh credentials rejected remotely
	Bindings               []RemotePoolBinding `json:"bindings"`                 // source to local bindings
	ExpiresAt              *time.Time          `json:"expires_at,omitempty"`     // remote access token expiration
	SnapshotAt             *time.Time          `json:"snapshot_at,omitempty"`    // last complete collection time
	Warning                string              `json:"warning,omitempty"`        // sanitized synchronization warning message
}

// RemotePoolSettingsInput carries administrator configuration and an optional
// replacement refresh token.
type RemotePoolSettingsInput struct {
	Enabled bool   `json:"enabled"`  // remote pool enabled flag
	BaseURL string `json:"base_url"` // normalized source site URL
	// RefreshToken replaces the saved session when nonempty. Empty input retains
	// credentials for the same normalized source URL and fails enablement
	// after a site change.
	RefreshToken string              `json:"refresh_token"`
	Bindings     []RemotePoolBinding `json:"bindings"` // source to local bindings
}

// remotePoolCredentials holds the session values encrypted together in the
// settings record.
type remotePoolCredentials struct {
	AccessToken  string    `json:"access_token"`  // remote bearer access credential
	RefreshToken string    `json:"refresh_token"` // remote session refresh credential
	ExpiresAt    time.Time `json:"expires_at"`    // remote access token expiration
}

// RemotePoolSourceGroup identifies a source group in the administrator binding
// directory.
type RemotePoolSourceGroup struct {
	ID   int64  `json:"id"`   // source group directory identifier
	Name string `json:"name"` // group display name
}

// remotePoolRecord stores source configuration, encrypted sessions, and the
// latest complete snapshot.
type remotePoolRecord struct {
	Enabled      bool                    `json:"enabled"`               // remote pool enabled flag
	BaseURL      string                  `json:"base_url"`              // normalized source site URL
	Secrets      string                  `json:"secrets"`               // encrypted remote session JSON
	Bindings     []RemotePoolBinding     `json:"bindings"`              // source to local bindings
	AuthInvalid  bool                    `json:"auth_invalid"`          // refresh credentials rejected remotely
	Warning      string                  `json:"warning,omitempty"`     // sanitized synchronization warning message
	LastAttempt  time.Time               `json:"last_attempt"`          // most recent collection attempt
	SnapshotAt   *time.Time              `json:"snapshot_at,omitempty"` // last complete collection time
	Snapshot     []RemotePoolAccount     `json:"snapshot"`              // last complete account summaries
	SourceGroups []RemotePoolSourceGroup `json:"source_groups"`         // administrator source group directory
	// credentials is decrypted for an update and reencrypted into Secrets before
	// persistence; its unexported field keeps plaintext out of the record JSON.
	credentials remotePoolCredentials
}

// RemotePoolGroup describes a source group or a locally mapped display group.
type RemotePoolGroup struct {
	ID             int64   `json:"id"`              // source or local group identifier
	Name           string  `json:"name"`            // group display name
	RateMultiplier float64 `json:"rate_multiplier"` // group display cost multiplier
}

// RemotePoolPlan describes one available subscription tier for the pool filters.
type RemotePoolPlan struct {
	Value string `json:"value"` // source plan filter value
	Label string `json:"label"` // source provided display label
}

// RemotePoolOptions returns visible groups and plans with synchronization
// status information.
type RemotePoolOptions struct {
	Stale       bool              `json:"stale"`                 // previous snapshot being served
	AuthInvalid bool              `json:"auth_invalid"`          // refresh credentials rejected remotely
	SnapshotAt  *time.Time        `json:"snapshot_at,omitempty"` // last complete collection time
	Warning     string            `json:"warning,omitempty"`     // sanitized synchronization warning message
	Groups      []RemotePoolGroup `json:"groups"`                // currently visible group summaries
	PlanTypes   []RemotePoolPlan  `json:"plan_types"`            // visible account plan options
}

// RemotePoolUsageWindow reports the source utilization percentage for one
// usage period.
type RemotePoolUsageWindow struct {
	Key         string   `json:"key"`         // source usage window identifier
	Label       string   `json:"label"`       // source provided display label
	Utilization *float64 `json:"utilization"` // percent; null means unavailable
}

// RemotePoolAccount lists the allowed remote account fields presented through
// local group bindings.
type RemotePoolAccount struct {
	ID                 int64  `json:"id"`                  // stable source account identifier
	DisplayName        string `json:"display_name"`        // source account display name
	Platform           string `json:"platform"`            // source upstream platform identifier
	AuthType           string `json:"auth_type"`           // source authentication type label
	PlanType           string `json:"plan_type"`           // source subscription tier identifier
	PlanLabel          string `json:"plan_label"`          // source subscription tier name
	Concurrency        int    `json:"concurrency"`         // source concurrent request limit
	CurrentConcurrency int    `json:"current_concurrency"` // source concurrent requests occupied
	Status             string `json:"status"`              // source account availability status
	// Groups retains source IDs in saved snapshots and contains local summaries
	// after permission filtering. Each response resolves current local
	// names and multipliers.
	Groups       []RemotePoolGroup       `json:"groups"`        // source or local memberships
	UsageWindows []RemotePoolUsageWindow `json:"usage_windows"` // source usage period summaries
	// RateMultiplier and AccountRateEnabled describe display pricing. Collection
	// clears source rates, and List uses the first visible local group's multiplier
	// with account-specific pricing disabled for the directory response.
	RateMultiplier     float64    `json:"rate_multiplier"`      // local display cost multiplier
	AccountRateEnabled bool       `json:"account_rate_enabled"` // account specific multiplier flag
	LastUsedAt         *time.Time `json:"last_used_at"`         // source most recent use
}

// RemotePoolPage contains a filtered snapshot page and its synchronization status.
type RemotePoolPage struct {
	Stale       bool                `json:"stale"`                 // previous snapshot being served
	AuthInvalid bool                `json:"auth_invalid"`          // refresh credentials rejected remotely
	SnapshotAt  *time.Time          `json:"snapshot_at,omitempty"` // last complete collection time
	Warning     string              `json:"warning,omitempty"`     // sanitized synchronization warning message
	Items       []RemotePoolAccount `json:"items"`                 // visible filtered account summaries
	Total       int64               `json:"total"`                 // visible matching account count
	Page        int                 `json:"page"`                  // one based page number
	PageSize    int                 `json:"page_size"`             // maximum accounts per page
	Pages       int                 `json:"pages"`                 // total matching page count
}

// remotePoolTokens decodes session credentials returned by the remote refresh
// endpoint.
type remotePoolTokens struct {
	AccessToken  string `json:"access_token"`  // remote bearer access credential
	RefreshToken string `json:"refresh_token"` // remote session refresh credential
	ExpiresIn    int    `json:"expires_in"`    // access token lifetime seconds
}

// @project-doc docs/interfaces/remote_account_pool.md#remote_pool_session
// RemoteAccountPoolService coordinates remote sessions, complete snapshots,
// and local visibility checks.
type RemoteAccountPoolService struct {
	repo          Repository      // serialized pool record persistence
	groups        GroupSource     // current local group reader
	encryptor     SecretEncryptor // session secret encryption provider
	keyConfigured bool            // fixed encryption key configured
	client        Client          // remote JSON exchange provider
	// mu serializes callbacks within this instance while Repository.UpdateMultiple holds
	// the database row lock across instances and persists any rotated refresh
	// credentials.
	mu sync.Mutex
}

// HTTPError reports a remote HTTP failure without retaining sensitive response
// content.
type HTTPError struct {
	Status  int  // remote HTTP response status
	Captcha bool // source reports challenge requirement
}

// NewRemoteAccountPoolService constructs remote pool access using caller-owned
// persistence and transport.
// keyConfigured must describe a fixed encryption key before any configuration or
// session update can be saved; groups supplies current local display permissions.
func NewRemoteAccountPoolService(repo Repository, encryptor SecretEncryptor, keyConfigured bool, groups GroupSource, client Client) *RemoteAccountPoolService {
	return &RemoteAccountPoolService{repo: repo, encryptor: encryptor, keyConfigured: keyConfigured, groups: groups, client: client}
}

// decode decodes one saved record and decrypts its session credentials.
// Empty storage selects the disabled default site. Invalid JSON or undecryptable
// credentials fail before any remote request or modification can consume them.
func (s *RemoteAccountPoolService) decode(raw string) (*remotePoolRecord, error) {
	r := &remotePoolRecord{BaseURL: "https://aishenji.top"}
	if raw == "" {
		return r, nil
	}
	if json.Unmarshal([]byte(raw), r) != nil {
		return nil, errors.New("invalid remote account pool configuration")
	}
	if r.Secrets != "" {
		plain, err := s.encryptor.Decrypt(r.Secrets)
		if err != nil || json.Unmarshal([]byte(plain), &r.credentials) != nil {
			return nil, errors.New("cannot decrypt remote account pool credentials")
		}
	}
	return r, nil
}

// load reads and decodes the current persisted remote pool configuration.
// An absent record uses the disabled default site, while storage and decoding
// failures stop the operation before a remote session is used.
func (s *RemoteAccountPoolService) load(ctx context.Context) (*remotePoolRecord, error) {
	raw, err := s.repo.GetValue(ctx, remotePoolSettingKey)
	if err != nil && !errors.Is(err, settings.ErrSettingNotFound) {
		return nil, err
	}
	// Missing settings select the disabled default. Other storage failures
	// remain errors so corrupted or unavailable configuration cannot start
	// synchronization.
	return s.decode(raw)
}

// remotePoolSummary builds the administrator settings response with session
// secrets omitted.
// Missing bindings become an empty array, and a zero access-token expiration is
// omitted so the settings form can distinguish an unknown expiry.
func remotePoolSummary(r *remotePoolRecord) *RemotePoolSettings {
	result := &RemotePoolSettings{
		Enabled: r.Enabled, BaseURL: r.BaseURL, Bindings: r.Bindings,
		AccessTokenConfigured: r.credentials.AccessToken != "", RefreshTokenConfigured: r.credentials.RefreshToken != "",
		AuthInvalid: r.AuthInvalid, SnapshotAt: r.SnapshotAt, Warning: r.Warning,
	}
	if result.Bindings == nil {
		result.Bindings = []RemotePoolBinding{}
	}
	if !r.credentials.ExpiresAt.IsZero() {
		expiry := r.credentials.ExpiresAt
		result.ExpiresAt = &expiry
	}
	return result
}

// GetSettings returns configuration and session status for the administrator
// settings form.
func (s *RemoteAccountPoolService) GetSettings(ctx context.Context) (*RemotePoolSettings, error) {
	r, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	return remotePoolSummary(r), nil
}

// mutate serializes a settings update and saves credentials with its result.
// The repository holds a database row lock across change, so independent instances
// cannot consume the same refresh token concurrently. A false dirty result leaves
// the record untouched; callback errors abort persistence.
//
// A collection callback records network failures as warnings and returns success,
// allowing rotated credentials to commit alongside the last complete snapshot.
// Encryption failure prevents the record update and is returned to the caller.
func (s *RemoteAccountPoolService) mutate(ctx context.Context, change func(*remotePoolRecord) (bool, error)) (*remotePoolRecord, error) {
	// Serialize instance callbacks before acquiring the shared row lock so
	// configuration changes and token rotation read one committed predecessor.
	s.mu.Lock()
	defer s.mu.Unlock()
	var result *remotePoolRecord
	update := func(values map[string]string) (map[string]string, error) {
		r, err := s.decode(values[remotePoolSettingKey])
		if err != nil {
			return nil, err
		}
		dirty, err := change(r)
		if err != nil {
			return nil, err
		}
		result = r
		if !dirty {
			return map[string]string{}, nil
		}
		if !s.keyConfigured {
			return nil, apperror.BadRequest("REMOTE_POOL_ENCRYPTION_KEY_REQUIRED", "请先设置固定的 TOTP_ENCRYPTION_KEY，以便持久保存远端 RT/AT")
		}
		plain, err := json.Marshal(r.credentials)
		if err != nil {
			return nil, err
		}
		r.Secrets, err = s.encryptor.Encrypt(string(plain))
		if err != nil {
			return nil, errors.New("cannot encrypt remote account pool credentials")
		}
		raw, err := json.Marshal(r)
		if err != nil {
			return nil, err
		}
		return map[string]string{remotePoolSettingKey: string(raw)}, nil
	}
	err := s.repo.UpdateMultiple(ctx, []string{remotePoolSettingKey}, update)
	return result, err
}

// UpdateSettings validates source configuration and saves administrator group
// bindings.
// An empty refresh token retains the saved session; a replacement clears the
// access token and rejected-session marker while retaining a same-site snapshot.
//
// Changing the normalized source URL clears the session, account snapshot, and
// source group directory before applying the submitted token. Each source group
// can bind to one existing local group, and enabled configurations require a
// refresh token. Validation failure leaves the persisted record unchanged.
func (s *RemoteAccountPoolService) UpdateSettings(ctx context.Context, in RemotePoolSettingsInput) (*RemotePoolSettings, error) {
	u, err := url.Parse(strings.TrimSpace(in.BaseURL))
	if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" {
		return nil, apperror.BadRequest("REMOTE_POOL_INVALID_URL", "请输入有效的远端 HTTP(S) 站点地址")
	}
	// Accept site roots and API roots through one normalized prefix so refresh
	// and account endpoints retain any configured deployment subpath.
	u.Path = strings.TrimSuffix(strings.TrimRight(u.Path, "/"), "/api/v1")
	base := strings.TrimRight(u.String(), "/")
	seen := map[int64]bool{}
	for _, binding := range in.Bindings {
		if binding.RemoteGroupID <= 0 || binding.LocalGroupID <= 0 || seen[binding.RemoteGroupID] {
			return nil, apperror.BadRequest("REMOTE_POOL_INVALID_BINDING", "每个远端分组只能绑定一个有效本地分组")
		}
		seen[binding.RemoteGroupID] = true
		group, err := s.groups.GetByIDLite(ctx, binding.LocalGroupID)
		if err != nil || group == nil {
			return nil, apperror.BadRequest("REMOTE_POOL_INVALID_BINDING", "绑定的本地分组不存在")
		}
	}
	r, err := s.mutate(ctx, func(r *remotePoolRecord) (bool, error) {
		if r.BaseURL != base {
			// Changing the source site clears its session and snapshot together so
			// credentials and account identities cannot cross sites.
			r.credentials = remotePoolCredentials{}
			r.Snapshot = nil
			r.SnapshotAt = nil
			r.SourceGroups = nil
			r.AuthInvalid = false
			r.Warning = ""
		}
		r.Enabled = in.Enabled
		r.BaseURL = base
		r.Bindings = in.Bindings
		r.LastAttempt = time.Time{}
		if strings.TrimSpace(in.RefreshToken) != "" {
			r.credentials = remotePoolCredentials{RefreshToken: strings.TrimSpace(in.RefreshToken)}
			r.AuthInvalid = false
			r.Warning = ""
		}
		if r.Enabled && r.credentials.RefreshToken == "" {
			return false, apperror.BadRequest("REMOTE_POOL_RT_REQUIRED", "启用远端号池需要填写 RT")
		}
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	return remotePoolSummary(r), nil
}

// Error returns a fixed failure message that omits remote response content.
func (e *HTTPError) Error() string { return "remote account pool request failed" }

// remotePoolFailure maps transport failures to sanitized errors suitable for
// snapshot warnings.
// Remote status codes distinguish authentication, access, and throttling failures.
// Other errors use a fixed message so remote content and credentials stay private.
func remotePoolFailure(err error) error {
	if remote, ok := errors.AsType[*HTTPError](err); ok {
		if remote.Captcha || remote.Status == 403 {
			return apperror.New(502, "REMOTE_POOL_ACCESS_DENIED", "远端拒绝访问，请检查号池访问权限")
		}
		if remote.Status == 401 {
			return apperror.New(502, "REMOTE_POOL_AUTH_FAILED", "远端认证失败，请管理员更新 RT")
		}
		if remote.Status == 429 {
			return apperror.New(502, "REMOTE_POOL_RATE_LIMITED", "远端请求过于频繁，请稍后重试")
		}
	}
	return apperror.New(502, "REMOTE_POOL_REQUEST_FAILED", "远端请求失败，保留最近一次成功获取的数据")
}

// remotePoolTokenValid reports whether the access token remains usable for
// another read.
// A zero expiration permits reuse until the source rejects it; dated tokens need
// at least thirty seconds remaining before a request starts.
func remotePoolTokenValid(r *remotePoolRecord) bool {
	// Refresh shortly before expiration to leave time for the following source
	// request to begin with a usable access token.
	return r.credentials.AccessToken != "" && (r.credentials.ExpiresAt.IsZero() || time.Until(r.credentials.ExpiresAt) > 30*time.Second)
}

// refreshAccess exchanges the saved refresh token for current remote session
// credentials.
// Rejected refresh credentials mark the session invalid and stop automatic retries.
// Successful exchanges retain a rotated refresh token and apply bounded expiration
// values; callers save this mutation even when later account collection fails.
func (s *RemoteAccountPoolService) refreshAccess(ctx context.Context, r *remotePoolRecord) error {
	if r.AuthInvalid {
		return apperror.New(502, "REMOTE_POOL_RT_INVALID", "远端 RT 已失效，请管理员更新 RT；当前展示最后一次成功获取的数据")
	}
	var tokens remotePoolTokens
	err := s.client.Request(ctx, r.BaseURL, "POST", "/auth/refresh", map[string]string{"refresh_token": r.credentials.RefreshToken}, "", &tokens)
	if err != nil {
		var remote *HTTPError
		if errors.As(err, &remote) && (remote.Status == 400 || remote.Status == 401 || remote.Status == 403) {
			r.AuthInvalid = true
			r.credentials.AccessToken = ""
			return apperror.New(502, "REMOTE_POOL_RT_INVALID", "远端 RT 已失效，请管理员更新 RT；当前展示最后一次成功获取的数据")
		}
		return remotePoolFailure(err)
	}
	if tokens.AccessToken == "" {
		return apperror.New(502, "REMOTE_POOL_AUTH_FAILED", "远端刷新未返回 AT，请管理员检查 RT")
	}
	r.credentials.AccessToken = tokens.AccessToken
	if tokens.RefreshToken != "" {
		r.credentials.RefreshToken = tokens.RefreshToken
	}
	r.credentials.ExpiresAt = time.Time{}
	// Bound expiration to one year before converting source seconds to a
	// duration, preventing malformed lifetimes from extending session reuse
	// indefinitely.
	if tokens.ExpiresIn > 0 && tokens.ExpiresIn <= 31536000 {
		r.credentials.ExpiresAt = time.Now().Add(time.Duration(tokens.ExpiresIn) * time.Second)
	}
	return nil
}

// fetchWithSession reads a source endpoint using the saved or refreshed access token.
// A remote 401 refreshes the session and retries once. Callers persist any rotated
// credentials even when this final request fails and the snapshot remains unchanged.
func (s *RemoteAccountPoolService) fetchWithSession(ctx context.Context, r *remotePoolRecord, path string, out any) error {
	if !remotePoolTokenValid(r) {
		if err := s.refreshAccess(ctx, r); err != nil {
			return err
		}
	}
	err := s.client.Request(ctx, r.BaseURL, "GET", path, nil, r.credentials.AccessToken, out)
	var remote *HTTPError
	// A rejected access token can expire before its announced deadline. Refresh
	// once and repeat this read using the newly saved session values.
	if errors.As(err, &remote) && remote.Status == 401 {
		if err := s.refreshAccess(ctx, r); err != nil {
			return err
		}
		err = s.client.Request(ctx, r.BaseURL, "GET", path, nil, r.credentials.AccessToken, out)
	}
	if err != nil {
		return remotePoolFailure(err)
	}
	return nil
}

// snapshot returns the saved snapshot after a bounded, serialized
// synchronization attempt.
// Disabled configurations fail immediately. Rejected credentials and attempts within
// one minute reuse stored data, including its warning and last successful timestamp.
//
// Collection fetches every page for every bound source group, merging repeated
// account IDs and their memberships. Only a complete collection replaces the saved
// accounts. Failure still commits rotated credentials and a warning. The detached
// context permits persistence after browser cancellation within a sixty-second
// budget.
func (s *RemoteAccountPoolService) snapshot(ctx context.Context) (*remotePoolRecord, error) {
	current, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	if !current.Enabled {
		return nil, apperror.BadRequest("REMOTE_POOL_DISABLED", "远端号池尚未启用")
	}
	if current.AuthInvalid || time.Since(current.LastAttempt) < time.Minute {
		return current, nil
	}
	// A refresh token may already be consumed when the browser disconnects.
	// Complete this bounded update independently so the rotated token can be
	// saved.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
	defer cancel()
	return s.mutate(ctx, func(r *remotePoolRecord) (bool, error) {
		if !r.Enabled {
			return false, apperror.BadRequest("REMOTE_POOL_DISABLED", "远端号池尚未启用")
		}
		if r.AuthInvalid || time.Since(r.LastAttempt) < time.Minute {
			return false, nil
		}
		r.LastAttempt = time.Now()
		// Reserve five seconds for saving rotated credentials after the remote
		// collection budget expires, retaining the previous complete snapshot.
		syncCtx, cancel := context.WithTimeout(ctx, 55*time.Second)
		defer cancel()
		var remoteOptions RemotePoolOptions
		// Record source failures as data so the settings transaction can commit
		// any rotated refresh token while retaining its complete account snapshot.
		if err := s.fetchWithSession(syncCtx, r, "/account-pool/options", &remoteOptions); err != nil {
			r.Warning = poolWarning(err)
			return true, nil
		}
		if remoteOptions.Groups == nil {
			// An absent group array cannot describe a complete directory. Retain the
			// previous snapshot and report the invalid source response.
			r.Warning = "远端分组响应格式无效，保留最近一次成功获取的数据"
			return true, nil
		}
		r.SourceGroups = []RemotePoolSourceGroup{}
		for _, group := range remoteOptions.Groups {
			r.SourceGroups = append(r.SourceGroups, RemotePoolSourceGroup{ID: group.ID, Name: group.Name})
		}
		accounts := map[int64]RemotePoolAccount{}
		order := []int64{}
		for _, binding := range r.Bindings {
			for page := 1; page <= 100; page++ {
				var result RemotePoolPage
				query := url.Values{"page": {strconv.Itoa(page)}, "page_size": {"500"}, "group_id": {strconv.FormatInt(binding.RemoteGroupID, 10)}}
				// A later page may fail after session refresh. Save that credential
				// change and leave the previously published account collection intact.
				if err := s.fetchWithSession(syncCtx, r, "/account-pool?"+query.Encode(), &result); err != nil {
					r.Warning = poolWarning(err)
					return true, nil
				}
				if result.Items == nil || result.Total < 0 {
					// Missing items or negative totals make completion unverifiable, so
					// retain the previous complete collection with the refreshed session
					// and warning.
					r.Warning = "远端账号响应格式无效，保留最近一次成功获取的数据"
					return true, nil
				}
				for _, account := range result.Items {
					// Retain source group identities for later mapping. Local names,
					// rates, and current access are resolved on every response.
					found := false
					for i := range account.Groups {
						if account.Groups[i].ID == binding.RemoteGroupID {
							found = true
						}
						account.Groups[i].Name = ""
						account.Groups[i].RateMultiplier = 0
					}
					if !found {
						continue
					}
					account.RateMultiplier = 0
					account.AccountRateEnabled = false
					if previous, exists := accounts[account.ID]; exists {
						// Some sources return only the queried group. Merge previously
						// collected memberships so repeated account IDs retain every bound
						// group.
						seen := map[int64]bool{}
						for _, group := range account.Groups {
							seen[group.ID] = true
						}
						for _, group := range previous.Groups {
							if !seen[group.ID] {
								account.Groups = append(account.Groups, group)
							}
						}
					} else {
						order = append(order, account.ID)
					}
					accounts[account.ID] = account
				}
				if int64(page*500) >= result.Total {
					break
				}
				if len(result.Items) == 0 || page == 100 {
					// Empty intermediate pages or the page cap leave accounts
					// uncollected. Keep the saved snapshot available until every source
					// page has been collected.
					r.Warning = "远端分页数据不完整，保留最近一次成功获取的数据"
					return true, nil
				}
			}
		}
		next := make([]RemotePoolAccount, 0, len(order))
		for _, id := range order {
			next = append(next, accounts[id])
		}
		now := time.Now()
		r.Snapshot = next
		r.SnapshotAt = &now
		r.Warning = ""
		return true, nil
	})
}

// poolWarning extracts a sanitized application message for the saved
// synchronization warning.
// Unknown error types use fixed copy because their text may include request
// destinations or credentials that must stay out of user responses.
func poolWarning(err error) string {
	if app, ok := errors.AsType[*apperror.ApplicationError](err); ok {
		return app.Message
	}
	// Unknown failures use fixed copy because their raw text may contain source
	// URLs, credentials, or response content unsuitable for users.
	return "远端获取失败，保留最近一次成功获取的数据"
}

// visibleGroups maps source IDs to currently permitted and enabled local groups.
// A nil allowed slice requests the administrator view; a nonnil empty slice denies
// all groups. Local group names and multipliers are resolved for each response.
func (s *RemoteAccountPoolService) visibleGroups(ctx context.Context, r *remotePoolRecord, allowed []routing.Group) (map[int64]RemotePoolGroup, error) {
	permitted := map[int64]routing.Group{}
	if allowed == nil {
		groups, err := s.groups.ListActive(ctx)
		if err != nil {
			return nil, err
		}
		for _, group := range groups {
			permitted[group.ID] = group
		}
	} else {
		for _, group := range allowed {
			if group.IsActive() {
				permitted[group.ID] = group
			}
		}
	}
	result := map[int64]RemotePoolGroup{}
	for _, binding := range r.Bindings {
		if group, ok := permitted[binding.LocalGroupID]; ok {
			result[binding.RemoteGroupID] = RemotePoolGroup{ID: group.ID, Name: group.Name, RateMultiplier: group.RateMultiplier}
		}
	}
	return result, nil
}

// project maps saved source accounts to the caller's currently visible local groups.
// Accounts with no permitted binding disappear before filtering and counting.
// Multiple
// source memberships bound to one local group produce one local membership.
//
// The saved snapshot keeps source IDs so deletion, disablement, permission removal,
// and rebinding affect the next response. Each returned account receives a fresh
// group slice, local names, and local multipliers. Source account multipliers are
// replaced, including when an older snapshot is being served.
func (s *RemoteAccountPoolService) project(ctx context.Context, r *remotePoolRecord, allowed []routing.Group) ([]RemotePoolAccount, error) {
	groups, err := s.visibleGroups(ctx, r, allowed)
	if err != nil {
		return nil, err
	}
	items := []RemotePoolAccount{}
	for _, source := range r.Snapshot {
		account := source
		account.Groups = []RemotePoolGroup{}
		seen := map[int64]bool{}
		for _, remote := range source.Groups {
			if local, ok := groups[remote.ID]; ok && !seen[local.ID] {
				account.Groups = append(account.Groups, local)
				seen[local.ID] = true
			}
		}
		if len(account.Groups) == 0 {
			continue
		}
		account.RateMultiplier = account.Groups[0].RateMultiplier
		account.AccountRateEnabled = false
		items = append(items, account)
	}
	return items, nil
}

// List filters and paginates the complete snapshot after applying current
// local access.
// Query group IDs identify local groups. Missing pagination selects page one and
// fifty items; page sizes outside one through five hundred use the default.
func (s *RemoteAccountPoolService) List(ctx context.Context, query url.Values, allowed []routing.Group) (*RemotePoolPage, error) {
	r, err := s.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	items, err := s.project(ctx, r, allowed)
	if err != nil {
		return nil, err
	}
	filtered := []RemotePoolAccount{}
	groupID, _ := strconv.ParseInt(query.Get("group_id"), 10, 64)
	search := strings.ToLower(query.Get("search"))
	for _, item := range items {
		if search != "" && !strings.Contains(strings.ToLower(item.DisplayName), search) && !strings.Contains(strconv.FormatInt(item.ID, 10), search) {
			continue
		}
		if plan := query.Get("plan_type"); plan != "" && item.PlanType != plan {
			continue
		}
		if status := query.Get("status"); status != "" && item.Status != status {
			continue
		}
		matched := groupID == 0
		for _, group := range item.Groups {
			if group.ID == groupID {
				matched = true
			}
		}
		if matched {
			filtered = append(filtered, item)
		}
	}
	page, _ := strconv.Atoi(query.Get("page"))
	if page < 1 {
		page = 1
	}
	size, _ := strconv.Atoi(query.Get("page_size"))
	if size < 1 || size > 500 {
		size = 50
	}
	start := min((page-1)*size, len(filtered))
	end := min(start+size, len(filtered))
	return &RemotePoolPage{Items: filtered[start:end], Total: int64(len(filtered)), Page: page, PageSize: size, Pages: (len(filtered) + size - 1) / size, SnapshotAt: r.SnapshotAt, Stale: r.Warning != "", AuthInvalid: r.AuthInvalid, Warning: r.Warning}, nil
}

// Options returns local group choices and plan choices from visible snapshot
// accounts.
// Local groups are deduplicated across bindings, and plans are sorted by value.
// Snapshot warnings accompany these options even when remote refresh is unavailable.
func (s *RemoteAccountPoolService) Options(ctx context.Context, allowed []routing.Group) (*RemotePoolOptions, error) {
	r, err := s.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	items, err := s.project(ctx, r, allowed)
	if err != nil {
		return nil, err
	}
	mapped, err := s.visibleGroups(ctx, r, allowed)
	if err != nil {
		return nil, err
	}
	result := &RemotePoolOptions{Groups: []RemotePoolGroup{}, PlanTypes: []RemotePoolPlan{}, SnapshotAt: r.SnapshotAt, Stale: r.Warning != "", AuthInvalid: r.AuthInvalid, Warning: r.Warning}
	seen := map[int64]bool{}
	for _, binding := range r.Bindings {
		if group, ok := mapped[binding.RemoteGroupID]; ok && !seen[group.ID] {
			result.Groups = append(result.Groups, group)
			seen[group.ID] = true
		}
	}
	plans := map[string]string{}
	for _, item := range items {
		plans[item.PlanType] = item.PlanLabel
	}
	keys := []string{}
	for key := range plans {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		result.PlanTypes = append(result.PlanTypes, RemotePoolPlan{Value: key, Label: plans[key]})
	}
	return result, nil
}

// SourceGroups returns the saved source group directory for administrator
// binding configuration.
// It uses the same bounded synchronization as account reads. An absent directory
// returns an empty slice, and callers restrict this operation to administrators.
func (s *RemoteAccountPoolService) SourceGroups(ctx context.Context) ([]RemotePoolSourceGroup, error) {
	r, err := s.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	if r.SourceGroups == nil {
		return []RemotePoolSourceGroup{}, nil
	}
	return r.SourceGroups, nil
}
