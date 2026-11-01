//go:build integration

package postgres_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql"
	"github.com/stretchr/testify/require"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	_ "github.com/TokenFlux/TokenRouter/ent/runtime"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/payment/postgres"
	"github.com/TokenFlux/TokenRouter/internal/payment/testkit"
	"github.com/TokenFlux/TokenRouter/internal/testutil/postgrescontainer"
)

// checkout_integration_test.go verifies gifted recharge snapshots and retryable
// money issuance against migrated PostgreSQL. The checkout store persists both
// paid and free credit quantities, while the production redemption transaction
// owns balance and usage writes. A database trigger rejects the first usage
// write after balance mutation, proving rollback before a later successful retry.
//
// Concurrent recovery calls then compete for the same fulfillment lease. Their
// returned errors may differ because one caller can observe another's active
// claim, but committed balance, redemption usage, and completed order state must
// describe a single total-credit grant. The fixture reads those facts through
// the base connection after both recovery calls have returned.

// rechargeIntegrationUsers reads billing recipient summaries from the migrated database.
type rechargeIntegrationUsers struct {
	client *dbent.Client // fixture-owned Ent database client
}

// GetByID returns the persisted recipient identity and current wallet balance.
func (r rechargeIntegrationUsers) GetByID(ctx context.Context, id int64) (*billing.UserSummary, error) {
	user, err := r.client.User.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return &billing.UserSummary{ID: user.ID, Balance: user.Balance}, nil
}

// TestGiftCheckoutRollbackAndRecovery rejects issuance after its balance update.
// PostgreSQL must roll back that grant, then concurrent retries must commit the
// original gifted order once with its monetary snapshot unchanged.
func TestGiftCheckoutRollbackAndRecovery(t *testing.T) {
	db := postgrescontainer.New(t)
	client := dbent.NewClient(dbent.Driver(sql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := t.Context()
	user, err := client.User.Create().SetEmail("gift@example.com").SetPasswordHash("test").SetUsername("gift").Save(ctx)
	require.NoError(t, err)
	cfg := &payment.PaymentConfig{BalanceRechargeMultiplier: 1, RechargeBonusTiers: []payment.RechargeBonusTier{{MinAmount: 1000, BonusAmount: 121}}, RechargeBonusActivity: payment.RechargeBonusActivity{CampaignID: "gift-tests"}}
	quote := payment.QuoteRechargeBonus(cfg, 1000, time.Now())
	draft := payment.BuildCheckoutDraft(payment.CreateOrderRequest{UserID: user.ID, Amount: 1000, OrderType: payment.OrderTypeBalance, PaymentType: "alipay"}, &payment.Buyer{Email: user.Email}, nil, cfg, quote.BaseAmount, 1000, payment.FeeBreakdown{PayAmount: 1000}, nil)
	store := postgres.NewOrderStore(client)
	order, err := store.CreateCheckout(ctx, draft)
	require.NoError(t, err)
	require.Zero(t, order.BonusAmount)
	paidAt := time.Now()
	_, err = store.TransitionOrder(ctx, payment.OrderTransition{ID: order.ID, From: []string{payment.OrderStatusPending}, Status: payment.OrderStatusPaid, PaidAt: &paidAt, RechargeConfig: cfg})
	require.NoError(t, err)
	order, err = store.Order(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, 121.0, order.BonusAmount)
	require.Equal(t, 121.0, order.BonusSnapshot["tier_reward_total"])
	redeem := billing.NewRedeemService(billingpostgres.NewRedeemCodeRepository(client), rechargeIntegrationUsers{client: client}, nil, nil, nil,
		billingpostgres.NewRedeemMutations(client, billingpostgres.RedeemWriters{Balances: billingpostgres.NewBalanceStore(client)}), nil, nil, billing.RedeemRuntime{})
	fulfillment := testkit.Fulfillment(client, redeem, nil, nil)
	// Reject the usage write after entitlement mutation within the production
	// redemption transaction; the persisted balance must roll back with that failure.
	_, err = db.ExecContext(ctx, `CREATE FUNCTION reject_gift_usage() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test gift usage failure'; END $$;
CREATE TRIGGER reject_gift_usage BEFORE INSERT ON redeem_code_usages FOR EACH ROW EXECUTE FUNCTION reject_gift_usage()`)
	require.NoError(t, err)
	require.Error(t, fulfillment.ExecuteBalanceFulfillment(ctx, order.ID))
	storedUser, err := client.User.Get(ctx, user.ID)
	require.NoError(t, err)
	require.Zero(t, storedUser.Balance)
	count, err := client.RedeemCodeUsage.Query().Count(ctx)
	require.NoError(t, err)
	require.Zero(t, count)
	_, err = db.ExecContext(ctx, `DROP TRIGGER reject_gift_usage ON redeem_code_usages; DROP FUNCTION reject_gift_usage()`)
	require.NoError(t, err)
	// Recovery callers race for the same saved lease; wait for both before
	// inspecting committed money so an in-flight grant cannot hide a duplicate.
	var workers sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		workers.Go(func() { results <- fulfillment.ExecuteBalanceFulfillment(ctx, order.ID) })
	}
	workers.Wait()
	close(results)
	succeeded := false
	for result := range results {
		succeeded = succeeded || result == nil
	}
	require.True(t, succeeded)
	storedUser, err = client.User.Get(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, 1121.0, storedUser.Balance)
	count, err = client.RedeemCodeUsage.Query().Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	stored, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, payment.OrderStatusCompleted, stored.Status)
	require.Equal(t, 121.0, stored.BonusAmount)
	_, err = client.PaymentOrder.UpdateOneID(order.ID).SetBonusAmount(1200).Save(ctx)
	require.Error(t, err)
}
