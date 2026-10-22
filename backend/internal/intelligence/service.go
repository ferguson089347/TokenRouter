package intelligence

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/egress/urlpolicy"
	"github.com/TokenFlux/TokenRouter/internal/intelligence/remote"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// service.go validates quality-check configurations and advances durable
// jobs. Administrators supply a personal Key and a public gateway URL,
// and users read only results for groups currently available to them.
// Configuration identity stays fixed so existing history continues to describe
// the same group, model, and benchmark. Drawing generation calls the gateway
// locally, and candy checks let the remote tester call the gateway. Both use
// the Key's ordinary routing and billing rules.
//
// Workers record submitting before chargeable requests, and ambiguous generation
// finishes as unknown. Completed local HTML is persisted before assessment and
// can be evaluated again after classifier failure. Recovery polls a saved remote
// ID, while leases protect writes across instances. Closing the global switch
// blocks queued generations and user reads; accepted jobs finish their evaluation.

var (
	// Module errors distinguish unavailable access, missing records and occupied
	// configurations so panel callers can report the relevant recovery action.
	ErrIntelligenceNotFound = apperror.NotFound("INTELLIGENCE_NOT_FOUND", "检测配置或记录不存在")
	ErrIntelligenceDisabled = apperror.Forbidden("INTELLIGENCE_DISABLED", "降智检测未开启")
	ErrIntelligenceBusy     = apperror.Conflict("INTELLIGENCE_BUSY", "当前配置正在检测，请等待本次检测结束")

	// intelligenceKeyPattern removes recognizable local credentials from result
	// text even when the configured Key has already been deleted.
	intelligenceKeyPattern = regexp.MustCompile(`sk-[A-Za-z0-9_-]{16,}`)
)

// IntelligenceService validates configurations, authorizes reads, and advances
// durable model checks.
type IntelligenceService struct {
	repo              IntelligenceRepository        // durable job persistence
	keys              KeyReader                     // current credential reader
	groups            GroupReader                   // current group reader
	available         IntelligenceGroupReader       // personal group permissions
	client            IntelligenceClient            // remote test client
	thumbnails        IntelligenceThumbnailRenderer // isolated drawing renderer
	thumbnailSlot     chan struct{}                 // serial screenshot admission
	enabled           func(context.Context) bool    // current global switch
	resolvePublicHost func(string) error            // submission DNS validator
	ctx               context.Context               // worker cancellation context
	cancel            context.CancelFunc            // worker cancellation function
	start             sync.Once                     // worker start guard
	stop              sync.Once                     // worker stop guard
	// wg tracks workers launched once by Start. Lifecycle calls Stop after
	// startup, then waits before closing their dependencies.
	wg sync.WaitGroup // worker completion counter
}

// NewIntelligenceService constructs quality checks with caller-supplied
// storage, credential readers, remote client, feature switch, and DNS
// validation. A nil screenshot renderer keeps saved images readable but disables
// capturing new ones. Call Start after assembly and Stop before closing shared
// dependencies; each instance owns its worker cancellation context.
func NewIntelligenceService(repo IntelligenceRepository, keys KeyReader, groups GroupReader, available IntelligenceGroupReader, client IntelligenceClient, enabled func(context.Context) bool, resolvePublicHost func(string) error, thumbnails IntelligenceThumbnailRenderer) *IntelligenceService {
	ctx, cancel := context.WithCancel(context.Background())
	return &IntelligenceService{
		repo:              repo,
		keys:              keys,
		groups:            groups,
		available:         available,
		client:            client,
		enabled:           enabled,
		resolvePublicHost: resolvePublicHost,
		thumbnails:        thumbnails,
		thumbnailSlot:     make(chan struct{}, 1),
		ctx:               ctx,
		cancel:            cancel,
	}
}

// isEnabled reports whether the injected global switch permits feature access.
func (s *IntelligenceService) isEnabled(ctx context.Context) bool {
	return s != nil && s.enabled != nil && s.enabled(ctx)
}

