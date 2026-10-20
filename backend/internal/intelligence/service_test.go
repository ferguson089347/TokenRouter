package intelligence

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/intelligence/remote"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// intelligenceRepoStub records persisted observations and injected lease
// outcomes in memory.
type intelligenceRepoStub struct {
	// IntelligenceRepository supplies unimplemented fixture methods. Expected operations are
	// overridden below; unexpected calls fail immediately through nil embedding.
	IntelligenceRepository
	config   *IntelligenceConfig // current fixture configuration
	run      *IntelligenceRun    // current fixture observation
	runs     []IntelligenceRun   // fixture observation history
	saves    []IntelligenceRun   // captured persisted observations
	denySave bool                // injected lease rejection
	nextPoll time.Time           // captured poll deadline
}

// intelligenceKeyStub supplies one current personal Key for credential checks.
type intelligenceKeyStub struct {
	// KeyReader supplies unimplemented fixture methods. Expected operations are
	// overridden below; unexpected calls fail immediately through nil embedding.
	KeyReader
	key *apikey.APIKey // supplied test credential
}

// intelligenceGroupStub supplies current group status and visible group
// choices.
type intelligenceGroupStub struct {
	// GroupReader supplies unimplemented fixture methods. Expected operations are
	// overridden below; unexpected calls fail immediately through nil embedding.
	GroupReader
	group   *routing.Group  // current fixture group
	visible []routing.Group // authorized fixture groups
}

// intelligenceClientStub records creation and polling calls with injected
// remote outcomes.
type intelligenceClientStub struct {
	creates, gets int                                                    // creates: creation attempt count; gets: polling attempt count
	create        func(remote.CreateRequest) (*remote.TestResult, error) // injected creation callback
	result        *remote.TestResult                                     // remote fixture observation
	err           error                                                  // injected fixture failure
}

// GetConfig returns the fixture configuration or the module not-found error.
func (r *intelligenceRepoStub) GetConfig(context.Context, int64) (*IntelligenceConfig, error) {
	if r.config == nil {
		return nil, ErrIntelligenceNotFound
	}
	c := *r.config
	return &c, nil
}

// ListConfigs returns configured fixture records for administrator and user
// list checks.
func (r *intelligenceRepoStub) ListConfigs(context.Context) ([]IntelligenceConfig, error) {
	if r.config == nil {
		return []IntelligenceConfig{}, nil
	}
	return []IntelligenceConfig{*r.config}, nil
}

// SaveConfig records the submitted configuration for subsequent fixture reads.
func (r *intelligenceRepoStub) SaveConfig(_ context.Context, c *IntelligenceConfig) error {
	copy := *c
	r.config = &copy
	return nil
}

// SaveRun captures a copied observation unless cancellation or injected lease
// rejection prevents writing. The fixture records next polling time for later
// assertions about retry behavior.
func (r *intelligenceRepoStub) SaveRun(ctx context.Context, run *IntelligenceRun, next time.Time, _ bool) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if r.denySave {
		return false, nil
	}
	copy := *run
	r.run = &copy
	r.nextPoll = next
	r.saves = append(r.saves, copy)
	return true, nil
}

// ListRuns returns a detached history slice containing the fixture
// observations.
func (r *intelligenceRepoStub) ListRuns(context.Context, int64) ([]IntelligenceRun, error) {
	return append([]IntelligenceRun{}, r.runs...), nil
}

// GetRun returns the fixture observation or the module not-found error.
func (r *intelligenceRepoStub) GetRun(context.Context, string) (*IntelligenceRun, error) {
	if r.run == nil {
		return nil, ErrIntelligenceNotFound
	}
	copy := *r.run
	return &copy, nil
}

// CreateRun returns a queued fixture observation with the requested
// configuration identity.
func (r *intelligenceRepoStub) CreateRun(_ context.Context, id int64, now time.Time) (*IntelligenceRun, error) {
	return &IntelligenceRun{ID: "local", ConfigID: id, Status: "queued", CreatedAt: now}, nil
}

// GetByID returns the current fixture value for the supplied identity.
func (r intelligenceKeyStub) GetByID(context.Context, int64) (*apikey.APIKey, error) {
	return r.key, nil
}

// GetByKey resolves the supplied secret against the fixture Key value.
func (r intelligenceKeyStub) GetByKey(_ context.Context, key string) (*apikey.APIKey, error) {
	if r.key != nil && r.key.Key == key {
		return r.key, nil
	}
	return nil, apikey.ErrAPIKeyNotFound
}

