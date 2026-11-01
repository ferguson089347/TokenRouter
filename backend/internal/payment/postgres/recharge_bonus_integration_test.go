//go:build integration

package postgres_test

import (
	"sync"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql"
	"github.com/stretchr/testify/require"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	_ "github.com/TokenFlux/TokenRouter/ent/runtime"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/payment/postgres"
	"github.com/TokenFlux/TokenRouter/internal/testutil/postgrescontainer"
)

// recharge_bonus_integration_test.go verifies cumulative allocation and its
// payment transition against migrated PostgreSQL. Sequential payments protect
// reward differences and the top-tier cap, while concurrent pending orders race
// for the same buyer lock. A failed paid update must leave campaign progress and
// order entitlement unchanged before a verified retry succeeds.

// TestCumulativeRechargePayment serializes confirmed purchased credit across pending orders.
// Fixed rewards are allocated once per highest cumulative tier, scheduled gifts
// use payment time, and failed writes or repeated notifications cannot duplicate rewards.
func TestCumulativeRechargePayment(t *testing.T) {
	db := postgrescontainer.New(t)
	client := dbent.NewClient(dbent.Driver(sql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := t.Context()
	buyer, err := client.User.Create().SetEmail("cumulative@example.com").SetPasswordHash("test").SetUsername("cumulative").Save(ctx)
	require.NoError(t, err)
	store := postgres.NewOrderStore(client)
	instances := postgres.NewInstanceStore(client)
	at := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	end := at.Add(24 * time.Hour)
	extraStart := at.Add(time.Hour)
	extraEnd := extraStart.Add(time.Hour)
	cfg := &payment.PaymentConfig{
		RechargeBonusTiers: []payment.RechargeBonusTier{
			{MinAmount: 100, BonusAmount: 11},
			{MinAmount: 300, BonusAmount: 33},
			{MinAmount: 500, BonusAmount: 66},
			{MinAmount: 1000, BonusAmount: 121},
			{MinAmount: 3000, BonusAmount: 231},
			{MinAmount: 5000, BonusAmount: 451},
			{MinAmount: 10000, BonusAmount: 1111},
		},
		RechargeBonusActivity: payment.RechargeBonusActivity{
			CampaignID: "double11-2026", StartsAt: &at, EndsAt: &end,
			TimeWindows: []payment.RechargeBonusWindow{{Enabled: true, StartsAt: extraStart, EndsAt: extraEnd, BonusPercent: 5}},
		},
	}
	first := cumulativeCheckout(t, store, buyer.ID, 100)
	// Reject the paid row update after campaign calculation, proving that its
	// transaction cannot expose either confirmed progress or an allocated gift on failure.
	_, err = db.ExecContext(ctx, `CREATE FUNCTION reject_campaign_paid() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test campaign paid failure'; END $$;
CREATE TRIGGER reject_campaign_paid BEFORE UPDATE ON payment_orders FOR EACH ROW WHEN (NEW.status = 'PAID' AND NEW.recharge_campaign_id <> '') EXECUTE FUNCTION reject_campaign_paid()`)
	require.NoError(t, err)
	_, err = store.TransitionOrder(ctx, payment.OrderTransition{ID: first.ID, From: []string{payment.OrderStatusPending}, Status: payment.OrderStatusPaid, PaidAt: &at, RechargeConfig: cfg})
	require.Error(t, err)
	progress, err := instances.RechargeBonusProgress(ctx, buyer.ID, cfg.RechargeBonusActivity.CampaignID)
	require.NoError(t, err)
	require.Zero(t, progress.CumulativeAmount)
	failed, err := store.Order(ctx, first.ID)
	require.NoError(t, err)
	require.Equal(t, payment.OrderStatusPending, failed.Status)
	require.Equal(t, 100.0, failed.Amount)
	require.Nil(t, failed.PaidAt)
	_, err = db.ExecContext(ctx, `DROP TRIGGER reject_campaign_paid ON payment_orders; DROP FUNCTION reject_campaign_paid()`)
	require.NoError(t, err)
	confirmCumulativeCheckout(t, store, first.ID, at, cfg)
	first, err = store.Order(ctx, first.ID)
	require.NoError(t, err)
	require.Equal(t, 11.0, first.BonusAmount)
	require.Equal(t, 111.0, first.Amount)
	second := cumulativeCheckout(t, store, buyer.ID, 200)
	// Gateway money can use a different currency scale. Campaign accumulation
	// follows purchased API credit, excluding that channel charge and any fees.
	_, err = client.PaymentOrder.UpdateOneID(second.ID).SetPayAmount(1400).Save(ctx)
	require.NoError(t, err)
	confirmCumulativeCheckout(t, store, second.ID, at, cfg)
	second, err = store.Order(ctx, second.ID)
	require.NoError(t, err)
	require.Equal(t, 22.0, second.BonusAmount)
	require.Equal(t, 300.0, second.BonusSnapshot["cumulative_amount"])
	require.Equal(t, 11.0, second.BonusSnapshot["previous_tier_reward"])
	capOrder := cumulativeCheckout(t, store, buyer.ID, 9700)
	confirmCumulativeCheckout(t, store, capOrder.ID, at, cfg)
	capOrder, err = store.Order(ctx, capOrder.ID)
	require.NoError(t, err)
	require.Equal(t, 1078.0, capOrder.BonusAmount)
	progress, err = instances.RechargeBonusProgress(ctx, buyer.ID, cfg.RechargeBonusActivity.CampaignID)
	require.NoError(t, err)
	require.Equal(t, 10000.0, progress.CumulativeAmount)
	require.Equal(t, 1111.0, progress.TierRewardTotal)
	extra := cumulativeCheckout(t, store, buyer.ID, 100)
	confirmCumulativeCheckout(t, store, extra.ID, extraStart, cfg)
	extra, err = store.Order(ctx, extra.ID)
	require.NoError(t, err)
	require.Equal(t, 5.0, extra.BonusAmount)
	require.Zero(t, extra.BonusSnapshot["tier_bonus_amount"])
	require.Equal(t, 5.0, extra.BonusSnapshot["time_bonus_amount"])
	fractionalAt := extraEnd.Add(-time.Nanosecond)
	fractional := cumulativeCheckout(t, store, buyer.ID, 100)
	confirmCumulativeCheckout(t, store, fractional.ID, fractionalAt, cfg)
	fractional, err = store.Order(ctx, fractional.ID)
	require.NoError(t, err)
	require.Equal(t, 5.0, fractional.BonusAmount)
	require.Equal(t, extraEnd.Add(-time.Microsecond), *fractional.PaidAt)
	boundary := cumulativeCheckout(t, store, buyer.ID, 100)
	confirmCumulativeCheckout(t, store, boundary.ID, extraEnd, cfg)
	boundary, err = store.Order(ctx, boundary.ID)
	require.NoError(t, err)
	require.Zero(t, boundary.BonusAmount)
	outside := cumulativeCheckout(t, store, buyer.ID, 100)
	confirmCumulativeCheckout(t, store, outside.ID, end, cfg)
	outside, err = store.Order(ctx, outside.ID)
	require.NoError(t, err)
	require.Empty(t, outside.RechargeCampaignID)
	legacy := cumulativeCheckout(t, store, buyer.ID, 100)
	_, err = client.PaymentOrder.UpdateOneID(legacy.ID).SetBaseAmount(0).Save(ctx)
	require.NoError(t, err)
	confirmCumulativeCheckout(t, store, legacy.ID, at, cfg)
	legacy, err = store.Order(ctx, legacy.ID)
	require.NoError(t, err)
	require.Empty(t, legacy.RechargeCampaignID)
	require.Zero(t, legacy.BonusAmount)
	progress, err = instances.RechargeBonusProgress(ctx, buyer.ID, cfg.RechargeBonusActivity.CampaignID)
	require.NoError(t, err)
	require.Equal(t, 10300.0, progress.CumulativeAmount)
	require.Equal(t, 1111.0, progress.TierRewardTotal)

	concurrentConfig := *cfg
	concurrentConfig.RechargeBonusActivity = payment.RechargeBonusActivity{CampaignID: "concurrent-campaign"}
	left := cumulativeCheckout(t, store, buyer.ID, 50)
	right := cumulativeCheckout(t, store, buyer.ID, 50)
	// Both pending orders begin from the same visible zero progress. Release
	// their payment transitions together and inspect results after both commits return.
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for _, id := range []int64{left.ID, right.ID} {
		workers.Go(func() {
			<-start
			_, err := store.TransitionOrder(ctx, payment.OrderTransition{ID: id, From: []string{payment.OrderStatusPending}, Status: payment.OrderStatusPaid, PaidAt: &at, RechargeConfig: &concurrentConfig})
			results <- err
		})
	}
	close(start)
	workers.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	progress, err = instances.RechargeBonusProgress(ctx, buyer.ID, "concurrent-campaign")
	require.NoError(t, err)
	require.Equal(t, 100.0, progress.CumulativeAmount)
	require.Equal(t, 11.0, progress.TierRewardTotal)
	left, err = store.Order(ctx, left.ID)
	require.NoError(t, err)
	right, err = store.Order(ctx, right.ID)
	require.NoError(t, err)
	require.Equal(t, 11.0, left.BonusAmount+right.BonusAmount)
	changed, err := store.TransitionOrder(ctx, payment.OrderTransition{ID: left.ID, From: []string{payment.OrderStatusPending}, Status: payment.OrderStatusPaid, PaidAt: &at, RechargeConfig: &concurrentConfig})
	require.NoError(t, err)
	require.Zero(t, changed)
	changed, err = store.TransitionOrder(ctx, payment.OrderTransition{ID: left.ID, From: []string{payment.OrderStatusPaid}, Status: payment.OrderStatusPaid, PaidAt: &end, RechargeConfig: &concurrentConfig})
	require.NoError(t, err)
	require.Equal(t, 1, changed)
	left, err = store.Order(ctx, left.ID)
	require.NoError(t, err)
	require.Equal(t, at, *left.PaidAt)
	progress, err = instances.RechargeBonusProgress(ctx, buyer.ID, "concurrent-campaign")
	require.NoError(t, err)
	require.Equal(t, 11.0, progress.TierRewardTotal)
}

// cumulativeCheckout persists a pending purchase with an immutable API credit base.
// The fixture leaves gifts unallocated until its payment transition and uses
// ordinary checkout defaults for timeout and pending-order validation.
func cumulativeCheckout(t *testing.T, store *postgres.OrderStore, userID int64, base float64) *payment.Order {
	t.Helper()
	draft := payment.BuildCheckoutDraft(payment.CreateOrderRequest{UserID: userID, Amount: base, OrderType: payment.OrderTypeBalance, PaymentType: "alipay"},
		&payment.Buyer{Email: "cumulative@example.com"}, nil, &payment.PaymentConfig{}, base, base, payment.FeeBreakdown{PayAmount: base}, nil)
	order, err := store.CreateCheckout(t.Context(), draft)
	require.NoError(t, err)
	require.Zero(t, order.BonusAmount)
	return order
}

// confirmCumulativeCheckout records one successful payment under supplied campaign terms.
// Its expected pending status requires exactly one changed order, keeping fixture
// failures visible before later assertions inspect allocated reward quantities.
func confirmCumulativeCheckout(t *testing.T, store *postgres.OrderStore, id int64, at time.Time, cfg *payment.PaymentConfig) {
	t.Helper()
	changed, err := store.TransitionOrder(t.Context(), payment.OrderTransition{ID: id, From: []string{payment.OrderStatusPending}, Status: payment.OrderStatusPaid, PaidAt: &at, RechargeConfig: cfg})
	require.NoError(t, err)
	require.Equal(t, 1, changed)
}
