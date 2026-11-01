package payment

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// recharge_bonus_test.go verifies cumulative fixed rewards and independent
// scheduled gifts. Fixed instants exercise half-open activity bounds,
// component rounding, independent gifts, snapshot ownership, configuration
// rejection, and the purchased-credit quantities used by referrals and refunds.

// TestRechargeBonusQuote combines cumulative fixed rewards with scheduled payment gifts.
// Confirmed purchases advance one campaign, grant only the newly reached reward
// difference, and keep extra percentages independent of the highest tier cap.
func TestRechargeBonusQuote(t *testing.T) {
	at := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	end := at.Add(24 * time.Hour)
	cfg := &PaymentConfig{
		BalanceRechargeMultiplier: 1,
		RechargeBonusTiers:        rechargeFixedTiers(),
		RechargeBonusActivity: RechargeBonusActivity{
			CampaignID: "double11-2026", StartsAt: &at, EndsAt: &end,
			TimeWindows: []RechargeBonusWindow{{Enabled: true, StartsAt: at, EndsAt: end, BonusPercent: 5}},
		},
	}
	first := QuoteRechargeBonus(cfg, 100, at)
	require.Equal(t, 11.0, first.TierAmount)
	require.Equal(t, 5.0, first.TimeAmount)
	require.Equal(t, 116.0, first.Amount)
	progress := RechargeBonusProgress{CampaignID: "double11-2026", CumulativeAmount: 100, TierRewardTotal: 11}
	next := QuoteRechargeBonus(cfg, 200, at, progress)
	require.Equal(t, 300.0, next.CumulativeAmount)
	require.Equal(t, 22.0, next.TierAmount)
	require.Equal(t, 33.0, next.TierRewardTotal)
	require.Equal(t, 10.0, next.TimeAmount)
	require.Equal(t, 232.0, next.Amount)
	below := QuoteRechargeBonus(cfg, 99, at)
	require.Zero(t, below.TierAmount)
	require.Equal(t, 4.95, below.TimeAmount)
	capped := QuoteRechargeBonus(cfg, 20000, at)
	require.Equal(t, 1111.0, capped.TierAmount)
	require.Equal(t, 1000.0, capped.TimeAmount)
	repeated := QuoteRechargeBonus(cfg, 100, at, RechargeBonusProgress{CampaignID: "double11-2026", CumulativeAmount: 20000, TierRewardTotal: 1111})
	require.Zero(t, repeated.TierAmount)
	require.Equal(t, 5.0, repeated.TimeAmount)
	lowerRewards := *cfg
	lowerRewards.RechargeBonusTiers = []RechargeBonusTier{{MinAmount: 100, BonusAmount: 11}, {MinAmount: 300, BonusAmount: 22}}
	retained := QuoteRechargeBonus(&lowerRewards, 100, at, RechargeBonusProgress{CampaignID: "double11-2026", CumulativeAmount: 300, TierRewardTotal: 33})
	require.Zero(t, retained.TierAmount)
	require.Equal(t, 33.0, retained.TierRewardTotal)
	require.Zero(t, QuoteRechargeBonus(cfg, 1000, at.Add(-time.Nanosecond)).BonusAmount)
	require.Zero(t, QuoteRechargeBonus(cfg, 1000, end).BonusAmount)
	cfg.BalanceRechargeMultiplier = 0.14
	converted := QuoteRechargeBonus(cfg, 1000, at)
	require.Equal(t, 140.0, converted.BaseAmount)
	require.Equal(t, 11.0, converted.TierAmount)
	require.Equal(t, 7.0, converted.TimeAmount)
	require.Equal(t, 158.0, converted.Amount)
	require.Equal(t, 10.08, calculateRechargeBonus(100.75, 10))
	cfg.RechargeBonusActivity.TimeWindows[0].StartsAt = at.Add(time.Hour)
	cfg.RechargeBonusActivity.TimeWindows[0].EndsAt = at.Add(2 * time.Hour)
	require.Zero(t, QuoteRechargeBonus(cfg, 1000, at).TimeAmount)
	require.Equal(t, 7.0, QuoteRechargeBonus(cfg, 1000, at.Add(time.Hour)).TimeAmount)
	require.Zero(t, QuoteRechargeBonus(cfg, 1000, at.Add(2*time.Hour)).TimeAmount)
}

