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
	Source string `json:"source"` // site or user history

	ID        int64  `json:"id"`         // local record identifier
	GroupID   int64  `json:"group_id"`   // configured routing group
	GroupName string `json:"group_name"` // current group name
	Model     string `json:"model"`      // requested model identifier
	Benchmark string `json:"benchmark"`  // candy or drawing benchmark
	// Prompt supplies the drawing instruction. Empty values select the default
	// pelican cycling animation when the worker starts local generation.
	Prompt  string `json:"prompt"`   // optional drawing instruction
	BaseURL string `json:"base_url"` // public gateway endpoint
	// APIKeyID references the ordinary personal credential resolved before
	// submission. It is excluded from administrator and user JSON responses.
	APIKeyID         int64            `json:"-"`                    // private credential reference
	APIKeyConfigured bool             `json:"api_key_configured"`   // saved credential presence
	Protocol         string           `json:"protocol"`             // gateway request protocol
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
	// Prompt configures drawing generation. Candy tests reject nonempty values,
	// while an empty drawing prompt selects the service default.
	Prompt  string `json:"prompt"`   // optional drawing instruction
	BaseURL string `json:"base_url"` // public gateway endpoint
	// APIKey supplies a secret for configuration validation. Empty edit input
	// retains the existing stored Key reference.
	APIKey          string `json:"api_key"`          // supplied gateway credential
	Protocol        string `json:"protocol"`         // gateway request protocol
	ReasoningEffort string `json:"reasoning_effort"` // requested reasoning level
	ServiceTier     string `json:"service_tier"`     // requested service tier
	Enabled         bool   `json:"enabled"`          // configuration availability switch
	ScheduleEnabled bool   `json:"schedule_enabled"` // periodic submission switch
	IntervalMinutes int    `json:"interval_minutes"` // scheduled interval minutes
}

// IntelligenceRun stores an observation with separately persisted private
// worker recovery state.
type IntelligenceRun struct {
	ID              string `json:"id"`                         // local record identifier
	ConfigID        int64  `json:"config_id"`                  // owning configuration identifier
	GroupID         int64  `json:"group_id"`                   // configured routing group
	Model           string `json:"model"`                      // requested model identifier
	Benchmark       string `json:"benchmark"`                  // candy or drawing benchmark
	Status          string `json:"status"`                     // job execution state
	Verdict         string `json:"verdict"`                    // normalized assessment outcome
	Phase           string `json:"phase"`                      // current execution phase
	Question        string `json:"question,omitempty"`         // reported benchmark prompt
	Answer          string `json:"answer,omitempty"`           // reported model answer
	HTML            string `json:"html,omitempty"`             // untrusted drawing document
	InputTokens     *int64 `json:"input_tokens,omitempty"`     // reported input tokens
	OutputTokens    *int64 `json:"output_tokens,omitempty"`    // reported output tokens
	ReasoningTokens *int64 `json:"reasoning_tokens,omitempty"` // reported reasoning tokens
	DurationMS      *int64 `json:"duration_ms,omitempty"`      // reported duration milliseconds
	// FirstTokenMS measures local request start to the first nonempty text or
	// reasoning delta. Missing observations remain nil for historical and buffered responses.
	FirstTokenMS     *int64     `json:"first_token_ms,omitempty"`    // first token milliseconds
	AssessmentReason string     `json:"assessment_reason,omitempty"` // remote assessment explanation
	ErrorMessage     string     `json:"error_message,omitempty"`     // safe local diagnostic
	HasDetail        bool       `json:"has_detail"`                  // available observation detail
	HasArtifact      bool       `json:"has_artifact"`                // retained drawing presence
	HasThumbnail     bool       `json:"has_thumbnail"`               // retained screenshot presence
	CreatedAt        time.Time  `json:"created_at"`                  // record creation timestamp
	UpdatedAt        time.Time  `json:"updated_at"`                  // latest observation timestamp
	FinishedAt       *time.Time `json:"finished_at,omitempty"`       // terminal observation timestamp
	// RemoteID is the capability used to poll an accepted remote job. SQL stores
	// it separately, and public JSON excludes it.
	RemoteID string `json:"-"` // private polling credential
	// LeaseToken identifies the current worker claim. SQL requires a matching,
	// unexpired claim before accepting result updates from that worker.
	LeaseToken string `json:"-"` // private worker ownership
	// LocalGeneration marks artwork generated by this service. Recovery reads it
	// from the private payload to keep remote evaluation separate from generation.
	LocalGeneration bool `json:"-"` // local artwork ownership
	// AssessmentAttempts is persisted before each HTML submission. Recovery uses
	// the accumulated count to bound classifier retries across worker replacements.
	AssessmentAttempts int `json:"-"` // HTML submission attempt count
}

