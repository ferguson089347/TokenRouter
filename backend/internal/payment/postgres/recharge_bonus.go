package postgres

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"time"

	"entgo.io/ent/dialect"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/paymentorder"
	"github.com/TokenFlux/TokenRouter/ent/user"
	"github.com/TokenFlux/TokenRouter/internal/payment"
)

// recharge_bonus.go serializes cumulative campaign allocation with first payment
// confirmation. A buyer can have several pending orders, so checkout estimates
// cannot reserve a fixed reward. The paid transition locks the buyer before the
// order, reads that campaign's confirmed purchased credit and fixed allocations,
// and saves the newly eligible difference alongside payment state and final
// entitlement. Time-window gifts use the same payment observation independently.
//
// Campaign participation is written once when an order first becomes paid.
// Fulfillment failures keep that allocation reserved, and retries use the saved
// total through the existing order-owned redeem code. A transaction failure
// rolls back both payment confirmation and its campaign allocation, permitting a
// later verified notification to retry the entire decision.

// RechargeBonusProgress returns confirmed purchases and allocated rewards for one buyer's campaign preview.
func (s *InstanceStore) RechargeBonusProgress(ctx context.Context, userID int64, campaignID string) (payment.RechargeBonusProgress, error) {
	return readRechargeBonusProgress(ctx, s.client, userID, campaignID)
}

// readRechargeBonusProgress sums purchased credit and fixed allocations for one campaign.
// Confirmed orders retain their allocations during fulfillment recovery. Callers
// allocating another reward hold the buyer row lock until their paid update commits;
// display readers receive a transactionally consistent estimate from this query.
//
// Every selected row supplies a positive purchased base and a finite tier
// allocation bounded by its total gift. Invalid observations abort the read
// before partial progress can be returned. Their decimal representations are
// added exactly before conversion to the payment API's float values, keeping
// binary conversion residue from accumulating across confirmed payments.
func readRechargeBonusProgress(ctx context.Context, client *dbent.Client, userID int64, campaignID string) (payment.RechargeBonusProgress, error) {
	result := payment.RechargeBonusProgress{CampaignID: campaignID}
	rows, err := client.PaymentOrder.Query().Where(
		paymentorder.UserIDEQ(userID), paymentorder.RechargeCampaignIDEQ(campaignID),
		paymentorder.PaidAtNotNil(),
	).Select(paymentorder.FieldBaseAmount, paymentorder.FieldBonusAmount, paymentorder.FieldBonusSnapshot).All(ctx)
	if err != nil {
		return result, err
	}
	base := new(big.Rat)
	rewards := new(big.Rat)
	for _, row := range rows {
		reward, ok := row.BonusSnapshot["tier_bonus_amount"].(float64)
		if !ok || !payment.IsValidProviderAmount(row.BaseAmount) ||
			math.IsNaN(reward) || math.IsInf(reward, 0) ||
			reward < 0 || reward > row.BonusAmount {
			return result, fmt.Errorf("invalid persisted recharge allocation")
		}
		// Stored amounts have cent precision. Parse each shortest decimal spelling
		// so binary float representation cannot introduce rounding drift while payments accumulate.
		baseValue, baseValid := new(big.Rat).SetString(strconv.FormatFloat(row.BaseAmount, 'f', -1, 64))
		rewardValue, rewardValid := new(big.Rat).SetString(strconv.FormatFloat(reward, 'f', -1, 64))
		if !baseValid || !rewardValid {
			return result, fmt.Errorf("invalid persisted recharge decimal amount")
		}
		base.Add(base, baseValue)
		rewards.Add(rewards, rewardValue)
	}
	// Convert exact cent totals after summation, keeping floating-point rounding
	// confined to the API representation used by the existing payment domain calculations.
	result.CumulativeAmount, _ = base.Float64()
	result.TierRewardTotal, _ = rewards.Float64()
	return result, nil
}

// confirmRechargePayment commits cumulative rewards and the accepted paid transition together.
// The initial identity read determines the immutable buyer lock; the transaction
// then locks that buyer before rereading the order in an accepted prior status.
// PostgreSQL row locks serialize pending orders belonging to the same buyer.
//
// Only orders with a purchased base recorded at checkout can join the campaign.
// Older pending orders retain their historical entitlement. Campaign progress is
// read after acquiring the buyer lock, so simultaneous successful payments cannot
// each grant the same fixed reward. The final snapshot survives downstream grant
// failures, and any error before commit leaves both confirmation and allocation
// available for a later verified retry.
func (s *OrderStore) confirmRechargePayment(ctx context.Context, change payment.OrderTransition) (int, error) {
	observed, err := s.client.PaymentOrder.Get(ctx, change.ID)
	if err != nil {
		return 0, err
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return 0, err
	}
	// Keep payment state, campaign identity, and reward allocation under this
	// transaction; every early return rolls them back before the callback may retry.
	defer func() { _ = tx.Rollback() }()
	buyerQuery := tx.User.Query().Where(user.IDEQ(observed.UserID))
	orderQuery := tx.PaymentOrder.Query().Where(paymentorder.IDEQ(change.ID), paymentorder.StatusIn(change.From...))
	if PaymentAuditDialect(s.client) == dialect.Postgres {
		// All cumulative allocations take the buyer lock first, preventing two
		// pending orders from reading the same previously allocated campaign reward.
		buyerQuery.ForUpdate()
		orderQuery.ForUpdate()
	}
	if _, err := buyerQuery.Only(ctx); err != nil {
		return 0, err
	}
	current, err := orderQuery.Only(ctx)
	if dbent.IsNotFound(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	// A repeated transition can arrive after allocation. Keep the first payment
	// observation, whose time selected the already saved extra gift and campaign eligibility.
	if current.PaidAt != nil {
		change.PaidAt = current.PaidAt
	} else if change.PaidAt != nil {
		// PostgreSQL stores microsecond timestamps. Select gifts with that same
		// truncated instant so persisted PaidAt and the selected schedule agree at interval endpoints.
		paidAt := change.PaidAt.UTC().Truncate(time.Microsecond)
		change.PaidAt = &paidAt
	}
	update := applyOrderTransition(tx.PaymentOrder.Update(), change)
	if current.PaidAt == nil && current.OrderType == payment.OrderTypeBalance && current.BaseAmount > 0 && change.PaidAt != nil {
		campaign := change.RechargeConfig.RechargeBonusActivity
		quote := payment.QuoteRechargeBonusForBase(change.RechargeConfig, current.BaseAmount, *change.PaidAt, payment.RechargeBonusProgress{})
		if quote.CampaignID != "" && quote.CumulativeAmount > 0 {
			progress, err := readRechargeBonusProgress(ctx, tx.Client(), current.UserID, campaign.CampaignID)
			if err != nil {
				return 0, err
			}
			quote = payment.QuoteRechargeBonusForBase(change.RechargeConfig, current.BaseAmount, *change.PaidAt, progress)
			if !payment.IsValidProviderAmount(quote.Amount) || math.IsInf(quote.BonusAmount, 0) || math.IsNaN(quote.BonusAmount) {
				return 0, fmt.Errorf("confirmed recharge credit exceeds its supported amount range")
			}
			update.SetAmount(quote.Amount).
				SetBonusAmount(quote.BonusAmount).
				SetBonusSnapshot(quote.Snapshot()).
				SetRechargeCampaignID(campaign.CampaignID)
		}
	}
	changed, err := update.Save(ctx)
	if err != nil {
		return 0, err
	}
	// Publish final entitlement only with its paid observation. Downstream
	// redemption and fulfillment retries then consume these already allocated order terms.
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return changed, nil
}