// GetByID returns the current fixture value for the supplied identity.
func (r intelligenceGroupStub) GetByID(context.Context, int64) (*routing.Group, error) {
	return r.group, nil
}

// GetAvailableGroups returns the current fixture list of visible routing
// groups.
func (r intelligenceGroupStub) GetAvailableGroups(context.Context, int64) ([]routing.Group, error) {
	return r.visible, nil
}

// Create records a creation attempt and invokes its injected fixture response.
func (c *intelligenceClientStub) Create(_ context.Context, in remote.CreateRequest, _ string) (*remote.TestResult, error) {
	c.creates++
	if c.create != nil {
		return c.create(in)
	}
	return c.result, c.err
}

// Get records a poll attempt and returns its injected fixture observation.
func (c *intelligenceClientStub) Get(context.Context, string) (*remote.TestResult, error) {
	c.gets++
	return c.result, c.err
}

// newIntelligenceFixture constructs isolated quality-check dependencies with
// an ordinary personal Key. DNS validation succeeds locally, and remote
// behavior is injected through an in-memory client for each scenario.
func newIntelligenceFixture() (*IntelligenceService, *intelligenceRepoStub, *intelligenceClientStub, *apikey.APIKey) {
	gid := int64(7)
	// Model ordinary Key creation with unavailable-group routing enabled, so
	// eligibility checks exercise the default saved configuration used by
	// administrators.
	key := &apikey.APIKey{ID: 9, Key: "sk-privatekey1234567890123456", GroupID: &gid, Status: "active", FallbackWhenGroupUnavailable: true, User: &identity.User{Status: "active"}}
	cfg := &IntelligenceConfig{ID: 1, GroupID: gid, GroupName: "测试组", APIKeyID: key.ID, Model: "gpt-test", Benchmark: "candy", BaseURL: "https://gateway.example/v1", Protocol: "responses", Enabled: true, IntervalMinutes: 60}
	repo := &intelligenceRepoStub{config: cfg}
	client := &intelligenceClientStub{}
	groups := intelligenceGroupStub{group: &routing.Group{ID: gid, Name: "测试组", Status: "active"}, visible: []routing.Group{{ID: gid}}}
	s := NewIntelligenceService(repo, intelligenceKeyStub{key: key}, groups, groups, client, func(context.Context) bool { return true }, func(string) error { return nil })
	s.resolvePublicHost = func(string) error { return nil }
	return s, repo, client, key
}

// intelligenceInput creates a valid administrator configuration using the
// supplied fixture secret.
func intelligenceInput(key string) IntelligenceConfigInput {
	return IntelligenceConfigInput{GroupID: 7, Model: "gpt-test", Benchmark: "candy", BaseURL: "https://gateway.example/custom/v1/", APIKey: key, Protocol: "responses", Enabled: true, IntervalMinutes: 60}
}

// intelligenceQueued creates a recently queued local job with a fixture lease.
func intelligenceQueued() *IntelligenceRun {
	return &IntelligenceRun{ID: "iq_local", ConfigID: 1, GroupID: 7, Model: "gpt-test", Benchmark: "candy", Status: "queued", Verdict: "pending", LeaseToken: "lease", CreatedAt: time.Now().UTC()}
}