// IntelligenceTest exposes shared site and user histories within authorized groups.
type IntelligenceTest struct {
	Source         string            `json:"source"`          // site or user history
	ID             int64             `json:"id"`              // local record identifier
	GroupID        int64             `json:"group_id"`        // configured routing group
	GroupName      string            `json:"group_name"`      // current group name
	RateMultiplier float64           `json:"rate_multiplier"` // current group billing multiplier
	Model          string            `json:"model"`           // requested model identifier
	Benchmark      string            `json:"benchmark"`       // candy or drawing benchmark
	Runs           []IntelligenceRun `json:"runs"`            // lightweight execution history
	Artifacts      []IntelligenceRun `json:"artifacts"`       // retained artwork summaries
}

// IntelligenceRepository persists configurations and conditionally advances
// jobs using expiring worker leases.
type IntelligenceRepository interface {
	// ListConfigs returns bounded configurations in group display order.
	ListConfigs(context.Context) ([]IntelligenceConfig, error)
	// ListCommunityConfigs reads public user-submitted check configurations.
	// Callers filter current group access before exposing model histories, and
	// administrators continue managing site settings through the separate site listing.
	ListCommunityConfigs(context.Context) ([]IntelligenceConfig, error)
	// CreateCommunityRun updates an idle source row and queues its drawing atomically.
	// Submission serializes credential and source identities, admits one active
	// community job per Key, and bounds the number of community configurations.
	CreateCommunityRun(context.Context, *IntelligenceConfig, time.Time) (*IntelligenceRun, error)
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
	// GetThumbnail reads the retained PNG screenshot for one observation.
	// Missing screenshots return nil; missing observations return the module's
	// not-found error so callers can distinguish rendering work from deleted history.
	GetThumbnail(context.Context, string) ([]byte, error)
	// SaveThumbnail stores a PNG while the run retains its drawing document.
	// The screenshot survives later HTML retention trimming and remains private
	// payload content omitted from history and detail JSON responses.
	SaveThumbnail(context.Context, string, []byte) error
}

// IntelligenceSelfTestInput selects the caller's credential for a system-configured drawing.
type IntelligenceSelfTestInput struct {
	APIKeyID int64 `json:"api_key_id"` // selected personal credential
	// ConfigID selects an enabled site drawing in the Key's group. Zero uses
	// the first enabled drawing in repository display order for older clients.
	ConfigID int64 `json:"config_id,omitempty"` // selected site configuration
}

// IntelligenceThumbnailRenderer captures untrusted drawing documents as static PNG images.
type IntelligenceThumbnailRenderer interface {
	// Render creates one bounded screenshot with document scripts isolated from
	// host credentials and network resources. Caller cancellation terminates the
	// rendering process and releases its temporary browser profile.
	Render(context.Context, string) ([]byte, error)
}

// IntelligenceGroupReader resolves the groups currently available to a
// personal user.
type IntelligenceGroupReader interface {
	// GetAvailableGroups returns the current personal group choices for a user.
	GetAvailableGroups(context.Context, int64) ([]routing.Group, error)
}

// IntelligenceClient generates gateway artwork, creates remote checks, and reads
// existing task observations.
type IntelligenceClient interface {
	// Generate requests drawing HTML from the configured gateway using its personal
	// credential. Callers persist generation intent first because an interrupted
	// request may consume quota and must not be repeated automatically.
	Generate(context.Context, remote.CreateRequest, string) (*remote.DrawingResult, error)
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
