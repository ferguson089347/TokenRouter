<template>
  <div>
    <div
      class="flex h-10 min-w-0 items-center gap-1"
      :aria-label="t('intelligence.recentResults', { count: 60 })"
    >
      <span
        v-for="slot in Math.max(0, 60 - ordered.length)"
        :key="`empty-${slot}`"
        class="h-7 min-w-0 flex-1 rounded-compact bg-gray-100 dark:bg-dark-700"
        aria-hidden="true"
      />
      <button
        v-for="run in ordered"
        :key="run.id"
        type="button"
        class="h-7 min-w-0 flex-1 rounded-compact transition hover:opacity-75 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary-500 disabled:cursor-default"
        :class="resultColor(run)"
        :disabled="!hasDetails(run)"
        :data-run-id="run.id"
        :aria-label="label(run)"
        :title="label(run)"
        @click="emit('select', run)"
      />
    </div>
    <p class="mt-1 text-xs text-gray-400">
      {{
        ordered.length
          ? t('intelligence.recentResults', { count: ordered.length })
          : t('intelligence.noRuns')
      }}
    </p>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { IntelligenceRun } from '@/api/intelligence'
import { formatDateTime } from '@/utils/format'
import { recentRuns, resultColor, resultStatus } from './results'

// IntelligenceResultBar.vue renders sixty chronological status slots using the
// shared result classification. Only observations with retained details can
// open the result dialog, while accessible labels include the observation
// timestamp and outcome for keyboard and assistive-technology users.

// The supplied runs are sorted into sixty visible slots, and selection events
// identify the saved observation requested by keyboard or pointer activation.
const props = defineProps<{ runs: IntelligenceRun[] }>()
const emit = defineEmits<{ select: [run: IntelligenceRun] }>()
const { t } = useI18n()
const ordered = computed(() => recentRuns(props.runs))
// Keep a status observation visible when details are absent, while disabling
// activation that would otherwise open an empty detail dialog.
// hasDetails reports whether an observation can open meaningful saved detail.
const hasDetails = (run: IntelligenceRun) =>
  !!(
    run.has_detail ||
    run.has_artifact ||
    run.question ||
    run.answer ||
    run.html ||
    run.error_message ||
    run.assessment_reason
  )
// label formats the observation timestamp and status for accessible result
// activation.
const label = (run: IntelligenceRun) =>
  t('intelligence.resultLabel', {
    time: formatDateTime(run.created_at),
    status: t(`intelligence.statuses.${resultStatus(run)}`),
  })
</script>