// TestIntelligenceConfigurationCredentialAndProtocolBoundaries rejects invalid
// credentials, private URLs, unsupported protocols and out-of-range schedules.
// Valid creation and editing return credential-free configuration responses
// while retaining the saved Key reference.
func TestIntelligenceConfigurationCredentialAndProtocolBoundaries(t *testing.T) {
	t.Run("密钥只保存引用且空值保留", func(t *testing.T) {
		s, repo, _, key := newIntelligenceFixture()
		saved, err := s.SaveConfig(context.Background(), 0, intelligenceInput(key.Key))
		require.NoError(t, err)
		require.Equal(t, key.ID, repo.config.APIKeyID)
		require.Equal(t, "https://gateway.example/custom/v1", saved.BaseURL)
		raw, err := json.Marshal(saved)
		require.NoError(t, err)
		require.NotContains(t, string(raw), key.Key)
		require.NotContains(t, string(raw), "api_key_id")
		_, err = s.SaveConfig(context.Background(), 1, intelligenceInput(""))
		require.NoError(t, err)
	})
	for _, tc := range []struct {
		name   string               // fixture scenario name
		mutate func(*apikey.APIKey) // scenario input mutation
	}{
		{"团队", func(k *apikey.APIKey) { id := int64(1); k.TeamID = &id }},
		{"复合", func(k *apikey.APIKey) { k.IsComposite = true }},
		{"复合映射", func(k *apikey.APIKey) { k.CompositeGroups = []apikey.APIKeyCompositeGroup{{GroupID: 7, Prefix: "a"}} }},
		{"错组", func(k *apikey.APIKey) { id := int64(8); k.GroupID = &id }},
		{"无绑定分组", func(k *apikey.APIKey) { k.GroupID = nil }},
		{"停用", func(k *apikey.APIKey) { k.Status = "disabled" }},
		{"过期", func(k *apikey.APIKey) { v := time.Now().Add(-time.Hour); k.ExpiresAt = &v }},
		{"超额", func(k *apikey.APIKey) { k.Quota = 1; k.QuotaUsed = 1 }},
		{"系统托管", func(k *apikey.APIKey) { managedBy := "creative_studio"; k.ManagedBy = &managedBy }},
		{"禁用用户", func(k *apikey.APIKey) { k.User.Status = "disabled" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, repo, client, key := newIntelligenceFixture()
			tc.mutate(key)
			_, err := s.SaveConfig(context.Background(), 0, intelligenceInput(key.Key))
			require.Error(t, err)
			_, err = s.Run(context.Background(), 1)
			require.Error(t, err)
			// Exercise credential and group failures alongside routing preference, keeping
			// invalid Keys blocked before any remote model submission can occur.
			s.execute(context.Background(), intelligenceQueued())
			require.Zero(t, client.creates)
			require.Equal(t, "error", repo.run.Status)
		})
	}
	for _, tc := range []struct {
		name   string                         // fixture scenario name
		mutate func(*IntelligenceConfigInput) // scenario input mutation
	}{
		{"糖果不能Chat", func(v *IntelligenceConfigInput) { v.Protocol = "chat_completions" }}, {"画图不能ultra", func(v *IntelligenceConfigInput) { v.Benchmark = "drawing"; v.ReasoningEffort = "ultra" }}, {"无default档", func(v *IntelligenceConfigInput) { v.ServiceTier = "default" }}, {"禁私网", func(v *IntelligenceConfigInput) { v.BaseURL = "https://127.0.0.1/v1" }}, {"禁userinfo", func(v *IntelligenceConfigInput) { v.BaseURL = "https://secret@gateway.example/v1" }}, {"禁query", func(v *IntelligenceConfigInput) { v.BaseURL = "https://gateway.example/v1?key=secret" }}, {"间隔下限", func(v *IntelligenceConfigInput) { v.IntervalMinutes = 4 }}, {"间隔上限", func(v *IntelligenceConfigInput) { v.IntervalMinutes = 10081 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _, key := newIntelligenceFixture()
			in := intelligenceInput(key.Key)
			tc.mutate(&in)
			_, err := s.SaveConfig(context.Background(), 0, in)
			require.Error(t, err)
		})
	}
	t.Run("禁止历史换组或换模型", func(t *testing.T) {
		s, _, _, key := newIntelligenceFixture()
		in := intelligenceInput(key.Key)
		in.Model = "other"
		_, err := s.SaveConfig(context.Background(), 1, in)
		require.Error(t, err)
	})
}

// TestIntelligenceOrdinaryKeyFallbackDoesNotBlockDetection covers both
// unavailable-group routing preferences on personal ordinary Keys. Eligible
// credentials reach remote creation, while inactive groups and other invalid
// credentials still prevent submission.
func TestIntelligenceOrdinaryKeyFallbackDoesNotBlockDetection(t *testing.T) {
	for _, tc := range []struct {
		name     string // fixture scenario name
		fallback bool   // unavailable-group routing preference
	}{
		{"默认开启自动降级", true},
		{"关闭自动降级", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, repo, client, key := newIntelligenceFixture()
			key.FallbackWhenGroupUnavailable = tc.fallback
			saved, err := s.SaveConfig(context.Background(), 1, intelligenceInput(key.Key))
			require.NoError(t, err)
			require.Equal(t, key.ID, saved.APIKeyID)
			run, err := s.Run(context.Background(), saved.ID)
			require.NoError(t, err)
			require.Equal(t, "queued", run.Status)
			client.create = func(in remote.CreateRequest) (*remote.TestResult, error) {
				// The remote tester receives the configured Key unchanged, letting ordinary
				// gateway routing apply its saved unavailable-group preference.
				require.Equal(t, key.Key, in.APIKey)
				require.Equal(t, saved.Model, in.Model)
				return &remote.TestResult{ID: "remote", Benchmark: "candy", Status: "running"}, nil
			}
			s.execute(context.Background(), intelligenceQueued())
			require.Equal(t, 1, client.creates)
			require.Equal(t, "running", repo.run.Status)
			require.Equal(t, saved.GroupID, *key.GroupID)
			require.Equal(t, tc.fallback, key.FallbackWhenGroupUnavailable)
		})
		t.Run(tc.name+"仍阻止停用分组提交", func(t *testing.T) {
			s, repo, client, key := newIntelligenceFixture()
			key.FallbackWhenGroupUnavailable = tc.fallback
			s.groups = intelligenceGroupStub{group: &routing.Group{ID: 7, Status: "disabled"}}
			_, err := s.SaveConfig(context.Background(), 1, intelligenceInput(key.Key))
			require.Error(t, err)
			s.execute(context.Background(), intelligenceQueued())
			require.Zero(t, client.creates)
			require.Equal(t, "error", repo.run.Status)
			require.Equal(t, "分组不可用", repo.run.ErrorMessage)
		})
	}
}

