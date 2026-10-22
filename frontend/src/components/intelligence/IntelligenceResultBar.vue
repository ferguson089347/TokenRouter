<template>
  <div>
    <IntelligenceStatusLegend v-if="!compact" class="mb-2" />
    <div
      ref="strip"
      data-testid="intelligence-history-strip"
      class="flex min-w-0 items-center"
      :class="compact ? 'h-7 gap-0.5' : 'h-10 gap-1'"
      :aria-label="t('intelligence.recentResults', { count: 60 })"
      @mouseleave="closePreview"
    >
      <span
        v-for="slot in Math.max(0, 60 - ordered.length)"
        :key="`empty-${slot}`"
        class="min-w-0 flex-1 rounded-compact bg-gray-100 dark:bg-dark-700"
        :class="compact ? 'h-5' : 'h-7'"
        aria-hidden="true"
      />
      <span
        v-for="run in ordered"
        :key="run.id"
        role="img"
        tabindex="0"
        class="min-w-0 flex-1 cursor-default rounded-compact transition hover:opacity-75 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary-500"
        :class="[resultColor(run), compact ? 'h-5' : 'h-7']"
        :data-run-id="run.id"
        :aria-label="label(run)"
        :aria-describedby="hovered?.id === run.id ? tooltipID : undefined"
        @mouseenter="openPreview(run)"
        @focus="openPreview(run)"
        @blur="leaveFocus"
        @keydown.esc.stop="closePreview"
      />
    </div>
    <p v-if="!compact" class="mt-1 text-xs text-gray-400">
      {{
        ordered.length
          ? t('intelligence.recentResults', { count: ordered.length })
          : t('intelligence.noRuns')
      }}
    </p>
    <Teleport to="body">
      <div
        v-if="hovered"
        :id="tooltipID"
        role="tooltip"
        data-testid="intelligence-history-preview"
        :data-preview-run-id="hovered.id"
        class="intelligence-history-preview pointer-events-none fixed left-1/2 top-1/2 z-tooltip -translate-x-1/2 -translate-y-1/2 space-y-2 rounded-surface border border-gray-200 bg-white p-3 text-xs shadow-xl dark:border-dark-600 dark:bg-dark-800"
        :class="{ 'has-image': hovered.has_artifact || hovered.has_thumbnail || hovered.html }"
      >
        <div class="flex items-center justify-between gap-3">
          <span>{{ timestamp(hovered.created_at) }}</span>
          <IntelligenceStatusBadge :status="resultStatus(hovered)" />
        </div>
        <p class="text-gray-500" data-testid="history-first-token">
          {{ t('intelligence.firstToken') }}
          {{ typeof hovered.first_token_ms === 'number' ? `${hovered.first_token_ms} ms` : '—' }}
        </p>
        <IntelligenceThumbnail
          v-if="hovered.has_artifact || hovered.has_thumbnail || hovered.html"
          :run-id="hovered.id"
          :title="label(hovered)"
        />
      </div>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, useId } from 'vue'
import { useI18n } from 'vue-i18n'
import type { IntelligenceRun } from '@/api/intelligence'
import { formatDateTime } from '@/utils/format'
import { recentRuns, resultColor, resultStatus } from './results'
import IntelligenceThumbnail from './IntelligenceThumbnail.vue'
import IntelligenceStatusBadge from './IntelligenceStatusBadge.vue'
import IntelligenceStatusLegend from './IntelligenceStatusLegend.vue'

// IntelligenceResultBar.vue renders sixty chronological status slots using the
// shared result classification. Pointer hover or keyboard focus opens one
// teleported panel with the selected screenshot, second-precision timestamp, and
// observed first-token delay in milliseconds. Nodes expose descriptive labels
// and display previews without click actions or detail navigation.
// Shared badges label quality and execution states, and standalone histories
// include a legend explaining the slot colors before a node is selected.
//
// The panel stays centered in the viewport, and screenshot previews grow to
// eight hundred pixels across while fitting the available width and height.
// Moving between nodes updates the existing panel and screenshot component.
// The strip owns pointer departure, so the gaps between neighboring nodes do
// not dismiss the preview. Screenshot loading rejects stale completions inside
// IntelligenceThumbnail, while the separate latest HTML remains animated. Leaving
// the strip, moving focus outside it, scrolling, resizing, or pressing Escape
// dismisses the current preview and releases its mounted image consumer.

// The supplied runs are sorted into sixty visible slots. Hover and focus update
// page-local display state; result nodes have no mutation or navigation action.
const props = defineProps<{ runs: IntelligenceRun[]; compact?: boolean }>()
const { t } = useI18n()
const ordered = computed(() => recentRuns(props.runs))
const hovered = ref<IntelligenceRun | null>(null)
const tooltipID = useId()
const strip = ref<HTMLElement>()

// timestamp formats year-first local dates through seconds for numeric history labels.
function timestamp(value: string): string {
  return formatDateTime(
    value,
    {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      hourCycle: 'h23',
    },
    'zh-CN',
  )
}

// openPreview selects the observation displayed in the centered screenshot panel.
// Pointer and keyboard selection reuse its mounted image consumer, allowing
// movement between neighboring nodes to replace the displayed screenshot in place.
function openPreview(run: IntelligenceRun) {
  hovered.value = run
}

// closePreview dismisses the history panel while leaving the latest drawing active.
function closePreview() {
  hovered.value = null
}

// leaveFocus dismisses the preview when keyboard focus leaves the history strip.
// Focus moving to another node keeps the panel mounted until that node selects
// its screenshot, allowing a continuous keyboard inspection of the saved history.
function leaveFocus(event: FocusEvent) {
  if (!(event.relatedTarget instanceof Node) || !strip.value?.contains(event.relatedTarget)) {
    closePreview()
  }
}
// label formats the observation timestamp and status for accessible result
// inspection.
const label = (run: IntelligenceRun) =>
  t('intelligence.resultLabel', {
    time: timestamp(run.created_at),
    status: t(`intelligence.statuses.${resultStatus(run)}`),
  })

onMounted(() => {
  // Scrolling and viewport changes can move the inspected node away from the
  // pointer, so dismiss its centered screenshot until another node is selected.
  window.addEventListener('scroll', closePreview, true)
  window.addEventListener('resize', closePreview)
})
onBeforeUnmount(() => {
  // Release global listeners with the result bar so removed groups cannot retain
  // their history state or react to scrolling on another authenticated page.
  window.removeEventListener('scroll', closePreview, true)
  window.removeEventListener('resize', closePreview)
})
</script>

<style scoped>
.intelligence-history-preview {
  width: min(20rem, calc(100vw - 2rem));
}

.intelligence-history-preview.has-image {
  /* Reserve seven rem for metadata, padding, and viewport margins before
     converting the remaining image height to a four-to-three canvas width. */
  width: min(50rem, calc(100vw - 2rem), calc((100dvh - 7rem) * 4 / 3 + 1.5rem));
}
</style>
