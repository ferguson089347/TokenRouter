<template>
  <div class="w-full min-w-0 space-y-2" :title="tooltip" data-testid="marketplace-passive-monitor">
    <div class="flex flex-wrap items-center justify-between gap-2 text-xs">
      <span class="text-gray-500 dark:text-dark-400">{{ t('marketplace.passiveMonitoring') }}</span>
      <span
        class="flex items-center gap-1.5 font-medium text-gray-900 dark:text-white"
        role="status"
        data-testid="passive-health-status"
      >
        <span class="h-2 w-2 rounded-full" :class="healthClass(healthStatus)" />
        {{ t(`marketplace.monitorStates.${healthStatus}`) }}
      </span>
    </div>
    <div
      class="grid h-8 min-w-0 items-center overflow-hidden"
      :style="barGridStyle"
      role="img"
      :aria-label="ariaLabel"
    >
      <span
        v-for="(bucket, index) in normalizedBuckets"
        :key="`${bucket.date || 'empty'}-${index}`"
        :class="[
          'h-6 max-w-full justify-self-center rounded-compact',
          healthClass(bucket.health?.status ?? 'unknown'),
        ]"
        :style="{ width: bucketWidth }"
        :title="bucketTooltip(bucket)"
        data-testid="passive-health-bucket"
      />
    </div>
    <div class="grid grid-cols-3 gap-3 text-xs text-gray-500 dark:text-dark-400">
      <span class="flex flex-col items-start gap-0.5">
        {{ t('marketplace.monitorSuccessRate') }}
        <strong class="font-semibold text-gray-900 dark:text-white">{{ rateLabel }}</strong>
      </span>
      <span class="flex flex-col items-start gap-0.5">
        {{ t('marketplace.monitorTTFT') }}
        <strong class="font-semibold text-gray-900 dark:text-white">{{
          latencyLabel(availability?.health?.ttft_p50_ms)
        }}</strong>
      </span>
      <span
        class="flex flex-col items-start gap-0.5"
        :title="t('marketplace.monitorCacheHint')"
        data-testid="passive-cache-rate"
      >
        {{ t('marketplace.monitorCacheRate') }}
        <strong class="font-semibold text-gray-900 dark:text-white">{{
          ratioLabel(availability?.health?.cache_rate)
        }}</strong>
      </span>
    </div>
    <p
      v-if="isStale || availability?.coverage_complete === false"
      class="text-xs text-gray-500 dark:text-dark-400"
      role="status"
    >
      {{ t(isStale ? 'marketplace.monitorDelayed' : 'marketplace.monitorHistoryFilling') }}
    </p>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type {
  MarketplaceGroupAvailability,
  MarketplaceGroupAvailabilityDay,
  MarketplaceGroupHealth,
} from '@/api/marketplace.types'

// GroupAvailabilityBar.vue displays passive channel health inside a marketplace
// group header. Status labels and history colors use the server's sample-gated
// Kedaya assessment, while success ratios, first-output latency, and cache-hit
// ratios remain separate measured values. A missing assessment stays unknown,
// including when an older response contains a numerical ratio without health.
//
// Fixed-width historical cells retain the newest configured window and pad
// absent older observations. Each cell describes its own interval in a tooltip;
// committed cutoff and coverage flags distinguish delayed aggregation from a
// channel failure. No component action starts tests or changes the observed
// data, and unavailable latency remains absent instead of implying zero delay.

// props accepts the optional server aggregate; missing data still produces the
// default window and empty cells so neighboring groups retain comparable geometry.
const props = defineProps<{
  // Server aggregate and buckets; null or omission uses an empty seven-day window.
  availability?: MarketplaceGroupAvailability | null
}>()

// Localized labels describe the displayed measurement window and distinguish
// empty history from a numerical rate in both visible and accessible text.
const { t } = useI18n()

// Window and bucket durations determine the target cell count; each duration is
// clamped to one or more before converting the day window into minutes.
const windowDays = computed(() => Math.max(props.availability?.window_days ?? 7, 1))
const bucketMinutes = computed(() => Math.max(props.availability?.bucket_minutes ?? 24 * 60, 1))
const targetBucketCount = computed(() =>
  Math.max(Math.ceil((windowDays.value * 24 * 60) / bucketMinutes.value), 1),
)

// normalizedBuckets keeps the newest target-sized suffix, padding missing older
// observations on the left without changing the supplied server bucket objects.
const normalizedBuckets = computed<MarketplaceGroupAvailabilityDay[]>(() => {
  const buckets = props.availability?.days ?? []
  const target = targetBucketCount.value
  if (buckets.length >= target) {
    return buckets.slice(buckets.length - target)
  }
  return [
    ...Array.from({ length: target - buckets.length }, () => ({
      date: '',
      success_count: 0,
      total_count: 0,
      availability_rate: null,
    })),
    ...buckets,
  ]
})