// AdminList returns administrator configurations with their latest lightweight
// result summaries. This remains available while the global feature switch is
// disabled so administrators can manage saved configuration and inspect
// history.
func (s *IntelligenceService) AdminList(ctx context.Context) ([]IntelligenceConfig, error) {
	configs, err := s.repo.ListConfigs(ctx)
	if err != nil {
		return nil, err
	}
	for i := range configs {
		runs, e := s.repo.ListRuns(ctx, configs[i].ID)
		if e != nil {
			return nil, e
		}
		if len(runs) > 0 {
			v := intelligenceSummary(runs[0])
			configs[i].LatestRun = &v
		}
	}
	return configs, nil
}

// SaveConfig validates and saves one group, model, and benchmark
// configuration. A zero ID creates a configuration; editing keeps its identity
// fixed, and an empty APIKey retains the previously stored Key reference.
// Protocol and schedule defaults are applied before persistence, while
// credential lifecycle and group status are checked against current records.
//
// The repository serializes configuration updates against active runs, keeping
// in-flight submissions associated with the same credentials and target URL.
// Only the Key identifier enters persistent configuration and response values.
func (s *IntelligenceService) SaveConfig(ctx context.Context, id int64, in IntelligenceConfigInput) (*IntelligenceConfig, error) {
	in.Model = strings.TrimSpace(in.Model)
	in.BaseURL = strings.TrimSpace(in.BaseURL)
	in.APIKey = strings.TrimSpace(in.APIKey)
	in.Prompt = strings.TrimSpace(in.Prompt)
	if len(in.Prompt) > remote.MaxPromptBytes || (in.Benchmark != "drawing" && in.Prompt != "") {
		return nil, intelligenceInvalid("自定义提示词仅用于画图测试，最多32KiB")
	}
	if in.GroupID <= 0 || in.Model == "" || len(in.Model) > 255 || (in.Benchmark != "candy" && in.Benchmark != "drawing") {
		return nil, intelligenceInvalid("请选择有效分组、模型和测试类型")
	}
	if in.IntervalMinutes == 0 {
		in.IntervalMinutes = 60
	}
	if in.IntervalMinutes < 5 || in.IntervalMinutes > 10080 {
		return nil, intelligenceInvalid("检测间隔须为5至10080分钟")
	}
	if in.Protocol == "" {
		in.Protocol = "responses"
	}
	if !intelligenceChoice(in.Protocol, "responses", "chat_completions") || (in.Benchmark == "candy" && in.Protocol != "responses") {
		return nil, intelligenceInvalid("糖果测试仅支持 Responses；画图支持 Responses 或 Chat Completions")
	}
	if !intelligenceChoice(in.ReasoningEffort, "", "low", "medium", "high", "xhigh", "max", "ultra") || (in.Benchmark == "drawing" && !intelligenceChoice(in.ReasoningEffort, "", "low", "medium", "high")) {
		return nil, intelligenceInvalid("不支持的推理程度")
	}
	if !intelligenceChoice(in.ServiceTier, "", "priority", "ultrafast") {
		return nil, intelligenceInvalid("不支持的服务档位")
	}
	base, err := validateIntelligenceBaseURL(in.BaseURL)
	if err != nil {
		return nil, err
	}
	group, err := s.groups.GetByID(ctx, in.GroupID)
	if err != nil || group == nil || group.Status != "active" {
		return nil, intelligenceInvalid("分组不存在或已停用")
	}
	c := &IntelligenceConfig{
		ID:              id,
		GroupID:         in.GroupID,
		GroupName:       group.Name,
		Model:           in.Model,
		Benchmark:       in.Benchmark,
		Source:          "site",
		Prompt:          in.Prompt,
		BaseURL:         base,
		Protocol:        in.Protocol,
		ReasoningEffort: in.ReasoningEffort,
		ServiceTier:     in.ServiceTier,
		Enabled:         in.Enabled,
		ScheduleEnabled: in.ScheduleEnabled,
		IntervalMinutes: in.IntervalMinutes,
	}
	if id > 0 {
		old, e := s.repo.GetConfig(ctx, id)
		if e != nil {
			return nil, e
		}
		// Keep group, model and benchmark identity fixed so existing observations
		// cannot be reassigned to another test configuration.
		if old.GroupID != c.GroupID || old.Model != c.Model || old.Benchmark != c.Benchmark {
			return nil, intelligenceInvalid("已有配置的分组、模型和测试类型不能更改，请新增配置")
		}
		c.APIKeyID = old.APIKeyID
	}
	var key *apikey.APIKey
	if in.APIKey != "" {
		key, err = s.keys.GetByKey(ctx, in.APIKey)
	} else if c.APIKeyID > 0 {
		key, err = s.keys.GetByID(ctx, c.APIKeyID)
	}
	if err != nil || !intelligenceValidKey(key, c.GroupID) {
		return nil, intelligenceInvalid("需要有效、未过期且仅绑定所选分组的普通站内 API Key，不支持复合、团队或系统托管 Key")
	}
	c.APIKeyID = key.ID
	c.APIKeyConfigured = true
	if err = s.repo.SaveConfig(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

// intelligenceChoice reports whether a value matches an allowed string
// exactly.
func intelligenceChoice(v string, allowed ...string) bool {
	return slices.Contains(allowed, v)
}

// intelligenceInvalid creates a client-visible validation error with the
// supplied explanation.
func intelligenceInvalid(message string) error {
	return apperror.BadRequest("INTELLIGENCE_INVALID", message)
}

// intelligenceValidKey checks whether a personal ordinary Key can test the
// selected group. Current lifecycle, quota, owner state, and group binding all
// participate; the Key's unavailable-group routing preference remains
// eligible.
func intelligenceValidKey(key *apikey.APIKey, groupID int64) bool {
	// The unavailable-group setting affects gateway routing after submission;
	// ordinary Keys remain eligible while their bound group is active.
	return key != nil && key.Key != "" &&
		key.IsActive() && !key.IsExpired() && !key.IsQuotaExhausted() &&
		!key.IsComposite && len(key.CompositeGroups) == 0 &&
		key.GroupID != nil && *key.GroupID == groupID &&
		key.TeamID == nil && key.ManagedBy == nil &&
		key.User != nil && key.User.Status == "active" && key.User.DeletedAt == nil
}

// validateIntelligenceBaseURL accepts public HTTPS gateway addresses and
// returns normalized URL text. Credentials, queries, fragments and control
// characters are rejected; DNS validation is repeated immediately before
// remote submission.
func validateIntelligenceBaseURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u == nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(raw, "\r\n") {
		return "", intelligenceInvalid("检测 Base URL 必须为公网 HTTPS 地址，不能包含凭据、查询参数或片段")
	}
	normalized, err := urlpolicy.ValidateHTTPSURL(raw, urlpolicy.ValidationOptions{})
	if err != nil {
		return "", intelligenceInvalid("检测 Base URL 必须为公网 HTTPS 地址")
	}
	return normalized, nil
}

// DeleteConfig removes an idle configuration through the repository
// transaction rules.
func (s *IntelligenceService) DeleteConfig(ctx context.Context, id int64) error {
	return s.repo.DeleteConfig(ctx, id)
}

// Run queues a manual check after validating the feature switch,
// configuration, and current Key. The repository admits one active job per
// configuration; workers perform the remote request after the queue record
// commits.
func (s *IntelligenceService) Run(ctx context.Context, id int64) (*IntelligenceRun, error) {
	if !s.isEnabled(ctx) {
		return nil, ErrIntelligenceDisabled
	}
	c, err := s.repo.GetConfig(ctx, id)
	if err != nil {
		return nil, err
	}
	if !c.Enabled {
		return nil, ErrIntelligenceDisabled
	}
	key, err := s.keys.GetByID(ctx, c.APIKeyID)
	if err != nil || !intelligenceValidKey(key, c.GroupID) {
		return nil, intelligenceInvalid("检测 Key 不可用或已更换分组，请更新配置")
	}
	return s.repo.CreateRun(ctx, id, time.Now().UTC())
}

// AdminRuns returns lightweight history after confirming the configuration
// exists. Question, answer, HTML, and assessment text are omitted from these
// summaries, while the detail endpoint reads the separately stored payload.
func (s *IntelligenceService) AdminRuns(ctx context.Context, id int64) ([]IntelligenceRun, error) {
	if _, err := s.repo.GetConfig(ctx, id); err != nil {
		return nil, err
	}
	runs, err := s.repo.ListRuns(ctx, id)
	if err != nil {
		return nil, err
	}
	for i := range runs {
		runs[i] = intelligenceSummary(runs[i])
	}
	return runs, nil
}

// AdminDetail returns a stored result and payload for administrator
// inspection.
func (s *IntelligenceService) AdminDetail(ctx context.Context, id string) (*IntelligenceRun, error) {
	return s.repo.GetRun(ctx, id)
}

// intelligenceSummary removes large result text from a copied record for list
// responses. Pointer-valued statistics and timestamps remain shared with the
// input and are treated as immutable by the list callers.
func intelligenceSummary(run IntelligenceRun) IntelligenceRun {
	run.HTML = ""
	run.Question = ""
	run.Answer = ""
	run.AssessmentReason = ""
	return run
}

// visibleGroups builds the current set of groups available to one user.
func (s *IntelligenceService) visibleGroups(ctx context.Context, userID int64) (map[int64]bool, error) {
	groups, err := s.available.GetAvailableGroups(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := map[int64]bool{}
	for _, g := range groups {
		out[g.ID] = true
	}
	return out, nil
}

// UserList returns enabled checks belonging to the user's currently available
// groups. The global switch gates access, histories contain lightweight
// summaries from both site checks and public user submissions, and each check
// exposes at most ten retained drawing artifacts.
func (s *IntelligenceService) UserList(ctx context.Context, userID int64) ([]IntelligenceTest, error) {
	if !s.isEnabled(ctx) {
		return nil, ErrIntelligenceDisabled
	}
	visible, err := s.visibleGroups(ctx, userID)
	if err != nil {
		return nil, err
	}
	configs, err := s.repo.ListConfigs(ctx)
	if err != nil {
		return nil, err
	}
	community, err := s.repo.ListCommunityConfigs(ctx)
	if err != nil {
		return nil, err
	}
	configs = append(configs, community...)
	out := make([]IntelligenceTest, 0)
	for _, c := range configs {
		if !c.Enabled || !visible[c.GroupID] {
			continue
		}
		runs, err := s.repo.ListRuns(ctx, c.ID)
		if err != nil {
			return nil, err
		}
		test := IntelligenceTest{ID: c.ID, GroupID: c.GroupID, GroupName: c.GroupName, Model: c.Model, Benchmark: c.Benchmark, Source: c.Source, Runs: make([]IntelligenceRun, 0, len(runs)), Artifacts: make([]IntelligenceRun, 0)}
		for _, run := range runs {
			test.Runs = append(test.Runs, intelligenceSummary(run))
			if run.HasArtifact && len(test.Artifacts) < 10 {
				test.Artifacts = append(test.Artifacts, intelligenceSummary(run))
			}
		}
		out = append(out, test)
	}
	return out, nil
}

// UserDetail reads a result after checking the global switch and current group
// permissions. Disabled configurations and unauthorized records return the
// same not-found error, keeping another group's history inaccessible by ID.
func (s *IntelligenceService) UserDetail(ctx context.Context, userID int64, id string) (*IntelligenceRun, error) {
	if !s.isEnabled(ctx) {
		return nil, ErrIntelligenceDisabled
	}
	run, err := s.repo.GetRun(ctx, id)
	if err != nil {
		return nil, err
	}
	c, err := s.repo.GetConfig(ctx, run.ConfigID)
	if err != nil {
		return nil, err
	}
	visible, err := s.visibleGroups(ctx, userID)
	if err != nil {
		return nil, err
	}
	if !c.Enabled || !visible[run.GroupID] || c.GroupID != run.GroupID {
		return nil, ErrIntelligenceNotFound
	}
	return run, nil
}

// Start launches the instance's two workers once after dependency assembly.
// The first worker schedules enabled configurations; both may poll already
// submitted jobs even when the global feature switch is closed.
func (s *IntelligenceService) Start() {
	s.start.Do(func() {
		for i := range 2 {
			// Register worker completion before launch so shutdown can account for every
			// goroutine before their shared dependencies are released.
			s.wg.Add(1)
			go s.worker(i)
		}
	})
}

// Stop cancels the instance workers and waits for their SQL and remote
// operations to finish. It is idempotent; call it before closing any shared
// dependencies and after the lifecycle has finished starting workers.
func (s *IntelligenceService) Stop() {
	s.stop.Do(func() {
		// Cancel before waiting so polling and scheduling relinquish their shared
		// dependencies before the application closes SQL and transport resources.
		s.cancel()
		s.wg.Wait()
	})
}

// worker claims one due operation per tick using a fresh lease token. Worker
// zero also schedules configurations while enabled. Each iteration bounds
// network and storage work before relinquishing its cancellation context.
func (s *IntelligenceService) worker(index int) {
	defer s.wg.Done()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			// Keep the operation budget below its lease so cancellation and bounded
			// result persistence finish before another worker may replace this claim.
			ctx, cancel := context.WithTimeout(s.ctx, 6*time.Minute)
			if index == 0 && s.isEnabled(ctx) {
				// Scheduling failures leave configurations due for the next tick, so an
				// interrupted scan can be retried without creating remote requests.
				_ = s.repo.ScheduleDue(ctx, time.Now().UTC(), 2)
			}
			now := time.Now().UTC()
			run, err := s.repo.ClaimRun(ctx, now, now.Add(7*time.Minute), uuid.NewString())
			// A failed claim leaves durable work available for a later tick; an idle scan
			// likewise waits before querying the queue again.
			if err == nil && run != nil {
				s.execute(ctx, run)
			}
			cancel()
		}
	}
}

