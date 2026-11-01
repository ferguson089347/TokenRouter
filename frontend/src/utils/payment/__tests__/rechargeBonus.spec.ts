import { describe, expect, it } from 'vitest'
import { emptyRechargeBonusActivity, quoteRechargeBonus } from '../rechargeBonus'
import type { RechargeBonusSettings, RechargeBonusProgress } from '@/api/payment.types'

// rechargeBonus.spec.ts checks cumulative fixed rewards and independent timed
// extras against the reference activity's published examples. Confirmed progress
// is supplied separately from pending purchases, covering threshold differences,
// the fixed cap, converted purchased credit, rounding, and exact time boundaries.

// Fixed UTC instants identify one campaign and its independently scheduled extra period.
const start = '2026-11-11T00:00:00Z'
const end = '2026-11-12T00:00:00Z'
const settings: RechargeBonusSettings = {
  recharge_bonus_tiers: [
    { min_amount: 100, bonus_amount: 11 },
    { min_amount: 300, bonus_amount: 33 },
    { min_amount: 500, bonus_amount: 66 },
    { min_amount: 1000, bonus_amount: 121 },
    { min_amount: 3000, bonus_amount: 231 },
    { min_amount: 5000, bonus_amount: 451 },
    { min_amount: 10000, bonus_amount: 1111 },
  ],
  recharge_bonus_notice: '',
  recharge_bonus_activity: {
    ...emptyRechargeBonusActivity(),
    campaign_id: 'event',
    starts_at: start,
    ends_at: end,
    time_windows: [{ enabled: true, starts_at: start, ends_at: end, bonus_percent: 5 }],
  },
}

// progress represents successful purchases and fixed rewards already assigned to this campaign.
function progress(cumulative_amount: number, tier_reward_total: number): RechargeBonusProgress {
  return { campaign_id: 'event', cumulative_amount, tier_reward_total }
}

describe('cumulative recharge previews', () => {
  // A purchase between thresholds receives the fixed reward of its highest reached tier.
  // Increasing the payment within that tier must not turn its reward into a percentage.
  it('grants eleven at a cumulative amount of one hundred fifty', () => {
    const quote = quoteRechargeBonus(settings, 150, 1, Date.parse(start))
    expect(quote.tier_bonus_amount).toBe(11)
    expect(quote.time_bonus_amount).toBe(7.5)
    expect(quote.amount).toBe(168.5)
    expect(quote.cumulative_amount).toBe(150)
    expect(quoteRechargeBonus(settings, 99, 1, Date.parse(start)).tier_bonus_amount).toBe(0)
  })

  // Split purchases must earn the same fixed total as the reference's one-payment example.
  // Previously assigned rewards are subtracted once when the next purchase crosses a tier.
  it('adds only the difference after several successful purchases', () => {
    expect(quoteRechargeBonus(settings, 400, 1, Date.parse(start)).tier_bonus_amount).toBe(33)
    const second = quoteRechargeBonus(settings, 600, 1, Date.parse(start), progress(400, 33))
    expect(second.tier_bonus_amount).toBe(88)
    expect(second.tier_reward_total).toBe(121)
    expect(second.cumulative_before).toBe(400)
    expect(second.cumulative_amount).toBe(1000)
    expect(
      quoteRechargeBonus(settings, 150, 1, Date.parse(start), progress(150, 11)).tier_bonus_amount,
    ).toBe(22)
  })

  // The fixed reward stays capped after the final threshold, while timed extras continue.
  // A five-percent payment gift must not consume or reopen the cumulative tier cap.
  it('keeps the fixed cap independent from the scheduled percentage', () => {
    const quote = quoteRechargeBonus(settings, 1000, 1, Date.parse(start), progress(10000, 1111))
    expect(quote.tier_bonus_amount).toBe(0)
    expect(quote.tier_reward_total).toBe(1111)
    expect(quote.time_bonus_amount).toBe(50)
    expect(quote.amount).toBe(1050)
  })

  // Purchased base credit participates after the existing payment-to-credit conversion.
  // Gifted credit cannot increase the cumulative purchased amount or select another campaign's reward.
  it('uses converted base credit and ignores another campaign progress', () => {
    const quote = quoteRechargeBonus(settings, 1000, 0.14, Date.parse(start))
    expect(quote.base_amount).toBe(140)
    expect(quote.tier_bonus_amount).toBe(11)
    expect(quote.time_bonus_amount).toBe(7)
    expect(quote.amount).toBe(158)
    const other = { ...progress(10000, 1111), campaign_id: 'old-event' }
    expect(quoteRechargeBonus(settings, 150, 1, Date.parse(start), other).tier_bonus_amount).toBe(
      11,
    )
  })

  // The first instant belongs to the activity and the final instant does not.
  // An ineligible payment preserves confirmed accumulation and receives neither new reward component.
  it('respects half-open activity boundaries', () => {
    expect(quoteRechargeBonus(settings, 1000, 1, Date.parse(start) - 1).bonus_amount).toBe(0)
    const ended = quoteRechargeBonus(settings, 1000, 1, Date.parse(end), progress(300, 33))
    expect(ended.bonus_amount).toBe(0)
    expect(ended.cumulative_amount).toBe(300)
  })

  // Exact decimal half-cents can fall below their boundary after binary multiplication.
  // Base conversion and scheduled gifts retain the server's independent decimal rounding.
  it('rounds purchased credit and timed gifts using decimal operands', () => {
    const extra = structuredClone(settings)
    extra.recharge_bonus_activity.time_windows[0].bonus_percent = 10
    const quote = quoteRechargeBonus(extra, 100.75, 1, Date.parse(start))
    expect(quote.time_bonus_amount).toBe(10.08)
    expect(quote.amount).toBe(121.83)
    expect(quoteRechargeBonus({}, 40.3, 0.25).base_amount).toBe(10.08)
    expect(quoteRechargeBonus({}, 1, 1e-7).base_amount).toBe(0)
  })
})
