<template>
  <div class="flex flex-wrap items-center gap-2" :aria-label="t('intelligence.statusLegend')">
    <span class="text-xs text-gray-500">{{ t('intelligence.statusLegend') }}</span>
    <span v-for="status in legend" :key="status" class="inline-flex items-center gap-1">
      <span
        class="h-3 w-1 rounded-compact"
        :class="intelligenceStatusColors[status]"
        aria-hidden="true"
      />
      <IntelligenceStatusBadge :status="status" />
    </span>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { intelligenceStatusColors, type IntelligenceDisplayStatus } from './results'
import IntelligenceStatusBadge from './IntelligenceStatusBadge.vue'

// IntelligenceStatusLegend.vue explains history colors beside their status
// labels and symbols in standalone administrator histories. User histories show
// their assessed-result colors within the user page. This complete guide keeps
// unknown assessments, execution errors, and pending work distinguishable during
// administrator inspection of individual observations.

const { t } = useI18n()

// legend pairs history slot colors with all six normalized display states.
// Matching badge labels let readers interpret colors before hovering a historical node.
const legend: IntelligenceDisplayStatus[] = [
  'passed',
  'incorrect',
  'error',
  'unknown',
  'running',
  'queued',
]
</script>
