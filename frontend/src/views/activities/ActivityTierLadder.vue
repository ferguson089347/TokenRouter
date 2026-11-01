<template>
  <div role="group" :aria-label="t('rechargeBonus.tierLadder')" data-testid="activity-tier-ladder">
    <div
      class="flex gap-2 overflow-x-auto pb-2 sm:grid sm:gap-2 sm:overflow-visible sm:pb-0"
      :style="desktopColumns"
    >
      <button
        v-for="(tier, index) in tiers"
        :key="tier.min_amount"
        type="button"
        class="relative flex min-w-[104px] shrink-0 snap-start flex-col gap-1 rounded-control border px-3 py-3 text-left transition sm:min-w-0 sm:shrink sm:border-0 sm:px-2 sm:pb-0 sm:text-center"
        :class="nodeClass(tier.min_amount)"
        :disabled="disabled || isReached(tier.min_amount)"
        :aria-pressed="isSelected(tier.min_amount)"
        data-testid="activity-tier-node"
        @click="emit('select', tier.min_amount)"
      >
        <!-- Each node draws the connector to its left neighbor, so the track
             follows the grid without absolute offsets. -->
        <span
          v-if="index > 0"
          aria-hidden="true"
          class="absolute top-4 hidden h-0.5 sm:block"
          :class="isReached(tier.min_amount) ? 'bg-primary-600' : 'bg-gray-200 dark:bg-dark-700'"
          :style="{ right: '50%', width: '100%' }"
        />
        <span
          aria-hidden="true"
          class="relative mx-auto hidden h-3 w-3 rounded-full border-2 bg-white sm:block dark:bg-dark-950"
          :class="dotClass(tier.min_amount)"
        />
        <span class="text-xs text-gray-500 dark:text-dark-300">{{
          formatBalanceAmount(tier.min_amount, { fractionDigits: 2 })
        }}</span>
        <span class="text-sm font-semibold tabular-nums" :class="giftClass(tier.min_amount)"
          >+{{ formatBalanceAmount(tier.bonus_amount, { fractionDigits: 2 }) }}</span
        >
        <span v-if="isReached(tier.min_amount)" class="text-xs text-gray-500 dark:text-dark-300">{{
          t('rechargeBonus.reached')
        }}</span>
        <span
          v-else-if="isNext(tier.min_amount)"
          class="text-xs font-medium text-primary-600 dark:text-primary-500"
          >{{ t('rechargeBonus.nextStep') }}</span
        >
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { RechargeBonusTier } from '@/api/payment.types'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'

// ActivityTierLadder.vue renders the activity's fixed reward thresholds as one
// track, replacing the separate chip list and reward cards that repeated the same
// numbers. Reached thresholds fill the connector and use the brand color, the next
// threshold carries a ring and a hint, and the remaining thresholds stay neutral.
// Clicking a threshold asks the page to raise the purchase amount to that
// threshold; a threshold already covered by earlier payments is disabled because
// it would add no reward. Threshold states derive from the buyer's confirmed
// cumulative credit, so guests and buyers with failed progress reads compare
// amounts in the same layout while their quotes stay gated by the page.
//
// Mobile presents the thresholds as horizontally scrolling cards with no
// connector, because a seven-column track leaves too little room for two lines of
// text per threshold on a phone.

// The page owns tier order, cumulative progress, and the selected threshold; this
// component only derives display states and reports the chosen threshold.
const props = defineProps<{
  tiers: RechargeBonusTier[] // Thresholds sorted by ascending cumulative credit.
  cumulativeBefore: number // Confirmed credit accumulated before this purchase.
  nextMinimum: number // Threshold of the next reward, or zero when all are reached.
  selectedMinimum: number // Threshold matched by the current preview, or zero.
  disabled: boolean // Blocks selection while personal progress is unavailable.
}>()

const emit = defineEmits<{
  select: [minimum: number] // Cumulative threshold the page should fill the amount to.
}>()

const { t } = useI18n()
const { formatBalanceAmount } = useBalanceDisplay()

// desktopColumns sizes the track to the number of thresholds, so a campaign with
// fewer tiers does not leave empty columns. Mobile ignores it and scrolls instead.
const desktopColumns = computed(() => ({
  gridTemplateColumns: `repeat(${Math.max(1, props.tiers.length)}, minmax(0, 1fr))`,
}))

/** isReached reports whether earlier purchases already covered this threshold. */
function isReached(minimum: number): boolean {
  return minimum <= props.cumulativeBefore
}

/** isNext reports whether this threshold is the first one still ahead of the buyer. */
function isNext(minimum: number): boolean {
  return props.nextMinimum > 0 && minimum === props.nextMinimum
}

/** isSelected reports whether the preview currently matches this threshold. */
function isSelected(minimum: number): boolean {
  return props.selectedMinimum > 0 && minimum === props.selectedMinimum
}

/** nodeClass selects the card border on mobile, where each threshold is a card. */
function nodeClass(minimum: number): string {
  if (isReached(minimum)) return 'border-primary-600 bg-primary-50 dark:bg-primary-500/8'
  if (isNext(minimum)) return 'border-primary-600'
  return 'border-gray-200 dark:border-dark-600'
}

/** dotClass colors the desktop dot for reached, next, and remaining thresholds. */
function dotClass(minimum: number): string {
  if (isReached(minimum)) return 'border-primary-600 bg-primary-600'
  if (isNext(minimum)) return 'border-primary-600 ring-4 ring-primary-500/20'
  return 'border-gray-300 dark:border-dark-600'
}

/** giftClass emphasizes rewards the buyer earns or can still reach. */
function giftClass(minimum: number): string {
  return isReached(minimum) || isNext(minimum)
    ? 'text-primary-600 dark:text-primary-500'
    : 'text-gray-950 dark:text-white'
}
</script>
