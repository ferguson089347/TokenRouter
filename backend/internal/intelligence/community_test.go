package intelligence

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// community_test.go exercises personal Key admission with controlled site
// configurations and captured queue writes. Tests verify configuration selection,
// copied execution settings, ownership, and group access before any chargeable
// work can enter shared user histories.

// communityRepo captures authenticated submissions and exposes shared user rows.
type communityRepo struct {
	*intelligenceRepoStub                      // existing observation fixture
	submitted             *IntelligenceConfig  // captured queue settings
	sites                 []IntelligenceConfig // ordered system configurations
}

// ListConfigs returns ordered system fixtures or the standard site's configuration.
func (r *communityRepo) ListConfigs(ctx context.Context) ([]IntelligenceConfig, error) {
	if r.sites != nil {
		return append([]IntelligenceConfig(nil), r.sites...), nil
	}
	return r.intelligenceRepoStub.ListConfigs(ctx)
}

// CreateCommunityRun captures execution settings and returns one queued fixture observation.
func (r *communityRepo) CreateCommunityRun(_ context.Context, config *IntelligenceConfig, now time.Time) (*IntelligenceRun, error) {
	r.submitted = config
	return &IntelligenceRun{ID: "community-run", ConfigID: 2, GroupID: config.GroupID, Model: config.Model, Benchmark: "drawing", Status: "queued", CreatedAt: now}, nil
}

// TestCommunityRunUsesSystemParametersAndRequiresOwnerAccess submits an owned
// Key with obsolete client overrides. The first enabled drawing supplies every
// execution parameter, while missing system settings and unauthorized Keys cannot queue work.
func TestCommunityRunUsesSystemParametersAndRequiresOwnerAccess(t *testing.T) {
	service, original, _, key := newIntelligenceFixture()
	key.UserID = 12
	site := *original.config
	site.Benchmark = "drawing"
	site.APIKeyID = key.ID + 100
	site.Model = "system-model"
	site.Protocol = "chat_completions"
	site.ReasoningEffort = "high"
	site.ServiceTier = "priority"
	site.Prompt = "system drawing instruction"
	site.BaseURL = "https://system.example/custom/v1"
	disabled := site
	disabled.Enabled = false
	otherGroup := site
	otherGroup.GroupID++
	later := site
	later.Model = "later-system-model"
	repo := &communityRepo{intelligenceRepoStub: original, sites: []IntelligenceConfig{disabled, otherGroup, *original.config, site, later}}
	service.repo = repo
	var input IntelligenceSelfTestInput
	err := json.Unmarshal([]byte(`{"api_key_id":9,"model":"client-model","protocol":"responses","reasoning_effort":"low","prompt":"client prompt","publish":false,"base_url":"https://client.example/v1"}`), &input)
	require.NoError(t, err)
	run, err := service.CommunityRun(context.Background(), 12, input)
	require.NoError(t, err)
	require.Equal(t, "queued", run.Status)
	require.Equal(t, "user", repo.submitted.Source)
	require.Equal(t, key.ID, repo.submitted.APIKeyID)
	require.Equal(t, site.Model, repo.submitted.Model)
	require.Equal(t, site.Protocol, repo.submitted.Protocol)
	require.Equal(t, site.ReasoningEffort, repo.submitted.ReasoningEffort)
	require.Equal(t, site.ServiceTier, repo.submitted.ServiceTier)
	require.Equal(t, site.Prompt, repo.submitted.Prompt)
	require.Equal(t, site.BaseURL, repo.submitted.BaseURL)
	require.False(t, repo.submitted.ScheduleEnabled)
	raw, err := json.Marshal(run)
	require.NoError(t, err)
	require.NotContains(t, string(raw), key.Key)

	repo.submitted = nil
	_, err = service.CommunityRun(context.Background(), 13, input)
	require.Error(t, err)
	require.Nil(t, repo.submitted)
	service.available = intelligenceGroupStub{visible: []routing.Group{}}
	_, err = service.CommunityRun(context.Background(), 12, input)
	require.ErrorIs(t, err, ErrIntelligenceNotFound)
	require.Nil(t, repo.submitted)
	service.available = intelligenceGroupStub{visible: []routing.Group{{ID: site.GroupID}}}
	repo.sites = []IntelligenceConfig{disabled, otherGroup, *original.config}
	_, err = service.CommunityRun(context.Background(), 12, input)
	require.Error(t, err)
	require.Nil(t, repo.submitted)
	site.BaseURL = "https://127.0.0.1/v1"
	repo.sites = []IntelligenceConfig{site}
	_, err = service.CommunityRun(context.Background(), 12, input)
	require.Error(t, err)
	require.Nil(t, repo.submitted)
}

// TestCommunityRunSelectsMatchingSiteDrawing submits a later model's site ID.
// Its settings reach the queue, while foreign, disabled, missing, user-source,
// and candy configurations cannot redirect a chargeable request to another model.
// Authorized histories also expose the current group multiplier for each model.
func TestCommunityRunSelectsMatchingSiteDrawing(t *testing.T) {
	service, original, _, key := newIntelligenceFixture()
	key.UserID = 12
	first := *original.config
	first.Benchmark = "drawing"
	selected := first
	selected.ID = first.ID + 1
	selected.Model = "selected-model"
	selected.Prompt = "selected drawing instruction"
	repo := &communityRepo{intelligenceRepoStub: original, sites: []IntelligenceConfig{first, selected}}
	service.repo = repo
	input := IntelligenceSelfTestInput{APIKeyID: key.ID, ConfigID: selected.ID}
	run, err := service.CommunityRun(t.Context(), 12, input)
	require.NoError(t, err)
	require.Equal(t, selected.Model, run.Model)
	require.Equal(t, selected.Prompt, repo.submitted.Prompt)
	service.available = intelligenceGroupStub{visible: []routing.Group{{ID: selected.GroupID, RateMultiplier: 2.5}}}
	histories, err := service.UserList(t.Context(), 12)
	require.NoError(t, err)
	require.Len(t, histories, 2)
	for _, history := range histories {
		require.Equal(t, 2.5, history.RateMultiplier)
	}

	for _, scenario := range []string{"foreign", "disabled", "missing", "user", "candy", "negative"} {
		t.Run(scenario, func(t *testing.T) {
			candidate := selected
			request := input
			switch scenario {
			case "foreign":
				candidate.GroupID++
			case "disabled":
				candidate.Enabled = false
			case "missing":
				request.ConfigID++
			case "user":
				candidate.Source = "user"
			case "candy":
				candidate.Benchmark = "candy"
			case "negative":
				request.ConfigID = -1
			}
			repo.sites = []IntelligenceConfig{first, candidate}
			repo.submitted = nil
			_, err := service.CommunityRun(t.Context(), 12, request)
			require.Error(t, err)
			require.Nil(t, repo.submitted)
		})
	}
}