// TestIntelligenceCreateOnceAfterDurableState observes the submitting record
// before the remote creation call begins. Losing the lease blocks submission,
// and successful acceptance records the remote polling identity for recovery.
func TestIntelligenceCreateOnceAfterDurableState(t *testing.T) {
	s, repo, client, _ := newIntelligenceFixture()
	run := intelligenceQueued()
	repo.config.Benchmark = "drawing"
	run.Benchmark = "drawing"
	client.create = func(in remote.CreateRequest) (*remote.TestResult, error) {
		require.Equal(t, "pelican", in.Benchmark)
		require.Equal(t, "submitting", repo.run.Status)
		require.Empty(t, repo.run.RemoteID)
		return &remote.TestResult{ID: "remote", Benchmark: "pelican", Status: "running"}, nil
	}
	s.execute(context.Background(), run)
	require.Equal(t, 1, client.creates)
	require.Equal(t, "running", repo.run.Status)
	require.Equal(t, "remote", repo.run.RemoteID)
	client.result = &remote.TestResult{ID: "remote", Benchmark: "pelican", Status: "succeeded", Assessment: &remote.Assessment{Quality: "good"}}
	s.execute(context.Background(), run)
	require.Equal(t, 1, client.creates)
	require.Equal(t, 1, client.gets)
}

// TestIntelligenceRecoveryDoesNotResubmit resumes submitting and running
// records with different recovery identities. Unknown acceptance terminates
// safely, while known remote tasks are polled without issuing another creation
// request.
func TestIntelligenceRecoveryDoesNotResubmit(t *testing.T) {
	t.Run("已获远端ID即使关闭也必须持久化", func(t *testing.T) {
		s, repo, client, _ := newIntelligenceFixture()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		client.create = func(remote.CreateRequest) (*remote.TestResult, error) {
			cancel()
			return &remote.TestResult{ID: "remote-after-cancel", Benchmark: "candy", Status: "running"}, nil
		}
		s.execute(ctx, intelligenceQueued())
		require.Equal(t, "running", repo.run.Status)
		require.Equal(t, "remote-after-cancel", repo.run.RemoteID)
		require.Equal(t, 1, client.creates)
	})
	t.Run("提交崩溃未有ID", func(t *testing.T) {
		s, repo, client, _ := newIntelligenceFixture()
		run := intelligenceQueued()
		run.Status = "submitting"
		s.execute(context.Background(), run)
		require.Zero(t, client.creates)
		require.Equal(t, "unknown", repo.run.Status)
	})
	t.Run("租约比较失败不发POST", func(t *testing.T) {
		s, repo, client, _ := newIntelligenceFixture()
		repo.denySave = true
		s.execute(context.Background(), intelligenceQueued())
		require.Zero(t, client.creates)
	})
	t.Run("总开关关闭阻止未提交", func(t *testing.T) {
		s, repo, client, _ := newIntelligenceFixture()
		s.enabled = func(context.Context) bool { return false }
		s.execute(context.Background(), intelligenceQueued())
		require.Zero(t, client.creates)
		require.Equal(t, "error", repo.run.Status)
		_, err := s.Run(context.Background(), 1)
		require.ErrorIs(t, err, ErrIntelligenceDisabled)
	})
	t.Run("总开关关闭仍完成已提交GET", func(t *testing.T) {
		s, repo, client, _ := newIntelligenceFixture()
		s.enabled = func(context.Context) bool { return false }
		run := intelligenceQueued()
		run.Status = "running"
		run.RemoteID = "remote"
		client.result = &remote.TestResult{ID: "remote", Status: "succeeded", Benchmark: "candy", Candy: &remote.CandyResult{Status: "passed", Answer: "21"}}
		s.execute(context.Background(), run)
		require.Zero(t, client.creates)
		require.Equal(t, 1, client.gets)
		require.Equal(t, "completed", repo.run.Status)
		require.Equal(t, "passed", repo.run.Verdict)
	})
}

