package payment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/shopspring/decimal"
)

// recharge_bonus.go quotes cumulative fixed tier rewards and scheduled extra
// gifts for confirmed recharge payments. Decimal arithmetic is adapted from
// Wei-Shaw/sub2api at revision
// d851a38477597157e78450d30af2d8504764d085, distributed under LGPL-3.0.
// Each campaign accumulates purchased API credit after the recharge multiplier.
// Its highest eligible tier names the total fixed reward, and a payment receives
// the difference from fixed rewards already allocated to that buyer.
//
// Optional activity dates gate both components, while individual extra windows
// gate the scheduled percentage. Configuration is validated before publication,
// and successful payment confirmation records the final components while
// serializing that buyer's campaign progress. Checkout previews remain estimates
// until that transaction commits. Later fulfillment retries consume the saved
// entitlement, while malformed schedules disable gifts and keep ordinary recharge
// available. Scheduled percentages are independent of the fixed tier reward cap.

// Recharge settings retain the upstream tier and notice keys while extending
// their shared checkout calculation with an optional scheduled activity record.
const (
	SettingRechargeBonusTiers    = "RECHARGE_BONUS_TIERS"
	SettingRechargeBonusNotice   = "RECHARGE_BONUS_NOTICE"
	SettingRechargeBonusActivity = "RECHARGE_BONUS_ACTIVITY"
	SettingRechargeBonusContent  = "RECHARGE_BONUS_CONTENT"
	maxRechargeBonusTiers        = 20
	maxRechargeBonusPercent      = 1000
)

