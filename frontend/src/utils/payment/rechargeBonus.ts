import type {
  RechargeBonusActivity,
  RechargeBonusQuote,
  RechargeBonusSettings,
  RechargeBonusTier,
  RechargeBonusProgress,
} from '@/api/payment.types'

// rechargeBonus.ts previews cumulative fixed tier rewards and scheduled extras
// for checkout, administrator settings, and public activity pages. Converted
// purchased credit advances the campaign's confirmed total, and its highest
// tier supplies a fixed total reward. The unpaid difference becomes this
// payment's tier gift. Percentage calculation and original threshold matching
// were adapted from Wei-Shaw/sub2api revision
// d851a38477597157e78450d30af2d8504764d085 under LGPL-3.0.
//
// Activity endpoints gate both gift components; an extra interval adds its
// percentage independently of the matched tier. The caller supplies the server
// clock observation and confirmed campaign progress where available. The server
// determines final rewards atomically when payment succeeds, so pending orders
// cannot reserve a tier twice. This helper provides an estimate for the same
// rules while another purchase or an activity boundary can change settlement.

// emptyRechargeBonusActivity creates independent editable collections for one settings draft.
// The supplied timezone controls presentation; absolute ISO timestamps determine
// monetary eligibility. An empty period permits ordinary tiers at any time.
export function emptyRechargeBonusActivity(timezone = 'UTC'): RechargeBonusActivity {
  return {
    campaign_id: '',
    page_enabled: false,
    slug: '',
    title: '',
    subtitle: '',
    timezone,
    time_windows: [],
    rules: '',
    faqs: [],
  }
}

// roundRechargeAmount rounds finite nonnegative credit to decimal cents for display.
// Nonfinite results remain visible to the caller's amount validation. Decimal
// coefficients avoid changing half-cent ties through a binary scaling operation.
export function roundRechargeAmount(value: number): number {
  return roundRechargeProduct(value, 1)
}

// roundRechargeProduct multiplies decimal number observations before rounding to cents.
// Callers provide nonnegative values, with divisorDigits equal to two for a
// percentage. Nonfinite operands propagate so an invalid preview stays rejectable.
//
// JavaScript binary multiplication can turn 100.75 times 10 percent into a value
// below 10.075. Converting each operand's decimal representation to an integer
// coefficient first preserves the same decimal inputs used by Go. The combined
// decimal exponent places the product at cents, and an exact integer remainder
// implements the server's half-up rounding without an arbitrary epsilon.
function roundRechargeProduct(left: number, right: number, divisorDigits = 0): number {
  if (!Number.isFinite(left) || !Number.isFinite(right)) return (left * right) / 10 ** divisorDigits
  const factors = [left, right].map((value) => {
    const [mantissa, exponent = '0'] = value.toString().split('e')
    const [integer, fraction = ''] = mantissa!.split('.')
    return {
      coefficient: BigInt(`${integer}${fraction}`),
      exponent: Number(exponent) - fraction.length,
    }
  })
  const coefficient = factors[0]!.coefficient * factors[1]!.coefficient
  // Two additional decimal places express the product in integer credited cents.
  const exponent = factors[0]!.exponent + factors[1]!.exponent + 2 - divisorDigits
  let cents: bigint
  if (exponent >= 0) {
    cents = coefficient * 10n ** BigInt(exponent)
  } else {
    const divisor = 10n ** BigInt(-exponent)
    const absolute = coefficient < 0n ? -coefficient : coefficient
    const roundedCents = (absolute + divisor / 2n) / divisor
    cents = coefficient < 0n ? -roundedCents : roundedCents
  }
  // Parse the scaled decimal once; converting the cents to Number first can
  // overflow even when the final credited quantity remains a finite number.
  return Number(`${cents}e-2`)
}

// matchRechargeBonusTier selects the highest qualifying cumulative purchased-credit threshold.
// Input rows may arrive in editor order. The returned row is borrowed and callers
// keep its values stable while displaying a quote derived from that row.
export function matchRechargeBonusTier(
  tiers: RechargeBonusTier[],
  principal: number,
): RechargeBonusTier | null {
  if (!Number.isFinite(principal) || principal <= 0) return null
  let selected: RechargeBonusTier | null = null
  for (const tier of tiers) {
    // Preserve the upstream tolerance for sums such as 0.1 plus 0.2 qualifying
    // for a 0.3 threshold after ordinary JavaScript floating-point arithmetic.
    if (
      principal + 1e-9 >= tier.min_amount &&
      (!selected || tier.min_amount > selected.min_amount)
    ) {
      selected = tier
    }
  }
  return selected
}

// quoteRechargeBonus previews the two independent gifts using one activity clock observation.
// Missing configuration permits ordinary credit conversion, and malformed dates
// disable gift eligibility. Callers use server time for live activity decisions.
//
// Thresholds use confirmed purchased credit plus this payment's multiplied base.
// The fixed reward subtracts previously assigned tier gifts, keeping rewards
// capped at the highest configured total. The scheduled percentage uses this
// payment's base independently. Activity starts are inclusive and ends exclusive;
// progress from another campaign is ignored, and the payment server settles
// concurrent successful purchases using its authoritative stored totals.
export function quoteRechargeBonus(
  settings: Partial<RechargeBonusSettings>,
  principal: number,
  multiplier = 1,
  now = Date.now(),
  progress?: RechargeBonusProgress,
): RechargeBonusQuote {
  const amount = Number.isFinite(principal) && principal > 0 ? principal : 0
  const rate = Number.isFinite(multiplier) && multiplier > 0 ? multiplier : 1
  const base = roundRechargeProduct(amount, rate)
  const activity = settings.recharge_bonus_activity
  const matchingProgress = progress?.campaign_id === activity?.campaign_id ? progress : undefined
  const previous = matchingProgress?.cumulative_amount ?? 0
  const awarded = matchingProgress?.tier_reward_total ?? 0
  const quote: RechargeBonusQuote = {
    base_amount: base,
    tier_bonus_amount: 0,
    time_bonus_amount: 0,
    bonus_amount: 0,
    amount: base,
    tier_reward_total: awarded,
    previous_tier_reward: awarded,
    cumulative_before: previous,
    cumulative_amount: previous,
    time_percent: 0,
    min_amount: 0,
  }
  if (base <= 0 || !Number.isFinite(base) || !activity?.campaign_id) return quote
  if (
    (activity?.starts_at && !(now >= Date.parse(activity.starts_at))) ||
    (activity?.ends_at && !(now < Date.parse(activity.ends_at)))
  )
    return quote
  quote.cumulative_amount = roundRechargeAmount(previous + base)
  const tier = matchRechargeBonusTier(settings.recharge_bonus_tiers ?? [], quote.cumulative_amount)
  quote.tier_reward_total = Math.max(awarded, tier?.bonus_amount ?? 0)
  quote.min_amount = tier?.min_amount ?? 0
  const window = activity?.time_windows?.find(
    (item) => item.enabled && now >= Date.parse(item.starts_at) && now < Date.parse(item.ends_at),
  )
  quote.time_percent = window?.bonus_percent ?? 0
  quote.tier_bonus_amount = roundRechargeAmount(Math.max(0, quote.tier_reward_total - awarded))
  quote.time_bonus_amount = roundRechargeProduct(base, quote.time_percent, 2)
  quote.bonus_amount = roundRechargeAmount(quote.tier_bonus_amount + quote.time_bonus_amount)
  quote.amount = roundRechargeAmount(base + quote.bonus_amount)
  return quote
}