// TestIntelligenceSubmissionFailureDoesNotRetry injects rejected, uncertain,
// oversized and timed-out creation responses. The resulting local diagnostics
// remain safe and recovery never repeats a potentially accepted paid
// submission.
func TestIntelligenceSubmissionFailureDoesNotRetry(t *testing.T) {
	for _, tc := range []struct {
		name        string // fixture scenario name
		err         error  // injected fixture failure
		wantStatus  string // expected execution status
		wantMessage string // expected diagnostic fragment
	}{
		{"HTTP400拒绝创建", &remote.HTTPError{StatusCode: 400}, "error", "检测服务拒绝创建任务（HTTP 400）"},
		{"HTTP429拒绝创建", &remote.HTTPError{StatusCode: 429}, "error", "检测服务拒绝创建任务（HTTP 429）"},
		{"HTTP503受理未知", &remote.HTTPError{StatusCode: 503}, "unknown", "检测服务创建接口异常（HTTP 503）"},
		{"提交超时受理未知", &remote.TransportError{Timeout: true}, "unknown", "连接检测服务超时"},
		{"原始错误不能回显", errors.New("https://private.example/v1 sk-privatekey1234567890123456 remote-capability"), "unknown", "检测提交未成功确认"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, repo, client, key := newIntelligenceFixture()
			client.err = tc.err
			s.execute(context.Background(), intelligenceQueued())
			require.Equal(t, 1, client.creates)
			require.Zero(t, client.gets)
			require.Len(t, repo.saves, 2)
			require.Equal(t, "submitting", repo.saves[0].Status)
			require.Equal(t, tc.wantStatus, repo.run.Status)
			require.Equal(t, tc.wantStatus, repo.run.Verdict)
			require.Contains(t, repo.run.ErrorMessage, tc.wantMessage)
			require.Contains(t, repo.run.ErrorMessage, "未自动重试")
			require.True(t, repo.run.HasDetail)
			require.NotNil(t, repo.run.FinishedAt)
			for _, private := range []string{"https://private.example", key.Key, "remote-capability"} {
				require.NotContains(t, repo.run.ErrorMessage, private)
			}
			// Resume after the durable submitting write to confirm diagnostic
			// classification cannot cause a potentially accepted creation request to
			// repeat.
			interrupted := repo.saves[0]
			s.execute(context.Background(), &interrupted)
			require.Equal(t, 1, client.creates)
			require.Zero(t, client.gets)
			require.Equal(t, "unknown", repo.run.Status)
		})
	}
}

// TestIntelligenceResultPrivacyAndUnknownVerdict applies remote observations
// containing credentials, task identifiers and incomplete evaluation. Saved
// content removes secrets, keeps execution failure separate from unknown
// assessment, and retains usable drawing artifacts.
func TestIntelligenceResultPrivacyAndUnknownVerdict(t *testing.T) {
	s, repo, _, key := newIntelligenceFixture()
	run := intelligenceQueued()
	run.RemoteID = "remote-capability"
	result := &remote.TestResult{ID: run.RemoteID, Status: "succeeded", Benchmark: "candy", Phase: "https://secret/" + key.Key, Candy: &remote.CandyResult{Status: "passed", Question: key.Key, Answer: run.RemoteID + " sk-deletedoldkey1234567890"}}
	s.applyResult(context.Background(), run, result, key.Key)
	require.Equal(t, "passed", repo.run.Verdict)
	raw, err := json.Marshal(repo.run)
	require.NoError(t, err)
	require.NotContains(t, string(raw), key.Key)
	require.NotContains(t, string(raw), run.RemoteID)
	require.NotContains(t, string(raw), "sk-deletedoldkey")
	require.Empty(t, repo.run.Phase)
	run = intelligenceQueued()
	run.RemoteID = "remote"
	s.applyResult(context.Background(), run, &remote.TestResult{ID: "remote", Status: "succeeded", Benchmark: "candy"}, "")
	require.NotEqual(t, "passed", repo.run.Verdict)
	run = intelligenceQueued()
	run.Benchmark = "drawing"
	run.RemoteID = "remote"
	s.applyResult(context.Background(), run, &remote.TestResult{ID: "remote", Status: "succeeded", Benchmark: "pelican", Assessment: &remote.Assessment{Quality: "good"}}, "")
	require.Equal(t, "unknown", repo.run.Status)
	require.Equal(t, "unknown", repo.run.Verdict)
	require.Contains(t, repo.run.ErrorMessage, "未能给出明确的画图评判")
}