// finish stores a terminal result with a separate bounded persistence context.
// Caller cancellation does not discard the observed outcome; the repository
// still rejects writes whose lease has expired or been replaced.
func (s *IntelligenceService) finish(ctx context.Context, run *IntelligenceRun, status, verdict, message string) {
	// Detach terminal persistence from request cancellation so an observed outcome
	// can still be saved within its own short budget.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	now := time.Now().UTC()
	run.Status = status
	run.Verdict = verdict
	run.ErrorMessage = message
	run.UpdatedAt = now
	run.FinishedAt = &now
	run.HasDetail = true
	// A rejected or failed completion keeps the durable task under its existing
	// state; recovery can resolve it after the lease expires.
	_, _ = s.repo.SaveRun(ctx, run, now, false)
}

// execute advances a claimed run using its current durable submission state. A
// queued job validates its current configuration, Key, group and DNS before
// persisting submitting and attempting one chargeable generation request.
// Recovery marks interrupted generation unknown when neither a remote task ID
// nor a completed local document was saved, since repeating it could incur costs.
//
// Locally generated HTML is saved before submission for assessment. Recovery can
// repeat HTML evaluation using that saved artwork, while jobs with remote IDs use
// read-only polling and bounded delays until completion or the observation
// deadline. Every write carries the claim token
// so a superseded worker cannot replace a newer observation.
func (s *IntelligenceService) execute(ctx context.Context, run *IntelligenceRun) {
	if run.Status == "submitting" && run.RemoteID == "" && (!run.LocalGeneration || run.HTML == "") {
		s.finish(ctx, run, "unknown", "unknown", "上次提交结果不确定，未自动重新发起检测，请先核对使用记录")
		return
	}
	// Bound observation time even when the remote service keeps returning
	// unfamiliar or nonterminal states for the same accepted task.
	if time.Since(run.CreatedAt) > 30*time.Minute {
		s.finish(ctx, run, "error", "error", "检测超过等待时限，未自动重试")
		return
	}
	if run.LocalGeneration && run.HTML != "" && run.RemoteID == "" {
		s.assessDrawing(ctx, run)
		return
	}
	if run.Status == "queued" {
		if !s.isEnabled(ctx) {
			s.finish(ctx, run, "error", "error", "降智检测已关闭，本次尚未提交")
			return
		}
		c, err := s.repo.GetConfig(ctx, run.ConfigID)
		if err != nil || !c.Enabled {
			s.finish(ctx, run, "error", "error", "检测配置不可用")
			return
		}
		group, err := s.groups.GetByID(ctx, c.GroupID)
		if err != nil || group == nil || group.Status != "active" {
			s.finish(ctx, run, "error", "error", "分组不可用")
			return
		}
		key, err := s.keys.GetByID(ctx, c.APIKeyID)
		if err != nil || !intelligenceValidKey(key, c.GroupID) {
			s.finish(ctx, run, "error", "error", "检测 Key 不可用或分组已变更")
			return
		}
		u, err := url.Parse(c.BaseURL)
		if err != nil || s.resolvePublicHost(u.Hostname()) != nil {
			s.finish(ctx, run, "error", "error", "检测目标必须可以解析为公网地址")
			return
		}
		// Persist submission intent while retaining the lease before network creation;
		// recovery treats an interrupted attempt as unknown rather than repeating it.
		run.Status = "submitting"
		if c.Benchmark == "drawing" {
			run.LocalGeneration = true
			run.Phase = "generating"
		}
		run.UpdatedAt = time.Now().UTC()
		ok, err := s.repo.SaveRun(ctx, run, time.Now().UTC(), true)
		if err != nil || !ok {
			return
		}
		if c.Benchmark == "drawing" {
			s.generateDrawing(ctx, run, c, key.Key)
			return
		}
		result, err := s.client.Create(ctx, remote.CreateRequest{
			Benchmark:       c.Benchmark,
			BaseURL:         c.BaseURL,
			APIKey:          key.Key,
			Model:           c.Model,
			Protocol:        c.Protocol,
			ReasoningEffort: c.ReasoningEffort,
			ServiceTier:     c.ServiceTier,
		}, run.ID)
		if err != nil {
			status := "unknown"
			var httpErr *remote.HTTPError
			if errors.As(err, &httpErr) && httpErr.StatusCode >= 400 && httpErr.StatusCode < 500 {
				status = "error"
			}
			s.finish(context.WithoutCancel(ctx), run, status, status, intelligenceSubmissionError(err))
			return
		}
		if result == nil || result.ID == "" {
			s.finish(ctx, run, "unknown", "unknown", "检测服务未返回有效任务编号，未自动重试")
			return
		}
		run.RemoteID = result.ID
		s.applyResult(ctx, run, result, key.Key)
		return
	}
	if run.RemoteID == "" {
		s.finish(ctx, run, "unknown", "unknown", "检测任务缺少远端编号，无法继续查询")
		return
	}
	result, err := s.client.Get(ctx, run.RemoteID)
	if err != nil {
		var he *remote.HTTPError
		if errors.As(err, &he) && (he.StatusCode == 404 || he.StatusCode == 410) {
			if run.LocalGeneration && run.HTML != "" && run.AssessmentAttempts < 5 {
				// Recreate an expired HTML assessment from the retained document. Clearing
				// its polling identity cannot initiate another chargeable gateway generation.
				run.RemoteID = ""
				run.Phase = "generated"
				run.UpdatedAt = time.Now().UTC()
				_, _ = s.repo.SaveRun(ctx, run, time.Now().Add(15*time.Second), false)
				return
			}
			s.finish(ctx, run, "error", "error", "检测服务中的结果已失效")
			return
		}
		delay := 15 * time.Second
		if errors.As(err, &he) && he.RetryAfter > delay {
			delay = he.RetryAfter
		}
		// Honor the remote retry delay within the remaining observation window so a
		// long Retry-After cannot trigger an earlier poll.
		remaining := time.Until(run.CreatedAt.Add(30 * time.Minute))
		if remaining <= 0 {
			s.finish(ctx, run, "error", "error", "检测超过等待时限，未自动重试")
			return
		}
		if delay > remaining && remaining > 0 {
			delay = remaining
		}
		run.UpdatedAt = time.Now().UTC()
		// A failed delay write leaves the accepted task recoverable after lease
		// expiry; no creation request is issued during this polling path.
		_, _ = s.repo.SaveRun(ctx, run, time.Now().Add(delay), false)
		return
	}
	// Recover the local secret solely to redact echoed result content; already
	// submitted tasks use their remote ID for polling.
	secret := ""
	if c, e := s.repo.GetConfig(ctx, run.ConfigID); e == nil {
		if key, e := s.keys.GetByID(ctx, c.APIKeyID); e == nil && key != nil {
			secret = key.Key
		}
	}
	s.applyResult(ctx, run, result, secret)
}

