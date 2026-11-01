package httpapi

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/server/httpx"
)

// recharge_activity.go exposes the selected public recharge promotion and its
// checkout preview inputs. The landing page receives the same gift schedule,
// multiplier, and public method limits used by authenticated checkout, while
// merchant configuration and buyer information remain owned by their existing
// authenticated endpoints.

// GetRechargeActivity returns published activity presentation and public monetary preview inputs.
// Disabled checkout, disabled balance purchases, unpublished pages, and unknown
// slugs return not found. Configuration or method-limit read errors remain visible.
// @project-doc docs/domains/payments_and_entitlements.md#recharge_bonus
func (h *PaymentHandler) GetRechargeActivity(c *gin.Context) {
	cfg, err := h.configService.GetPaymentConfig(c.Request.Context())
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}
	if !cfg.Enabled || cfg.BalanceDisabled || !cfg.RechargeBonusActivity.IsPublishedForSlug(c.Param("slug")) {
		httpx.NotFound(c, "Recharge activity not found")
		return
	}
	limits, err := h.configService.GetAvailableMethodLimits(c.Request.Context())
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	httpx.Success(c, gin.H{
		"activity":    cfg.RechargeBonusActivity,
		"tiers":       cfg.RechargeBonusTiers,
		"notice":      cfg.RechargeBonusNotice,
		"multiplier":  cfg.BalanceRechargeMultiplier,
		"methods":     limits.Methods,
		"min_amount":  cfg.MinAmount,
		"max_amount":  cfg.MaxAmount,
		"server_time": time.Now().UTC(),
	})
}