// TestIntelligenceDecodedResultPersistsOutcomeAndSafeDiagnostic decodes remote
// JSON before applying its result to a claimed run. This protects
// classification and persistence together, including nested quota errors,
// unknown assessment and redacted diagnostics.
func TestIntelligenceDecodedResultPersistsOutcomeAndSafeDiagnostic(t *testing.T) {
	// Decode protocol JSON before applying the observation so the test exercises
	// error parsing together with classification and persistence.
	for _, tc := range []struct {
		name         string // fixture scenario name
		benchmark    string // selected benchmark identifier
		payload      string // remote observation JSON
		wantStatus   string // expected execution status
		wantVerdict  string // expected assessment verdict
		wantMessage  string // expected diagnostic fragment
		wantDetail   bool   // expected detail availability
		wantArtifact bool   // expected artwork retention
	}{
		{
			name: "糖果通过", benchmark: "candy",
			payload:    `{"benchmark":"candy","status":"succeeded","candy":{"status":"passed","question":"糖果问题 __KEY__","answer":"21 __REMOTE__"}}`,
			wantStatus: "completed", wantVerdict: "passed", wantDetail: true,
		},
		{
			name: "糖果未通过", benchmark: "candy",
			payload:    `{"benchmark":"candy","status":"succeeded","candy":{"status":"incorrect","answer":"20"}}`,
			wantStatus: "completed", wantVerdict: "failed", wantDetail: true,
		},
		{
			name: "糖果结果无正文", benchmark: "candy",
			payload:    `{"benchmark":"candy","status":"succeeded","candy":{"status":"passed"}}`,
			wantStatus: "completed", wantVerdict: "passed",
		},
		{
			name: "糖果状态未知", benchmark: "candy",
			payload:    `{"benchmark":"candy","status":"succeeded","candy":{"status":"undetermined"}}`,
			wantStatus: "unknown", wantVerdict: "unknown", wantMessage: "糖果判定状态无法识别", wantDetail: true,
		},
		{
			name: "糖果缺少结果", benchmark: "candy",
			payload:    `{"benchmark":"candy","status":"succeeded"}`,
			wantStatus: "unknown", wantVerdict: "unknown", wantMessage: "未返回糖果判定结果", wantDetail: true,
		},
		{
			name: "糖果执行错误无原因", benchmark: "candy",
			payload:    `{"benchmark":"candy","status":"succeeded","candy":{"status":"error"}}`,
			wantStatus: "error", wantVerdict: "error", wantMessage: "未提供可识别的失败原因", wantDetail: true,
		},
		{
			name: "顶层鉴权错误覆盖通过结果", benchmark: "candy",
			payload:    `{"benchmark":"candy","status":"succeeded","candy":{"status":"passed"},"error":{"code":"invalid_api_key","message":"__URL__ __KEY__ __REMOTE__","status_code":401}}`,
			wantStatus: "error", wantVerdict: "error", wantMessage: "检测服务报告失败：模型接口鉴权失败，请检查检测 Key（HTTP 401）", wantDetail: true,
		},
		{
			name: "糖果嵌套额度错误覆盖通过结果", benchmark: "candy",
			payload:    `{"benchmark":"candy","status":"succeeded","candy":{"status":"passed","error":{"error":{"type":"insufficient_quota","message":"__URL__ __KEY__ __REMOTE__"},"status":429}}}`,
			wantStatus: "error", wantVerdict: "error", wantMessage: "糖果检测异常：模型接口余额或额度不足（HTTP 429）", wantDetail: true,
		},
		{
			name: "字符串限流错误", benchmark: "candy",
			payload:    `{"benchmark":"candy","status":"failed","error":"HTTP 429 too many requests __URL__ __KEY__ __REMOTE__"}`,
			wantStatus: "error", wantVerdict: "error", wantMessage: "检测服务报告失败：模型接口请求受限，请稍后重试（HTTP 429）", wantDetail: true,
		},
		{
			name: "未知错误原文不回显", benchmark: "candy",
			payload:    `{"benchmark":"candy","status":"failed","error":{"code":"__KEY__","message":"__URL__ __REMOTE__"}}`,
			wantStatus: "error", wantVerdict: "error", wantMessage: "未提供可安全展示的具体原因", wantDetail: true,
		},
		{
			name: "空错误占位不改变成功", benchmark: "candy",
			payload:    `{"benchmark":"candy","status":"succeeded","error":{},"candy":{"status":"passed","error":""}}`,
			wantStatus: "completed", wantVerdict: "passed",
		},
		{
			name: "糖果取消", benchmark: "candy",
			payload:    `{"benchmark":"candy","status":"cancelled","error":"__URL__ __KEY__ __REMOTE__"}`,
			wantStatus: "error", wantVerdict: "error", wantMessage: "检测任务已被取消", wantDetail: true,
		},
		{
			name: "画图通过", benchmark: "drawing",
			payload:    `{"benchmark":"pelican","status":"succeeded","result":{"html":"<p>作品 __KEY__ __REMOTE__</p>"},"assessment":{"quality":"normal","reason":"内容完整"}}`,
			wantStatus: "completed", wantVerdict: "passed", wantDetail: true, wantArtifact: true,
		},
		{
			name: "画图未通过", benchmark: "drawing",
			payload:    `{"benchmark":"pelican","status":"succeeded","assessment":{"quality":"degraded","reason":"缺少部分元素"}}`,
			wantStatus: "completed", wantVerdict: "failed", wantDetail: true,
		},
		{
			name: "画图评判未知仍保留作品", benchmark: "drawing",
			payload:    `{"benchmark":"pelican","status":"succeeded","result":{"html":"<p>作品</p>"},"assessment":{"quality":"unknown","reason":"评估说明 __KEY__ __REMOTE__"}}`,
			wantStatus: "unknown", wantVerdict: "unknown", wantMessage: "未能给出明确的画图评判", wantDetail: true, wantArtifact: true,
		},
		{
			name: "画图缺少质量评估", benchmark: "drawing",
			payload:    `{"benchmark":"pelican","status":"succeeded","result":{"html":"<p>作品</p>"}}`,
			wantStatus: "unknown", wantVerdict: "unknown", wantMessage: "未返回画图质量评估", wantDetail: true, wantArtifact: true,
		},
		{
			name: "画图执行失败HTTP503", benchmark: "drawing",
			payload:    `{"benchmark":"pelican","status":"failed","error":{"status_code":503,"message":"__URL__ __KEY__ __REMOTE__"}}`,
			wantStatus: "error", wantVerdict: "error", wantMessage: "模型接口服务异常，请稍后重试（HTTP 503）", wantDetail: true,
		},
		{
			name: "画图取消", benchmark: "drawing",
			payload:    `{"benchmark":"pelican","status":"cancelled"}`,
			wantStatus: "error", wantVerdict: "error", wantMessage: "检测任务已被取消", wantDetail: true,
		},
		{
			name: "省略远端类型沿用画图配置", benchmark: "drawing",
			payload:    `{"status":"succeeded","assessment":{"quality":"normal"}}`,
			wantStatus: "completed", wantVerdict: "passed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, repo, _, key := newIntelligenceFixture()
			run := intelligenceQueued()
			run.Benchmark = tc.benchmark
			run.RemoteID = "remote-private-capability"
			privateURL := "https://private.example/custom/v1?credential=private-value"
			payload := strings.NewReplacer("__KEY__", key.Key, "__REMOTE__", run.RemoteID, "__URL__", privateURL).Replace(tc.payload)
			payload = `{"id":"` + run.RemoteID + `",` + strings.TrimPrefix(payload, "{")
			var result remote.TestResult
			require.NoError(t, json.Unmarshal([]byte(payload), &result))
			s.applyResult(context.Background(), run, &result, key.Key)
			require.Len(t, repo.saves, 1)
			require.Equal(t, tc.wantStatus, repo.run.Status)
			require.Equal(t, tc.wantVerdict, repo.run.Verdict)
			require.Equal(t, tc.wantDetail, repo.run.HasDetail)
			require.Equal(t, tc.wantArtifact, repo.run.HasArtifact)
			require.NotNil(t, repo.run.FinishedAt)
			if tc.wantMessage == "" {
				require.Empty(t, repo.run.ErrorMessage)
			} else {
				require.Contains(t, repo.run.ErrorMessage, tc.wantMessage)
			}
			raw, err := json.Marshal(repo.run)
			require.NoError(t, err)
			for _, private := range []string{key.Key, run.RemoteID, privateURL, "private.example", run.LeaseToken} {
				require.NotContains(t, string(raw), private)
			}
			var visible map[string]any
			require.NoError(t, json.Unmarshal(raw, &visible))
			require.Equal(t, tc.wantDetail, visible["has_detail"])
		})
	}
}