// applyResult validates a remote observation and stores its safe local
// representation. The remote ID and benchmark must match the claimed job; an
// omitted benchmark uses the persisted configuration type before outcome
// classification. Missing assessments remain unknown, and execution errors
// remain distinct from incorrect model answers.
//
// Credential and remote-ID occurrences are removed from saved content.
// Persistence uses a separate bounded context after cancellation so an
// accepted task ID can survive for recovery, subject to the repository's
// current lease check.
func (s *IntelligenceService) applyResult(ctx context.Context, run *IntelligenceRun, result *remote.TestResult, secret string) {
	// Persist the accepted remote task identity after caller cancellation so a
	// later worker can resume polling the same request.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	expectedBenchmark := run.Benchmark
	// Compare observations using Manxue's pelican identifier so a drawing
	// configuration still validates responses against the benchmark actually submitted.
	if expectedBenchmark == "drawing" {
		expectedBenchmark = "pelican"
	}
	if run.LocalGeneration {
		expectedBenchmark = "html"
	}
	// HTML assessment polling can identify the classifier as pelican. Accept that
	// documented response label while keeping legacy remote drawing jobs compatible.
	compatible := result != nil && (result.Benchmark == "" || result.Benchmark == expectedBenchmark || (run.LocalGeneration && result.Benchmark == "pelican"))
	if !compatible || result.ID != run.RemoteID {
		s.finish(ctx, run, "error", "error", "检测服务返回的任务信息不一致")
		return
	}
	run.UpdatedAt = time.Now().UTC()
	// Use the persisted benchmark when polling omits it, keeping the assessment
	// tied to the configuration that submitted this job.
	if result.Benchmark == "" {
		copy := *result
		copy.Benchmark = expectedBenchmark
		result = &copy
	}
	run.Phase = ""
	if intelligenceChoice(result.Phase, "queued", "generating", "testing", "assessing", "classifying", "completed", "complete", "failed", "finished", "candy", "pelican") {
		run.Phase = result.Phase
	}
	run.Verdict = result.Outcome()
	run.Status = "running"
	run.ErrorMessage = ""
	redact := func(v string) string { return redactIntelligenceText(v, secret, run.RemoteID) }
	if result.Candy != nil && !run.LocalGeneration {
		run.Question = redact(result.Candy.Question)
		run.Answer = redact(result.Candy.Answer)
		run.InputTokens = result.Candy.InputTokens
		run.OutputTokens = result.Candy.OutputTokens
		run.ReasoningTokens = result.Candy.ReasoningTokens
		run.DurationMS = result.Candy.DurationMS
	}
	if result.Result != nil && !run.LocalGeneration {
		run.Question = redact(result.Result.Prompt)
		run.HTML = redact(result.Result.HTML)
		run.InputTokens = result.Result.InputTokens
		run.OutputTokens = result.Result.OutputTokens
		run.ReasoningTokens = result.Result.ReasoningTokens
		run.DurationMS = result.Result.DurationMS
	}
	if result.Assessment != nil {
		run.AssessmentReason = redact(result.Assessment.Reason)
	}
	// Retain the assessment when artwork exceeds storage limits.
	if len(run.HTML) > 2*1024*1024 {
		run.HTML = ""
		run.ErrorMessage = "画图内容超过2MiB保存限制，仅保留检测结果"
	}
	if result.Terminal() {
		run.Status = "completed"
		if run.Verdict == "error" || run.Verdict == "unknown" {
			// A finished generation can lack a recognized assessment; retain the unknown
			// verdict so users can distinguish incomplete evaluation.
			run.Status = run.Verdict
			run.ErrorMessage = intelligenceResultMessage(result, run.Verdict)
		}
		run.FinishedAt = &run.UpdatedAt
	}
	run.HasArtifact = run.HTML != ""
	run.HasDetail = run.Question != "" || run.Answer != "" || run.HTML != "" || run.AssessmentReason != "" || run.ErrorMessage != ""
	// If observation persistence fails, the prior durable state remains
	// recoverable; a submitting record without its accepted ID becomes unknown on
	// recovery.
	_, _ = s.repo.SaveRun(ctx, run, time.Now().Add(10*time.Second), false)
}

