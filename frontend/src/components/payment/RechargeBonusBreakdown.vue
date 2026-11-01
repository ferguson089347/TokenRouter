<template>
  <dl class="space-y-3 text-sm" data-testid="recharge-bonus-breakdown">
    <template v-if="estimated && quote.cumulative_before !== undefined">
      <div class="flex justify-between gap-4">
        <dt class="text-gray-500 dark:text-dark-300">{{ t('rechargeBonus.cumulativeAmount') }}</dt>
        <dd class="font-medium tabular-nums" data-testid="recharge-bonus-cumulative-before">{{
          format(quote.cumulative_before)
        }}</dd>
      </div>
      <div class="flex justify-between gap-4">
        <dt class="text-gray-500 dark:text-dark-300">{{ t('rechargeBonus.tierBonusAwarded') }}</dt>
        <dd class="font-medium tabular-nums" data-testid="recharge-bonus-previous-tier-reward">{{
          format(quote.previous_tier_reward ?? 0)
        }}</dd>
      </div>
      <div class="flex justify-between gap-4 border-b border-gray-200 pb-3 dark:border-dark-600">
        <dt class="text-gray-500 dark:text-dark-300">{{ t('rechargeBonus.cumulativeAfter') }}</dt>
        <dd class="font-medium tabular-nums" data-testid="recharge-bonus-cumulative-after">{{
          format(quote.cumulative_amount ?? quote.cumulative_before)
        }}</dd>
      </div>
    </template>
    <div class="flex justify-between gap-4">
      <dt class="text-gray-500 dark:text-dark-300">{{ t('rechargeBonus.base') }}</dt>
      <dd class="font-medium tabular-nums">{{ format(baseAmount) }}</dd>
    </div>
    <div v-if="(quote.tier_bonus_amount ?? 0) > 0" class="flex justify-between gap-4">
      <dt class="text-gray-500 dark:text-dark-300">{{ t('rechargeBonus.tierGift') }}</dt>
      <dd class="font-medium tabular-nums" data-testid="recharge-bonus-tier-gift"
        >+{{ format(quote.tier_bonus_amount ?? 0) }}</dd
      >
    </div>
    <div v-if="(quote.time_bonus_amount ?? 0) > 0" class="flex justify-between gap-4">
      <dt class="text-gray-500 dark:text-dark-300"
        >{{ t('rechargeBonus.timeGift') }}
        <span v-if="quote.time_percent">({{ quote.time_percent }}%)</span></dt
      >
      <dd class="font-medium tabular-nums" data-testid="recharge-bonus-time-gift"
        >+{{ format(quote.time_bonus_amount ?? 0) }}</dd
      >
    </div>
    <div
      class="flex items-baseline justify-between gap-4 border-t border-gray-200 pt-3 dark:border-dark-600"
    >
      <dt class="font-medium">{{
        t(estimated ? 'rechargeBonus.estimatedTotal' : 'rechargeBonus.total')
      }}</dt>
      <dd class="text-2xl font-semibold tabular-nums" data-testid="recharge-bonus-total">{{
        format(totalAmount)
      }}</dd>
    </div>
  </dl>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import type { RechargeBonusQuote } from '@/api/payment.types'

// RechargeBonusBreakdown.vue displays purchased credit and campaign gift amounts
// on purchase, activity, and order screens. Fixed tier rewards represent the
// additional credit earned after subtracting rewards already delivered for the
// campaign. Scheduled percentages apply separately to this purchase's base
// credit, so that extra remains visible after the cumulative tier cap is reached.
//
// Estimated previews also show successful campaign credit before the purchase,
// already delivered tier rewards, and the projected cumulative credit afterward.
// Persisted orders may supply partial snapshots, so their displayed base and
// total use surrounding order quantities when component amounts are absent.
// The caller selects estimated wording for calculators; saved orders retain
// their supplied total and final wording.

// The caller supplies immutable preview or persisted campaign gift observations
// for consistent rendering.
const props = defineProps<{
  quote: Partial<RechargeBonusQuote> // Preview or saved order gift quantities.
  // estimated selects provisional copy and cumulative progress rows; omitted
  // values display the persisted order's credited amount as its final total.
  estimated?: boolean
}>()

// Presentation follows the current interface language and the site's configured
// balance unit.
const { t } = useI18n()
const { formatBalanceAmount } = useBalanceDisplay()

// Missing component snapshots can occur on older orders. Derive their purchased
// base from the order total and aggregate gift while keeping the saved total authoritative.
const baseAmount = computed(
  () =>
    props.quote.base_amount ??
    Math.max(0, (props.quote.amount ?? 0) - (props.quote.bonus_amount ?? 0)),
)
const totalAmount = computed(
  () => props.quote.amount ?? baseAmount.value + (props.quote.bonus_amount ?? 0),
)

// format renders credit quantities in the configured account balance unit.
function format(value: number): string {
  return formatBalanceAmount(value, { fractionDigits: 2 })
}
</script>