// bucketWidth limits sparse bars so separate observations stay visible, while
// dense histories fill each available column within the responsive chart.
const bucketWidth = computed(() => {
  const count = normalizedBuckets.value.length
  if (count <= 30) {
    return '8px'
  }
  if (count <= 90) {
    return '5px'
  }
  if (count <= 180) {
    return '4px'
  }
  return '100%'
})

// barGridStyle reduces gaps as histories become denser and caps sparse charts
// at their combined bar widths, keeping the chart beside its fixed summary.
const barGridStyle = computed(() => {
  const count = normalizedBuckets.value.length
  // Reduce separation for denser histories so every observation remains visible.
  const gap = count > 360 ? '0' : count > 180 ? '1px' : '2px'

  return {
    gap,
    gridTemplateColumns: `repeat(${count}, minmax(0, 1fr))`,
    // Sparse histories otherwise spread fixed-width bars across fractional tracks.
    // Cap their combined width so neighboring observations keep the declared gap.
    maxWidth:
      count <= 180 ? `calc(${count} * ${bucketWidth.value} + ${count - 1} * ${gap})` : undefined,
  }
})

// Summary labels retain absent rates and signal measurements. Health categories
// come from the server, so a low raw success ratio can coexist with a healthy
// score when configured exclusions remove client failures from health scoring.
const healthStatus = computed(() => props.availability?.health?.status ?? 'unknown')
const rateLabel = computed(() => ratioLabel(props.availability?.availability_rate))
const isStale = computed(() => {
  const through = props.availability?.data_through
  if (!through) return false
  const refresh = Math.max(props.availability?.refresh_interval_seconds ?? 300, 60)
  return Date.now() - Date.parse(through) > refresh * 2 * 1000
})

// tooltip explains observed success and the committed cutoff for this group.
// No-data responses keep the configured window label while withholding numeric
// success claims until the server supplies enough eligible request samples.
const tooltip = computed(() => {
  const availability = props.availability
  const observation =
    typeof availability?.availability_rate === 'number'
      ? t('marketplace.availabilityHint', {
          days: windowDays.value,
          rate: rateLabel.value,
          success: availability.success_count,
          total: availability.total_count,
        })
      : t('marketplace.monitorInsufficientHint', { days: windowDays.value })
  const through = availability?.data_through
  return through
    ? `${observation} · ${t('marketplace.monitorUpdated', { time: new Date(through).toLocaleString() })}`
    : observation
})

// ariaLabel names the window and sampled health independently of chart color.
const ariaLabel = computed(
  () =>
    `${t('marketplace.availabilityWindow', { days: windowDays.value })}: ${t(`marketplace.monitorStates.${healthStatus.value}`)}, ${rateLabel.value}`,
)

// ratioLabel formats an observed success fraction and retains absent values.
// The server gates ratios using its configured minimum request count; missing
// or nonfinite values receive the localized unknown label instead of a number.
function ratioLabel(value?: number | null): string {
  return typeof value === 'number' && Number.isFinite(value)
    ? `${(value * 100).toFixed(2)}%`
    : t('marketplace.monitorStates.unknown')
}

// latencyLabel formats observed milliseconds as seconds with two fractional digits.
// Missing signal observations remain a dash because they do not demonstrate
// immediate output or meet the server's minimum latency sample requirement.
function latencyLabel(value?: number | null): string {
  return typeof value === 'number' && Number.isFinite(value)
    ? `${(value / 1000).toFixed(2)} s`
    : '—'
}

// healthClass maps server-assessed health to the shared semantic status colors.
// Missing bucket health uses the neutral color; success-ratio thresholds do not
// override the server's configured error exclusions, latency budgets, or weights.
function healthClass(status: MarketplaceGroupHealth['status']): string {
  return {
    healthy: 'bg-emerald-500',
    warning: 'bg-amber-400',
    critical: 'bg-rose-400',
    unknown: 'bg-gray-200 dark:bg-dark-700',
  }[status]
}

// bucketTooltip describes the selected history interval and its measured signals.
// Padded cells have no timestamp or observations, so their tooltip reports only
// unknown health instead of assigning a fabricated request time or latency.
function bucketTooltip(bucket: MarketplaceGroupAvailabilityDay): string {
  const state = t(`marketplace.monitorStates.${bucket.health?.status ?? 'unknown'}`)
  if (!bucket.date) return state
  return `${new Date(bucket.date).toLocaleString()} · ${state} · ${t('marketplace.monitorSuccessRate')} ${ratioLabel(bucket.availability_rate)} · ${t('marketplace.monitorTTFT')} ${latencyLabel(bucket.health?.ttft_p50_ms)} · ${t('marketplace.monitorCacheRate')} ${ratioLabel(bucket.health?.cache_rate)}`
}
</script>