// intelligenceSubmissionError describes an uncertain creation failure using
// local templates. Only bounded HTTP status and timeout classifications enter
// the message; raw network errors may contain credentials or remote task
// identifiers.
func intelligenceSubmissionError(err error) string {
	var httpErr *remote.HTTPError
	if errors.As(err, &httpErr) && httpErr.StatusCode >= 400 && httpErr.StatusCode <= 599 {
		if httpErr.StatusCode < 500 {
			return fmt.Sprintf("检测服务拒绝创建任务（HTTP %d），请检查检测参数或服务限流；未自动重试", httpErr.StatusCode)
		}
		return fmt.Sprintf("检测服务创建接口异常（HTTP %d），是否受理尚不确定；未自动重试，请核对站内使用记录", httpErr.StatusCode)
	}
	var transport *remote.TransportError
	if errors.As(err, &transport) && transport.Timeout {
		return "连接检测服务超时，是否受理尚不确定；未自动重试，请核对站内使用记录"
	}
	if errors.Is(err, remote.ErrResponseTooLarge) {
		return "检测服务响应超过大小限制，未能确认任务信息；未自动重试，请核对站内使用记录"
	}
	return "检测提交未成功确认，未自动重试，请核对站内使用记录"
}

// intelligenceResultMessage explains execution errors and unknown assessments
// using safe local templates. The caller supplies the computed verdict; remote
// diagnostic text is interpreted through the client's allowlisted categories
// before display.
func intelligenceResultMessage(result *remote.TestResult, verdict string) string {
	if verdict == "unknown" {
		if result.Benchmark == "candy" {
			if result.Candy == nil {
				return "检测已结束，但检测服务未返回糖果判定结果；不能据此判定模型是否通过"
			}
			return "检测已结束，但糖果判定状态无法识别；不能据此判定模型是否通过"
		}
		if result.Assessment == nil {
			return "检测已结束，但检测服务未返回画图质量评估；不能据此判定模型是否降智"
		}
		return "检测已结束，但检测服务未能给出明确的画图评判；请查看评估说明"
	}
	if result.Status == "cancelled" {
		return "检测任务已被取消，未自动重新提交"
	}
	if result.Error != nil && result.Error.Code != "" {
		return "检测服务报告失败：" + result.Error.SafeMessage()
	}
	if result.Candy != nil && result.Candy.Error != nil && result.Candy.Error.Code != "" {
		return "糖果检测异常：" + result.Candy.Error.SafeMessage()
	}
	return "检测服务报告执行异常，但未提供可识别的失败原因；请核对相同时段的站内使用记录"
}
