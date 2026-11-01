package payment

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fulfillment_test.go verifies the distinction between failed payment
// initiation and failed entitlement delivery. The payment observation time
// determines whether a late success needs first-payment campaign allocation or
// can resume an already confirmed order's saved entitlement.

// latePaymentStore captures first-payment transitions while exposing completed recovery reads.
type latePaymentStore struct {
	// FulfillmentStore supplies unused declarations. This focused fixture handles
	// transition capture and completed-order reads before entitlement operations are needed.
	FulfillmentStore
	transitions []OrderTransition // accepted transition observations in order
}

// TransitionOrder records the proposed paid transition and reports one changed row.
func (s *latePaymentStore) TransitionOrder(_ context.Context, change OrderTransition) (int, error) {
	s.transitions = append(s.transitions, change)
	return 1, nil
}

// Order reports completed entitlement so the test isolates payment transition selection.
func (s *latePaymentStore) Order(_ context.Context, id int64) (*Order, error) {
	return &Order{ID: id, OrderType: OrderTypeBalance, Status: OrderStatusCompleted}, nil
}

// TestToPaidFailedInitiation confirms genuine late payment after checkout initiation failed.
// A missing PaidAt requires the atomic campaign transition, while a previously
// paid failure resumes saved fulfillment without another allocation or configuration read.
func TestToPaidFailedInitiation(t *testing.T) {
	at := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	for _, paidAt := range []*time.Time{nil, &at} {
		store := &latePaymentStore{}
		reads := 0
		cfg := &PaymentConfig{RechargeBonusActivity: RechargeBonusActivity{CampaignID: "late-payment"}}
		service := NewFulfillment(store, nil, nil, nil, nil, FulfillmentRuntime{
			Now: func() time.Time { return at },
			RechargeConfig: func(context.Context) (*PaymentConfig, error) {
				reads++
				return cfg, nil
			},
		})
		order := &Order{ID: 1, OrderType: OrderTypeBalance, Status: OrderStatusFailed, PaidAt: paidAt}
		require.NoError(t, service.ToPaid(t.Context(), order, "late-success", 100, TypeEasyPay, nil))
		if paidAt == nil {
			require.Equal(t, 1, reads)
			require.Len(t, store.transitions, 1)
			require.Same(t, cfg, store.transitions[0].RechargeConfig)
			require.Equal(t, OrderStatusPaid, store.transitions[0].Status)
			require.Equal(t, at, *store.transitions[0].PaidAt)
		} else {
			require.Zero(t, reads)
			require.Empty(t, store.transitions)
		}
	}
}
