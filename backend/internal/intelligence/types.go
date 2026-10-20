package intelligence

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/intelligence/remote"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// types.go defines quality-check configuration, result records, and the
// dependencies used by workers. Public JSON omits Key references, remote task
// identifiers, and lease tokens, while large observation text is split from
// summaries by the PostgreSQL adapter.

// IntelligenceConfig stores one immutable test identity and its administrator
// execution settings.
type IntelligenceConfig struct {
	ID        int64  `json:"id"`         // local record identifier
	GroupID   int64  `json:"group_id"`   // configured routing group
	GroupName string `json:"group_name"` // current group name
	Model     string `json:"model"`      // requested model identifier
	Benchmark string `json:"benchmark"`  // candy or drawing benchmark
	BaseURL   string `json:"base_url"`   // public gateway endpoint
	// APIKeyID references the ordinary personal credential resolved before
	// submission. It is excluded from administrator and user JSON responses.
	APIKeyID         int64            `json:"-"`                    // private credential reference
	APIKeyConfigured bool             `json:"api_key_configured"`   // saved credential presence
	Protocol         string           `json:"protocol"`             // remote model protocol
	ReasoningEffort  string           `json:"reasoning_effort"`     // requested reasoning level
	ServiceTier      string           `json:"service_tier"`         // requested service tier
	Enabled          bool             `json:"enabled"`              // configuration availability switch
	ScheduleEnabled  bool             `json:"schedule_enabled"`     // periodic submission switch
	IntervalMinutes  int              `json:"interval_minutes"`     // scheduled interval minutes
	NextRunAt        *time.Time       `json:"next_run_at"`          // next scheduled submission
	CreatedAt        time.Time        `json:"created_at"`           // record creation timestamp
	UpdatedAt        time.Time        `json:"updated_at"`           // latest observation timestamp
	LatestRun        *IntelligenceRun `json:"latest_run,omitempty"` // newest lightweight observation
}

// IntelligenceConfigInput carries administrator fields with a write-only
// supplied Key secret.
type IntelligenceConfigInput struct {
	GroupID   int64  `json:"group_id"`  // configured routing group
	Model     string `json:"model"`     // requested model identifier
	Benchmark string `json:"benchmark"` // candy or drawing benchmark
	BaseURL   string `json:"base_url"`  // public gateway endpoint
	// APIKey supplies a secret for configuration validation. Empty edit input
	// retains the existing stored Key reference.
	APIKey          string `json:"api_key"`          // supplied gateway credential
	Protocol        string `json:"protocol"`         // remote model protocol
	ReasoningEffort string `json:"reasoning_effort"` // requested reasoning level
	ServiceTier     string `json:"service_tier"`     // requested service tier
	Enabled         bool   `json:"enabled"`          // configuration availability switch
	ScheduleEnabled bool   `json:"schedule_enabled"` // periodic submission switch
	IntervalMinutes int    `json:"interval_minutes"` // scheduled interval minutes
}

// IntelligenceRun stores an observation with separately persisted private
// worker recovery state.
type IntelligenceRun struct {
	ID               string     `json:"id"`                          // local record identifier
	ConfigID         int64      `json:"config_id"`                   // owning configuration identifier
	GroupID          int64      `json:"group_id"`                    // configured routing group
	Model            string     `json:"model"`                       // requested model identifier
	Benchmark        string     `json:"benchmark"`                   // candy or drawing benchmark
	Status           string     `json:"status"`                      // job execution state
	Verdict          string     `json:"verdict"`                     // normalized assessment outcome
	Phase            string     `json:"phase"`                       // remote execution phase
	Question         string     `json:"question,omitempty"`          // reported benchmark prompt
	Answer           string     `json:"answer,omitempty"`            // reported model answer
	HTML             string     `json:"html,omitempty"`              // untrusted drawing document
	InputTokens      *int64     `json:"input_tokens,omitempty"`      // reported input tokens
	OutputTokens     *int64     `json:"output_tokens,omitempty"`     // reported output tokens
	ReasoningTokens  *int64     `json:"reasoning_tokens,omitempty"`  // reported reasoning tokens
	DurationMS       *int64     `json:"duration_ms,omitempty"`       // reported duration milliseconds
	AssessmentReason string     `json:"assessment_reason,omitempty"` // remote assessment explanation
	ErrorMessage     string     `json:"error_message,omitempty"`     // safe local diagnostic
	HasDetail        bool       `json:"has_detail"`                  // available observation detail
	HasArtifact      bool       `json:"has_artifact"`                // retained drawing presence
	CreatedAt        time.Time  `json:"created_at"`                  // record creation timestamp
	UpdatedAt        time.Time  `json:"updated_at"`                  // latest observation timestamp
	FinishedAt       *time.Time `json:"finished_at,omitempty"`       // terminal observation timestamp
	// RemoteID is the capability used to poll an accepted remote job. SQL stores
	// it separately, and public JSON excludes it.
	RemoteID string `json:"-"` // private polling credential
	// LeaseToken identifies the current worker claim. SQL requires a matching,
	// unexpired claim before accepting result updates from that worker.
	LeaseToken string `json:"-"` // private worker ownership
}

