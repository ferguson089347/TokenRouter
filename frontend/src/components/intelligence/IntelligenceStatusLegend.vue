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
// labels and symbols. The page shows one legend above its compact rows, while
// standalone administrator histories include the same guide above their nodes.
// Unknown assessments remain visible beside execution errors with separate labels.

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