// rechargeFixedTiers returns the configured total-reward ladder for the campaign fixture.
func rechargeFixedTiers() []RechargeBonusTier {
	return []RechargeBonusTier{
		{MinAmount: 100, BonusAmount: 11},
		{MinAmount: 300, BonusAmount: 33},
		{MinAmount: 500, BonusAmount: 66},
		{MinAmount: 1000, BonusAmount: 121},
		{MinAmount: 3000, BonusAmount: 231},
		{MinAmount: 5000, BonusAmount: 451},
		{MinAmount: 10000, BonusAmount: 1111},
	}
}

// TestRechargeBonusConfiguration rejects invalid thresholds and ambiguous extra schedules.
// Valid writes sort independent copies, preserve omitted fields, and retain
// explicit empty lists so administrators can disable tier gifts.
func TestRechargeBonusConfiguration(t *testing.T) {
	for _, value := range []float64{-1, math.NaN(), math.Inf(1), 1.001} {
		_, err := NormalizeRechargeBonusTiers([]RechargeBonusTier{{MinAmount: value, BonusAmount: 10}})
		require.Error(t, err)
	}
	for _, percent := range []float64{-1, math.NaN(), math.Inf(1), 0.001} {
		_, err := NormalizeRechargeBonusTiers([]RechargeBonusTier{{MinAmount: 100, BonusAmount: percent}})
		require.Error(t, err)
	}
	_, err := NormalizeRechargeBonusTiers([]RechargeBonusTier{{MinAmount: 0, BonusAmount: 1000}})
	require.NoError(t, err)
	_, err = NormalizeRechargeBonusTiers([]RechargeBonusTier{{MinAmount: 100, BonusAmount: 10}, {MinAmount: 100, BonusAmount: 20}})
	require.Error(t, err)
	_, err = NormalizeRechargeBonusTiers([]RechargeBonusTier{{MinAmount: 100, BonusAmount: 20}, {MinAmount: 300, BonusAmount: 10}})
	require.Error(t, err)
	input := []RechargeBonusTier{{MinAmount: 1000, BonusAmount: 20}, {MinAmount: 100, BonusAmount: 10}}
	normalized, err := NormalizeRechargeBonusTiers(input)
	require.NoError(t, err)
	require.Equal(t, 100.0, normalized[0].MinAmount)
	require.Equal(t, 1000.0, input[0].MinAmount)
	at := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	window := RechargeBonusWindow{Enabled: true, StartsAt: at, EndsAt: at.Add(time.Hour), BonusPercent: 5}
	_, err = NormalizeRechargeBonusActivity(RechargeBonusActivity{TimeWindows: []RechargeBonusWindow{window, window}})
	require.Error(t, err)
	later := window
	later.StartsAt = window.EndsAt
	later.EndsAt = later.StartsAt.Add(time.Hour)
	_, err = NormalizeRechargeBonusActivity(RechargeBonusActivity{TimeWindows: []RechargeBonusWindow{later, window}})
	require.NoError(t, err)
	empty := []RechargeBonusTier{}
	settings, err := PreparePaymentConfig(UpdatePaymentConfigRequest{RechargeBonusTiers: &empty})
	require.NoError(t, err)
	require.Equal(t, map[string]string{SettingRechargeBonusTiers: "[]"}, settings)
	malformed := parseRechargeBonusActivity(`{"starts_at":"broken"}`)
	cfg := &PaymentConfig{BalanceRechargeMultiplier: 1, RechargeBonusTiers: normalized, RechargeBonusActivity: malformed}
	require.Zero(t, QuoteRechargeBonus(cfg, 1000, at).BonusAmount)
}