// rechargeActivitySlug restricts public activity paths to stable lowercase URL
// segments, keeping administrator input out of route and query syntax.
var (
	rechargeActivitySlug = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	// rechargeFAQID accepts editor-generated stable IDs and derived legacy IDs
	// while bounding the field names retained in each localized content map.
	rechargeFAQID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,32}$`)
)

// RechargeBonusTier selects a total fixed reward from cumulative purchased credit.
type RechargeBonusTier struct {
	MinAmount   float64 `json:"min_amount"`   // cumulative purchased credit threshold
	BonusAmount float64 `json:"bonus_amount"` // total campaign tier reward
}

// RechargeBonusWindow supplies an extra percentage during one half-open UTC interval.
type RechargeBonusWindow struct {
	Enabled      bool      `json:"enabled"`       // scheduled gift enable flag
	StartsAt     time.Time `json:"starts_at"`     // inclusive interval start instant
	EndsAt       time.Time `json:"ends_at"`       // exclusive interval end instant
	BonusPercent float64   `json:"bonus_percent"` // credited base percentage points
}

// RechargeBonusFAQ contains one administrator-authored activity question and answer.
type RechargeBonusFAQ struct {
	ID       string `json:"id"`       // stable localization question identity
	Question string `json:"question"` // public activity question text
	Answer   string `json:"answer"`   // public activity answer text
}

// RechargeBonusActivity combines public presentation with the schedule used at payment confirmation.
type RechargeBonusActivity struct {
	// CampaignID identifies one cumulative reward ledger across presentation and
	// schedule edits. A different campaign ID starts a separate buyer accumulation.
	CampaignID string `json:"campaign_id"`
	// PageEnabled publishes the landing page while monetary eligibility remains
	// determined by the configured tiers, activity endpoints, and enabled extra windows.
	PageEnabled bool   `json:"page_enabled"` // public landing page enablement
	Slug        string `json:"slug"`         // public activity route segment
	Title       string `json:"title"`        // administrator activity display title
	Subtitle    string `json:"subtitle"`     // administrator activity introduction text
	Timezone    string `json:"timezone"`     // IANA display timezone identifier
	// StartsAt and EndsAt bound both tier and scheduled gifts independently of
	// PageEnabled. Omitted endpoints leave that side of the activity interval open.
	StartsAt *time.Time `json:"starts_at,omitempty"` // inclusive activity start instant
	EndsAt   *time.Time `json:"ends_at,omitempty"`   // exclusive activity end instant
	// TimeWindows is normalized to a sorted independent slice before publication.
	// Enabled windows cannot overlap, making the selected extra percentage unique.
	TimeWindows []RechargeBonusWindow `json:"time_windows"` // scheduled extra gift intervals
	Rules       string                `json:"rules"`        // public activity rule paragraphs
	FAQs        []RechargeBonusFAQ    `json:"faqs"`         // ordered public activity questions
	// invalid records a failed stored-data parse. Confirmation then quotes the ordinary
	// credited base so malformed schedule data cannot enable an unlimited gift.
	invalid bool
}

// RechargeBonusQuote records the credit and gift quantities selected for one order.
type RechargeBonusQuote struct {
	BaseAmount         float64   `json:"base_amount"`          // purchased credit before gifts
	TierAmount         float64   `json:"tier_bonus_amount"`    // selected tier gift credit
	TimeAmount         float64   `json:"time_bonus_amount"`    // selected scheduled gift credit
	BonusAmount        float64   `json:"bonus_amount"`         // combined free credit amount
	Amount             float64   `json:"amount"`               // final credited entitlement amount
	TimePercent        float64   `json:"time_percent"`         // matched extra percentage points
	MinAmount          float64   `json:"min_amount"`           // matched cumulative credit threshold
	CumulativeAmount   float64   `json:"cumulative_amount"`    // purchased campaign credit after payment
	TierRewardTotal    float64   `json:"tier_reward_total"`    // total allocated fixed campaign reward
	CumulativeBefore   float64   `json:"cumulative_before"`    // purchased campaign credit before payment
	PreviousTierReward float64   `json:"previous_tier_reward"` // fixed campaign rewards allocated before payment
	QuotedAt           time.Time `json:"quoted_at"`            // server payment observation instant
	Activity           string    `json:"activity"`             // configured activity route segment
	CampaignID         string    `json:"campaign_id"`          // stable cumulative campaign identity
}

// RechargeBonusProgress records purchased credit and fixed rewards allocated within one campaign.
type RechargeBonusProgress struct {
	CampaignID       string  `json:"campaign_id"`       // stable cumulative campaign identity
	CumulativeAmount float64 `json:"cumulative_amount"` // previously confirmed purchased campaign credit
	TierRewardTotal  float64 `json:"tier_reward_total"` // previously allocated fixed campaign reward
}

// NormalizeRechargeBonusTiers validates and sorts an independent tier list for saving.
// Empty lists disable tier gifts. Duplicate thresholds, nonfinite values, extra
// fractional digits, and decreasing fixed rewards are rejected.
func NormalizeRechargeBonusTiers(raw []RechargeBonusTier) ([]RechargeBonusTier, error) {
	if len(raw) > maxRechargeBonusTiers {
		return nil, fmt.Errorf("at most %d recharge tiers are allowed", maxRechargeBonusTiers)
	}
	out := make([]RechargeBonusTier, 0, len(raw))
	seen := make(map[string]bool, len(raw))
	for _, tier := range raw {
		if !rechargeBonusValueValid(tier.MinAmount, math.MaxFloat64) ||
			!rechargeBonusValueValid(tier.BonusAmount, math.MaxFloat64) {
			return nil, fmt.Errorf("recharge thresholds and fixed rewards must be nonnegative with at most two decimals")
		}
		key := decimal.NewFromFloat(tier.MinAmount).StringFixed(2)
		if seen[key] {
			return nil, fmt.Errorf("duplicate recharge threshold: %s", key)
		}
		seen[key] = true
		out = append(out, tier)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].MinAmount < out[j].MinAmount })
	for i := 1; i < len(out); i++ {
		if out[i].BonusAmount < out[i-1].BonusAmount {
			return nil, fmt.Errorf("cumulative tier rewards cannot decrease at higher thresholds")
		}
	}
	return out, nil
}

// rechargeBonusValueValid reports whether a value fits upstream monetary precision.
// The caller supplies the inclusive upper limit; nonfinite or negative values
// and values requiring more than two fractional digits are rejected.
func rechargeBonusValueValid(value, maximum float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= maximum &&
		decimal.NewFromFloat(value).Equal(decimal.NewFromFloat(value).Round(2))
}

// NormalizeRechargeBonusActivity validates public text and activity/window ordering for saving.
// Blank timezones select the application's local timezone. Returned endpoint
// pointers and collections are copied, times use UTC, and windows are sorted.
//
// Every extra interval must fit inside the supplied activity endpoints. Enabled
// intervals cannot overlap, so checkout selects one extra percentage regardless
// of editor row order. Disabled rows retain their configuration for later editing,
// while published pages additionally require a usable route slug and title.
func NormalizeRechargeBonusActivity(value RechargeBonusActivity) (RechargeBonusActivity, error) {
	value.CampaignID = strings.TrimSpace(value.CampaignID)
	if value.CampaignID != "" && !rechargeFAQID.MatchString(value.CampaignID) {
		return value, fmt.Errorf("campaign ID must contain at most 32 letters, digits, underscores, or hyphens")
	}
	value.Slug = strings.TrimSpace(value.Slug)
	value.Title = strings.TrimSpace(value.Title)
	value.Subtitle = strings.TrimSpace(value.Subtitle)
	value.Timezone = strings.TrimSpace(value.Timezone)
	if value.Timezone == "" {
		value.Timezone = time.Local.String()
		// Bootstrap installs the configured site location. A generic process Local
		// name has no portable browser meaning, so use UTC before publishing it.
		if value.Timezone == "Local" {
			value.Timezone = "UTC"
		}
	}
	if _, err := time.LoadLocation(value.Timezone); err != nil {
		return value, fmt.Errorf("activity timezone is invalid")
	}
	if (value.Slug != "" && (!rechargeActivitySlug.MatchString(value.Slug) || len(value.Slug) > 80)) ||
		(value.PageEnabled && (value.Slug == "" || value.Title == "")) {
		return value, fmt.Errorf("published activities need a lowercase slug and a title")
	}
	if utf8.RuneCountInString(value.Title) > 160 || utf8.RuneCountInString(value.Subtitle) > 500 || utf8.RuneCountInString(value.Rules) > 10000 {
		return value, fmt.Errorf("activity presentation text exceeds its length limit")
	}
	if value.StartsAt != nil && value.EndsAt != nil && !value.EndsAt.After(*value.StartsAt) {
		return value, fmt.Errorf("activity end must follow its start")
	}
	if len(value.TimeWindows) > 20 || len(value.FAQs) > 12 {
		return value, fmt.Errorf("at most 20 extra windows and 12 questions are allowed")
	}
	// Canonicalize independent endpoint values before serializing them. Retaining
	// the caller's pointers would let later editor mutations change this validated schedule.
	if value.StartsAt != nil {
		startsAt := value.StartsAt.UTC()
		value.StartsAt = &startsAt
	}
	if value.EndsAt != nil {
		endsAt := value.EndsAt.UTC()
		value.EndsAt = &endsAt
	}
	value.TimeWindows = append([]RechargeBonusWindow{}, value.TimeWindows...)
	value.FAQs = append([]RechargeBonusFAQ{}, value.FAQs...)
	for i := range value.TimeWindows {
		value.TimeWindows[i].StartsAt = value.TimeWindows[i].StartsAt.UTC()
		value.TimeWindows[i].EndsAt = value.TimeWindows[i].EndsAt.UTC()
	}
	sort.Slice(value.TimeWindows, func(i, j int) bool {
		return value.TimeWindows[i].StartsAt.Before(value.TimeWindows[j].StartsAt)
	})
	var previousEnd time.Time
	for _, window := range value.TimeWindows {
		if window.StartsAt.IsZero() || !window.EndsAt.After(window.StartsAt) ||
			!rechargeBonusValueValid(window.BonusPercent, maxRechargeBonusPercent) {
			return value, fmt.Errorf("extra windows need valid dates and a percentage between 0 and %d", maxRechargeBonusPercent)
		}
		if (value.StartsAt != nil && window.StartsAt.Before(*value.StartsAt)) ||
			(value.EndsAt != nil && window.EndsAt.After(*value.EndsAt)) {
			return value, fmt.Errorf("extra windows must fall inside the activity period")
		}
		if window.Enabled {
			if window.StartsAt.Before(previousEnd) {
				return value, fmt.Errorf("enabled extra windows cannot overlap")
			}
			previousEnd = window.EndsAt
		}
	}
	seenQuestions := map[string]bool{}
	for i := range value.FAQs {
		faq := &value.FAQs[i]
		if faq.ID == "" {
			// Historical drafts lack editor IDs. Derive identity from source content
			// so repeated reads and row reordering keep each question's translation keys stable.
			digest := sha256.Sum256([]byte(faq.Question + "\x00" + faq.Answer))
			// A sixteen-character digest prefix fits the editor's stable-ID limit.
			faq.ID = hex.EncodeToString(digest[:8])
		}
		if !rechargeFAQID.MatchString(faq.ID) || seenQuestions[faq.ID] {
			return value, fmt.Errorf("activity questions need unique stable IDs of at most 32 characters")
		}
		seenQuestions[faq.ID] = true
		if strings.TrimSpace(faq.Question) == "" || strings.TrimSpace(faq.Answer) == "" ||
			utf8.RuneCountInString(faq.Question) > 300 || utf8.RuneCountInString(faq.Answer) > 3000 {
			return value, fmt.Errorf("activity questions and answers must contain text within their length limits")
		}
	}
	return value, nil
}

// parseRechargeBonusTiers reads stored upstream tiers into a validated sorted list.
// Missing configuration disables tiers. Malformed stored lists select no tiers
// so invalid historical input cannot create an unvalidated monetary grant.
func parseRechargeBonusTiers(raw string) []RechargeBonusTier {
	var tiers []RechargeBonusTier
	if strings.TrimSpace(raw) == "" {
		return []RechargeBonusTier{}
	}
	if json.Unmarshal([]byte(raw), &tiers) != nil {
		// Stored JSON can bypass current settings validation. Treat a broken list
		// as disabled instead of allowing decoding errors to interrupt ordinary checkout.
		return []RechargeBonusTier{}
	}
	result, err := NormalizeRechargeBonusTiers(tiers)
	if err != nil {
		// A stored list bypassed current write validation. Disable its gifts until
		// an administrator repairs the invalid configuration through the settings API.
		return []RechargeBonusTier{}
	}
	return result
}

// parseRechargeBonusActivity distinguishes absent activity data from invalid stored schedules.
// An absent record leaves campaign gifts disabled. Invalid stored data marks the
// result unusable so confirmation cannot turn an intended bounded gift into an unbounded one.
//
// Parsing and normalization share this failure marker because malformed JSON
// and invalid dates both lose the intended eligibility boundaries. Configuration
// readers also remove tiers from public previews when this marker is set, keeping
// the displayed offer consistent with checkout while allowing ordinary purchases.
func parseRechargeBonusActivity(raw string) RechargeBonusActivity {
	var value RechargeBonusActivity
	if strings.TrimSpace(raw) != "" && json.Unmarshal([]byte(raw), &value) != nil {
		// Discard the entire schedule after decoding fails, including partial data,
		// so an unparsed endpoint cannot accidentally remove an intended gift restriction.
		return RechargeBonusActivity{invalid: true}
	}
	result, err := NormalizeRechargeBonusActivity(value)
	if err != nil {
		// Invalid historical schedules must fail closed for money while preserving
		// ordinary recharge availability and allowing administrators to repair configuration.
		return RechargeBonusActivity{invalid: true}
	}
	return result
}

// IsPublishedForSlug reports whether the requested public activity has valid presentation.
func (a RechargeBonusActivity) IsPublishedForSlug(slug string) bool {
	return !a.invalid && a.PageEnabled && a.Slug != "" && a.Slug == slug && a.Title != ""
}

// prepareRechargeBonusSettings validates supplied gifts and appends their serialized settings.
// Nil request fields keep their stored keys untouched. The caller owns the
// destination map and persists it together with the other validated payment settings.
func prepareRechargeBonusSettings(req UpdatePaymentConfigRequest, values map[string]string) error {
	if req.RechargeBonusTiers != nil {
		tiers, err := NormalizeRechargeBonusTiers(*req.RechargeBonusTiers)
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(tiers)
		if err != nil {
			return err
		}
		values[SettingRechargeBonusTiers] = string(encoded)
	}
	if req.RechargeBonusNotice != nil {
		if utf8.RuneCountInString(*req.RechargeBonusNotice) > 10000 {
			return fmt.Errorf("recharge notice cannot exceed 10000 characters")
		}
		values[SettingRechargeBonusNotice] = strings.TrimSpace(*req.RechargeBonusNotice)
	}
	if req.RechargeBonusActivity != nil {
		activity, err := NormalizeRechargeBonusActivity(*req.RechargeBonusActivity)
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(activity)
		if err != nil {
			return err
		}
		values[SettingRechargeBonusActivity] = string(encoded)
	}
	return nil
}

// QuoteRechargeBonus estimates one payment's cumulative and scheduled gifts.
// The principal is multiplied into purchased API credit before tier selection.
// Supply a finite positive principal already validated by checkout amount rules.
// Optional progress supplies already confirmed credit and fixed rewards; omitted
// progress starts at zero for anonymous previews. Confirmation uses a serialized
// progress observation and the stored purchased base before saving final terms.
func QuoteRechargeBonus(cfg *PaymentConfig, principal float64, at time.Time, progress ...RechargeBonusProgress) RechargeBonusQuote {
	multiplier := DefaultBalanceRechargeMultiplier
	if cfg != nil {
		multiplier = cfg.BalanceRechargeMultiplier
	}
	base := CalculateCreditedBalance(principal, multiplier)
	previous := RechargeBonusProgress{}
	if len(progress) > 0 {
		previous = progress[0]
	}
	return QuoteRechargeBonusForBase(cfg, base, at, previous)
}

// QuoteRechargeBonusForBase combines purchased campaign credit with previous fixed rewards.
// Callers supply the order's finite purchased API credit and finite nonnegative
// progress from the same campaign. Confirmation serializes progress reads and persists the
// returned order terms, while page previews use this calculation as an estimate.
//
// The highest cumulative threshold determines the total fixed campaign reward.
// Only its positive difference from previously allocated rewards is granted;
// reductions in configured rewards cannot reclaim an earlier grant. An enabled
// time window adds its percentage of this payment's purchased base independently.
// Both activity and extra intervals include their start and exclude their end.
func QuoteRechargeBonusForBase(cfg *PaymentConfig, base float64, at time.Time, previous RechargeBonusProgress) RechargeBonusQuote {
	quote := RechargeBonusQuote{BaseAmount: base, Amount: base, QuotedAt: at.UTC()}
	// Finite input amounts can overflow after multiplication. Return that amount
	// for checkout rejection before another decimal conversion could panic on infinity.
	if math.IsNaN(base) || math.IsInf(base, 0) || base <= 0 || cfg == nil {
		return quote
	}
	activity := cfg.RechargeBonusActivity
	quote.Activity = activity.Slug
	quote.CampaignID = activity.CampaignID
	if activity.invalid || activity.CampaignID == "" ||
		(activity.StartsAt != nil && at.Before(*activity.StartsAt)) ||
		(activity.EndsAt != nil && !at.Before(*activity.EndsAt)) {
		return quote
	}
	if previous.CampaignID != activity.CampaignID {
		previous = RechargeBonusProgress{CampaignID: activity.CampaignID}
	}
	quote.CumulativeAmount = addRechargeBonus(previous.CumulativeAmount, base)
	quote.CumulativeBefore = previous.CumulativeAmount
	quote.PreviousTierReward = previous.TierRewardTotal
	quote.TierRewardTotal = previous.TierRewardTotal
	for _, tier := range cfg.RechargeBonusTiers {
		// Retain the decimal comparison tolerance for sums such as 0.1 plus 0.2
		// reaching a configured cumulative-credit threshold of 0.3.
		if quote.CumulativeAmount+1e-9 >= tier.MinAmount && tier.MinAmount >= quote.MinAmount {
			quote.TierRewardTotal = math.Max(previous.TierRewardTotal, tier.BonusAmount)
			quote.MinAmount = tier.MinAmount
		}
	}
	quote.TierAmount = decimal.NewFromFloat(quote.TierRewardTotal).
		Sub(decimal.NewFromFloat(previous.TierRewardTotal)).Round(2).InexactFloat64()
	for _, window := range activity.TimeWindows {
		if window.Enabled && !at.Before(window.StartsAt) && at.Before(window.EndsAt) {
			quote.TimePercent = window.BonusPercent
			break
		}
	}
	quote.TimeAmount = calculateRechargeBonus(base, quote.TimePercent)
	quote.BonusAmount = addRechargeBonus(quote.TierAmount, quote.TimeAmount)
	quote.Amount = addRechargeBonus(base, quote.BonusAmount)
	return quote
}

// calculateRechargeBonus applies an upstream gift percentage and rounds credit to cents.
// Supply finite base and percentage values from validated checkout configuration;
// a result beyond float64's range remains infinite for checkout rejection.
func calculateRechargeBonus(base, percent float64) float64 {
	return decimal.NewFromFloat(base).
		Mul(decimal.NewFromFloat(percent)).
		Div(decimal.NewFromInt(100)).
		Round(2).
		InexactFloat64()
}

// addRechargeBonus sums independent credited quantities using the upstream two-decimal rounding.
// Nonfinite operands propagate without decimal conversion so checkout can reject
// unrepresentable gifts using the same validation as an unrepresentable credited base.
func addRechargeBonus(base, bonus float64) float64 {
	// Percentage multiplication can overflow a finite base. Preserve its invalid
	// result until checkout rejects the quote instead of panicking during decimal conversion.
	if math.IsNaN(base) || math.IsInf(base, 0) || math.IsNaN(bonus) || math.IsInf(bonus, 0) {
		return base + bonus
	}
	return decimal.NewFromFloat(base).Add(decimal.NewFromFloat(bonus)).Round(2).InexactFloat64()
}

// Snapshot returns scalar JSON values recording the selected order gift calculation.
// The new map owns its entries; amounts and identifiers are copied directly so
// the persisted order never needs to reread mutable activity settings during fulfillment.
func (q RechargeBonusQuote) Snapshot() map[string]any {
	return map[string]any{
		"base_amount": q.BaseAmount, "tier_bonus_amount": q.TierAmount,
		"time_bonus_amount": q.TimeAmount, "tier_reward_total": q.TierRewardTotal,
		"time_percent": q.TimePercent, "min_amount": q.MinAmount,
		"quoted_at": q.QuotedAt.Format(time.RFC3339Nano), "activity": q.Activity,
		"campaign_id": q.CampaignID, "cumulative_amount": q.CumulativeAmount,
		"cumulative_before": q.CumulativeBefore, "previous_tier_reward": q.PreviousTierReward,
		"bonus_amount": q.BonusAmount, "amount": q.Amount,
	}
}

// GetRechargeBonusProgress reads confirmed purchased credit and fixed campaign rewards.
// Empty campaign IDs and unauthenticated user IDs return zero progress. A
// configured buyer preview requires the storage reader to succeed so a failed
// read cannot display an incorrectly repeated fixed reward.
func (s *ConfigService) GetRechargeBonusProgress(ctx context.Context, userID int64, campaignID string) (RechargeBonusProgress, error) {
	progress := RechargeBonusProgress{CampaignID: campaignID}
	if campaignID == "" || userID <= 0 {
		return progress, nil
	}
	reader, ok := s.store.(interface {
		// RechargeBonusProgress reads confirmed accumulation for one buyer and campaign.
		RechargeBonusProgress(context.Context, int64, string) (RechargeBonusProgress, error)
	})
	if !ok {
		return progress, fmt.Errorf("recharge campaign progress reader is unavailable")
	}
	return reader.RechargeBonusProgress(ctx, userID, campaignID)
}