// IntelligenceTest exposes authorized group and model histories to read-only
// users.
type IntelligenceTest struct {
	ID        int64             `json:"id"`         // local record identifier
	GroupID   int64             `json:"group_id"`   // configured routing group
	GroupName string            `json:"group_name"` // current group name
	Model     string            `json:"model"`      // requested model identifier
	Benchmark string            `json:"benchmark"`  // candy or drawing benchmark
	Runs      []IntelligenceRun `json:"runs"`       // lightweight execution history
	Artifacts []IntelligenceRun `json:"artifacts"`  // retained artwork summaries
}

// IntelligenceRepository persists configurations and conditionally advances
// jobs using expiring worker leases.
type IntelligenceRepository interface {
	// ListConfigs returns bounded configurations in group display order.
	ListConfigs(context.Context) ([]IntelligenceConfig, error)
	// GetConfig returns the configuration or ErrIntelligenceNotFound when absent.
	GetConfig(context.Context, int64) (*IntelligenceConfig, error)
	// SaveConfig persists an idle configuration and fills identity and timestamps.
	// Existing rows serialize against queued work; creation enforces the same two-
	// hundred-record cap used by administrator listing.
	SaveConfig(context.Context, *IntelligenceConfig) error
	// DeleteConfig removes an idle configuration and its dependent history. Active
	// work returns ErrIntelligenceBusy; the idle check and deletion share the same
	// configuration row lock until commit.
	DeleteConfig(context.Context, int64) error
	// CreateRun queues one job while advancing its configuration schedule
	// atomically. Active work returns ErrIntelligenceBusy, and disabled
	// configurations or inactive groups cannot create additional chargeable work.
	CreateRun(context.Context, int64, time.Time) (*IntelligenceRun, error)
	// ScheduleDue creates a bounded batch of due scheduled jobs. Concurrent
	// instances skip locked configurations, and each job insertion advances its
	// schedule within the same transaction before publication.
	ScheduleDue(context.Context, time.Time, int) error
	// ClaimRun leases one due active job and returns its full payload. Missing
	// work returns nil without error; expired claims can be replaced by a new
	// token supplied by the caller.
	ClaimRun(context.Context, time.Time, time.Time, string) (*IntelligenceRun, error)
	// SaveRun compares a matching unexpired lease before saving and optionally
	// releasing it. False means ownership was lost; released updates also retain
	// at most sixty runs and ten HTML artifacts.
	SaveRun(context.Context, *IntelligenceRun, time.Time, bool) (bool, error)
	// GetRun returns the full observation with private recovery fields.
	GetRun(context.Context, string) (*IntelligenceRun, error)
	// ListRuns returns at most sixty summaries ordered newest first.
	ListRuns(context.Context, int64) ([]IntelligenceRun, error)
}

// IntelligenceGroupReader resolves the groups currently available to a
// personal user.
type IntelligenceGroupReader interface {
	// GetAvailableGroups returns the current personal group choices for a user.
	GetAvailableGroups(context.Context, int64) ([]routing.Group, error)
}

// IntelligenceClient creates remote checks and reads existing task
// observations.
type IntelligenceClient interface {
	// Create submits one remote check using the saved local idempotency identity.
	// A failure may leave acceptance uncertain, so callers persist submitting
	// first and avoid repeating the creation request.
	Create(context.Context, remote.CreateRequest, string) (*remote.TestResult, error)
	// Get reads an existing remote job using its private polling identifier.
	Get(context.Context, string) (*remote.TestResult, error)
}

// KeyReader resolves configured credentials using their persisted identity or supplied secret.
type KeyReader interface {
	// GetByID reads the current persisted record or returns a missing-record
	// error.
	GetByID(context.Context, int64) (*apikey.APIKey, error)
	// GetByKey resolves supplied credential text to its current persisted personal
	// Key.
	GetByKey(context.Context, string) (*apikey.APIKey, error)
}

// GroupReader reads the current group status before configuration or remote submission.
type GroupReader interface {
	// GetByID reads the current persisted record or returns a missing-record
	// error.
	GetByID(context.Context, int64) (*routing.Group, error)
}

// String returns a fixed credential-redacted description of configuration
// input.
func (IntelligenceConfigInput) String() string {
	return "IntelligenceConfigInput{credentials:redacted}"
}

// GoString supplies the same credential-redacted description for Go-syntax
// formatting.
func (v IntelligenceConfigInput) GoString() string { return v.String() }
