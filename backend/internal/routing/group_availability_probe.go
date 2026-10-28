package routing

import (
	"context"
	"time"
)

// group_availability_probe.go defines active diagnostic outcomes and public
// observation summaries. Active diagnostics store one result per probe round,
// while the marketplace reads eligible real requests from passive monitoring.
// Both readers retain missing ratios, and passive summaries include sampled
// health and committed coverage so absent data cannot imply healthy traffic.

// Probe outcome labels distinguish successful execution from failures in stored
// history and latest-state responses returned to marketplace consumers.
const (
	GroupAvailabilityProbeStatusSuccess = "success" // completed successful probe
	GroupAvailabilityProbeStatusFailed  = "failed"  // completed failed probe
)

// GroupAvailabilityProbeDueGroup contains the saved configuration claimed for
// one probe round.
type GroupAvailabilityProbeDueGroup struct {
	GroupID int64                        // claimed group row ID
	Name    string                       // claimed administrative group name
	Config  GroupAvailabilityProbeConfig // claimed availability probe settings
}

// GroupAvailabilityProbeResult contains one attempt outcome selected for a
// round's final sample.
type GroupAvailabilityProbeResult struct {
	GroupID int64 // observed group row ID
	// ProviderID is absent when provider selection fails or a synthetic
	// configuration failure is recorded. A selected test attempt retains the
	// provider ID even when execution fails.
	ProviderID *int64 // selected provider row ID
	// ModelID records the configured request name so probe history remains tied to
	// the administrator's selection while testing executes the separately mapped
	// provider model.
	ModelID string // configured request model ID
	// Status and Success describe the selected attempt outcome. Success stops
	// retrying and supplies summary counts, while Status becomes the stored
	// latest-state label for the group.
	Status       string // persisted probe status string
	Success      bool   // attempt success decision flag
	LatencyMs    int64  // attempt latency in milliseconds
	ErrorMessage string // attempt failure diagnostic text
	// StartedAt and FinishedAt belong to the final selected attempt rather than
	// the whole retry round. History window counts use StartedAt, and latest
	// checked time uses FinishedAt.
	StartedAt  time.Time // recorded attempt start time
	FinishedAt time.Time // recorded attempt finish time
}

// GroupAvailabilityHealth contains sampled channel health for a marketplace observation interval.
type GroupAvailabilityHealth struct {
	// Status and Score use the configured request minimum. TTFTP50Ms and
	// CacheRate additionally require enough observations for their own signals;
	// absent pointers represent unavailable measurements rather than zero values.
	Status    string   // sampled health category label
	Score     *float64 // optional weighted health score
	TTFTP50Ms *int64   // milliseconds
	CacheRate *float64 // observed cache read ratio
}

// GroupAvailabilityBucket aggregates observations within one displayed marketplace interval.
type GroupAvailabilityBucket struct {
	Date string // formatted time-bucket start label
	// SuccessCount and TotalCount count observations supplied by the owning
	// reader. Active diagnostics count rounds; passive monitoring counts requests
	// and withholds AvailabilityRate until its minimum sample requirement is met.
	SuccessCount     int64                    // successful observations in bucket
	TotalCount       int64                    // all observations in bucket
	AvailabilityRate *float64                 // success fraction in bucket
	Health           *GroupAvailabilityHealth // optional passive health assessment
}

// GroupAvailabilitySummary combines window observations, sampled health, and the reader's latest status.
type GroupAvailabilitySummary struct {
	WindowDays    int // nominal observation-window day count
	BucketMinutes int // observation bucket width in minutes
	// SuccessCount and TotalCount describe the selected reader's observations
	// within the window. Passive monitoring requires enough requests before
	// publishing AvailabilityRate or a measured health category.
	SuccessCount     int64    // successful observations in window
	TotalCount       int64    // all observations in window
	AvailabilityRate *float64 // window success fraction
	// LastStatus and LastCheckedAt describe the reader's latest observation.
	// Active diagnostics can use a round before the window; passive monitoring
	// uses its newest nonempty bucket and the committed observation cutoff.
	LastStatus    string                    // latest observed status string
	LastCheckedAt *time.Time                // latest observation finish time
	Days          []GroupAvailabilityBucket // ordered observation time buckets
	// Health, DataThrough, CoverageComplete, and RefreshIntervalSeconds are
	// supplied by passive monitoring. Active diagnostic readers leave them unset;
	// public consumers interpret coverage and freshness when passive data exists.
	Health                 *GroupAvailabilityHealth // optional passive window health
	DataThrough            *time.Time               // latest committed aggregation cutoff
	CoverageComplete       bool                     // selected history fully recomputed
	RefreshIntervalSeconds int                      // seconds
}

// GroupAvailabilityProbeRepository manages probe claims, atomic result
// scheduling, and observation queries.
type GroupAvailabilityProbeRepository interface {
	// ClaimDue leases due groups until lockUntil for the supplied instance
	// identity. PostgreSQL selects ten when limit is nonpositive and otherwise
	// caps the batch at that positive limit; callers supply their worker bound and
	// launch claimed groups promptly.
	ClaimDue(ctx context.Context, now time.Time, lockUntil time.Time, lockedBy string, limit int) ([]GroupAvailabilityProbeDueGroup, error)
	// SaveResultAndScheduleNext persists one final observation and advances the
	// group's next-run time. A successful PostgreSQL commit publishes history and
	// clears the claim lease together.
	//
	// The history insertion and schedule update share a transaction, so a later
	// storage failure rolls the observation back rather than leaving a sample with
	// an unrelated schedule. Callers supply the round context and retain failed
	// claims for lease recovery if saving cannot complete. PostgreSQL addresses the
	// schedule by GroupID alone, so callers finish within the claim lease; a late
	// save can clear a newer owner's lease.
	SaveResultAndScheduleNext(ctx context.Context, result *GroupAvailabilityProbeResult, nextRunAt time.Time) error
	// GetSummaryByGroupIDs returns window counts, time buckets, and the latest
	// status for selected groups. Ratios use success divided by total and remain
	// nil when unobserved; latest status is queried separately and may precede the
	// window.
	GetSummaryByGroupIDs(ctx context.Context, groupIDs []int64, days int, bucketMinutes int, timezone string, now time.Time) (map[int64]*GroupAvailabilitySummary, error)
	// CleanupOldResults deletes stored probe observations older than the supplied
	// cutoff. The runner schedules this as retention maintenance with its own
	// timeout, allowing a failure to leave probes and later cleanup attempts
	// available.
	CleanupOldResults(ctx context.Context, before time.Time) error
}