// TestIntelligenceClassifyingPhasePersistsUntilAssessmentArrives polls a
// drawing through its classification phase into an unknown assessment. Polling
// retains task identity and generated artwork while distinguishing unfinished
// evaluation from a model failure.
func TestIntelligenceClassifyingPhasePersistsUntilAssessmentArrives(t *testing.T) {
	s, repo, _, key := newIntelligenceFixture()
	run := intelligenceQueued()
	run.Benchmark = "drawing"
	run.RemoteID = "remote-classifying"
	var result remote.TestResult
	require.NoError(t, json.Unmarshal([]byte(`{"id":"remote-classifying","benchmark":"pelican","status":"running","phase":"classifying","result":{"html":"<p>待评估作品</p>"}}`), &result))
	s.applyResult(context.Background(), run, &result, key.Key)
	require.Equal(t, "classifying", repo.run.Phase)
	require.Equal(t, "running", repo.run.Status)
	require.Equal(t, "pending", repo.run.Verdict)
	require.Nil(t, repo.run.FinishedAt)
	require.Empty(t, repo.run.ErrorMessage)
	require.True(t, repo.run.HasArtifact)
	require.True(t, repo.run.HasDetail)
	// An unknown terminal assessment keeps previously generated artwork available
	// after polling the same remote task through classification.
	result = remote.TestResult{}
	require.NoError(t, json.Unmarshal([]byte(`{"id":"remote-classifying","benchmark":"pelican","status":"succeeded","phase":"completed","assessment":{"quality":"unknown","reason":"无法给出明确判断"}}`), &result))
	s.applyResult(context.Background(), repo.run, &result, key.Key)
	require.Len(t, repo.saves, 2)
	require.Equal(t, "unknown", repo.run.Status)
	require.Equal(t, "unknown", repo.run.Verdict)
	require.Equal(t, "completed", repo.run.Phase)
	require.Equal(t, "<p>待评估作品</p>", repo.run.HTML)
	require.NotNil(t, repo.run.FinishedAt)
	require.True(t, repo.run.HasDetail)
}