// TestRechargeBonusActivityUTC copies administrator schedule inputs during normalization.
// Stored endpoints and windows must use UTC without retaining mutable endpoint
// pointers or changing the caller's window and question collections.
func TestRechargeBonusActivityUTC(t *testing.T) {
	zone := time.FixedZone("fixture-offset", 9*60*60)
	start := time.Date(2026, 11, 11, 12, 0, 0, 0, zone)
	end := start.Add(3 * time.Hour)
	input := RechargeBonusActivity{
		Timezone: "Asia/Tokyo", StartsAt: &start, EndsAt: &end,
		TimeWindows: []RechargeBonusWindow{{
			Enabled: true, StartsAt: start.Add(time.Hour), EndsAt: start.Add(2 * time.Hour), BonusPercent: 5,
		}},
		FAQs: []RechargeBonusFAQ{{Question: "When?", Answer: "During the displayed period."}},
	}
	normalized, err := NormalizeRechargeBonusActivity(input)
	require.NoError(t, err)
	require.NotSame(t, input.StartsAt, normalized.StartsAt)
	require.NotSame(t, input.EndsAt, normalized.EndsAt)
	require.Equal(t, time.UTC, normalized.StartsAt.Location())
	require.Equal(t, time.UTC, normalized.EndsAt.Location())
	require.Equal(t, time.UTC, normalized.TimeWindows[0].StartsAt.Location())
	require.Equal(t, time.UTC, normalized.TimeWindows[0].EndsAt.Location())
	require.Equal(t, zone, input.StartsAt.Location())
	require.Equal(t, zone, input.TimeWindows[0].StartsAt.Location())
	wantStart := start.UTC()
	start = start.Add(-time.Hour)
	input.TimeWindows[0].BonusPercent = 99
	input.FAQs[0].Answer = "Changed later."
	require.Equal(t, wantStart, *normalized.StartsAt)
	require.Equal(t, 5.0, normalized.TimeWindows[0].BonusPercent)
	require.Equal(t, "During the displayed period.", normalized.FAQs[0].Answer)
	encoded, err := json.Marshal(normalized)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"starts_at":"2026-11-11T03:00:00Z"`)
}

// TestRechargeBonusOverflow preserves unrepresentable monetary results for checkout validation.
// Finite purchase inputs can overflow at several arithmetic stages, and none
// may panic before checkout rejects the resulting nonfinite total.
func TestRechargeBonusOverflow(t *testing.T) {
	for _, scenario := range []struct {
		name       string  // overflowing arithmetic stage name
		principal  float64 // finite purchase principal amount
		multiplier float64 // configured recharge credit multiplier
		tier       float64 // selected tier percentage points
		extra      float64 // scheduled extra percentage points
	}{
		{name: "base", principal: math.MaxFloat64, multiplier: 2},
		{name: "gift", principal: math.MaxFloat64 / 2, multiplier: 1, extra: 1000},
		{name: "combined gifts", principal: math.MaxFloat64 / 2, multiplier: 1, tier: math.MaxFloat64 * 0.75, extra: 150},
		{name: "total", principal: math.MaxFloat64 * 0.75, multiplier: 1, tier: math.MaxFloat64 * 0.75},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			at := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
			cfg := &PaymentConfig{
				BalanceRechargeMultiplier: scenario.multiplier,
				RechargeBonusTiers:        []RechargeBonusTier{{MinAmount: 0, BonusAmount: scenario.tier}},
				RechargeBonusActivity: RechargeBonusActivity{CampaignID: "overflow", TimeWindows: []RechargeBonusWindow{{
					Enabled: true, StartsAt: at, EndsAt: at.Add(time.Hour), BonusPercent: scenario.extra,
				}}},
			}
			require.NotPanics(t, func() {
				quote := QuoteRechargeBonus(cfg, scenario.principal, at)
				require.True(t, math.IsInf(quote.Amount, 1))
			})
		})
	}
}

// TestRechargeBonusSnapshotAndPurchasedCredit separates pending credit from allocated paid gifts.
// Checkout fixes purchased credit alone, while saved payment allocations retain
// their final amounts and free credit stays outside the referral base.
func TestRechargeBonusSnapshotAndPurchasedCredit(t *testing.T) {
	cfg := &PaymentConfig{
		BalanceRechargeMultiplier: 1, RechargeBonusTiers: rechargeFixedTiers(),
		RechargeBonusActivity: RechargeBonusActivity{CampaignID: "double11-2026"},
	}
	draft := BuildCheckoutDraft(CreateOrderRequest{UserID: 1, Amount: 1000, OrderType: OrderTypeBalance}, &Buyer{}, nil, cfg, 1000, 1000, FeeBreakdown{PayAmount: 1000}, nil)
	require.Equal(t, 1000.0, draft.Order.Amount)
	require.Equal(t, 1000.0, draft.Order.BaseAmount)
	require.Zero(t, draft.Order.BonusAmount)
	quote := QuoteRechargeBonus(cfg, 1000, time.Now())
	order := &Order{OrderType: OrderTypeBalance, Amount: quote.Amount, BaseAmount: quote.BaseAmount, BonusAmount: quote.BonusAmount, BonusSnapshot: quote.Snapshot()}
	cfg.RechargeBonusTiers[3].BonusAmount = 999
	copy := order.Clone()
	copy.BonusSnapshot["tier_reward_total"] = 999.0
	require.Equal(t, 121.0, order.BonusSnapshot["tier_reward_total"])
	points, ok := OrderPurchasedReasoningPoints(order)
	require.True(t, ok)
	require.Equal(t, 1000.0, points)
	_, ok = OrderPurchasedReasoningPoints(&Order{OrderType: OrderTypeBalance, Amount: math.NaN()})
	require.False(t, ok)
}