// TestIntelligenceUserVisibilityAndLightweightList reads histories through
// current group visibility and configuration enablement. User summaries omit
// large payloads, while authorized detail reads retain the complete
// observation and reject other groups.
func TestIntelligenceUserVisibilityAndLightweightList(t *testing.T) {
	s, repo, _, _ := newIntelligenceFixture()
	run := intelligenceQueued()
	run.HTML = "<html>large</html>"
	run.Answer = "private answer"
	run.Question = "question"
	run.HasArtifact = true
	run.HasDetail = true
	repo.run = run
	repo.runs = []IntelligenceRun{*run}
	list, err := s.UserList(context.Background(), 99)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Empty(t, list[0].Runs[0].HTML)
	require.Empty(t, list[0].Artifacts[0].HTML)
	require.True(t, list[0].Artifacts[0].HasArtifact)
	data, err := s.UserDetail(context.Background(), 99, run.ID)
	require.NoError(t, err)
	require.Equal(t, run.HTML, data.HTML)
	s.available = intelligenceGroupStub{}
	list, err = s.UserList(context.Background(), 99)
	require.NoError(t, err)
	require.Empty(t, list)
	_, err = s.UserDetail(context.Background(), 99, run.ID)
	require.ErrorIs(t, err, ErrIntelligenceNotFound)
}

// TestIntelligencePollFailureKeepsRemoteTask injects polling errors and long
// remote retry delays for an accepted task. Its saved remote identity
// survives, and subsequent observation waits within the remaining execution
// deadline.
func TestIntelligencePollFailureKeepsRemoteTask(t *testing.T) {
	s, repo, client, _ := newIntelligenceFixture()
	run := intelligenceQueued()
	run.Status = "running"
	run.RemoteID = "remote"
	client.err = errors.New("network")
	s.execute(context.Background(), run)
	require.Zero(t, client.creates)
	require.Equal(t, "running", repo.run.Status)
	require.Equal(t, "remote", repo.run.RemoteID)
	client.err = &remote.HTTPError{StatusCode: 429, RetryAfter: 5 * time.Minute}
	before := time.Now()
	s.execute(context.Background(), run)
	require.GreaterOrEqual(t, repo.nextPoll.Sub(before), 5*time.Minute)
	// Keep large drawing payloads available through detail reads while list
	// refreshes transfer the bounded observation summaries used by status bars.
	run.Benchmark = "drawing"
	body := strings.Repeat("a", 512*1024)
	s.applyResult(context.Background(), run, &remote.TestResult{ID: "remote", Benchmark: "pelican", Status: "succeeded", Result: &remote.DrawingResult{HTML: body}, Assessment: &remote.Assessment{Quality: "good"}}, "")
	require.Equal(t, body, repo.run.HTML)
}
