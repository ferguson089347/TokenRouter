<template>
  <span
    class="inline-flex shrink-0 items-center gap-1 whitespace-nowrap rounded-compact px-2 py-1 text-xs font-semibold"
    :class="appearance[status].color"
    :data-intelligence-status="status"
  >
    <Icon :name="appearance[status].icon" size="xs" :animate-on-hover="false" />
    {{ t(`intelligence.statuses.${status}`) }}
  </span>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import type { IconName } from '@/components/icons/registry'
import type { IntelligenceDisplayStatus } from './results'

// IntelligenceStatusBadge.vue labels quality observations with text, an icon,
// and a shared status color across rows, history previews, and administrator
// details. Execution errors and missing assessments keep their own labels so
// readers can distinguish an unsuccessful check from a degraded model result.

// Badge inputs use the normalized display status; callers derive it from the
// observation before rendering so execution failures keep precedence over verdicts.
defineProps<{ status: IntelligenceDisplayStatus }>()
const { t } = useI18n()

// appearance assigns distinct symbols to quality, diagnostic, and pending states.
// Both themes keep readable status text on a tinted background for quick scanning.
const appearance: Record<IntelligenceDisplayStatus, { icon: IconName; color: string }> = {
  passed: {
    icon: 'check',
    color: 'bg-emerald-50 text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300',
  },
  incorrect: {
    icon: 'arrowDown',
    color: 'bg-rose-50 text-rose-700 dark:bg-rose-500/15 dark:text-rose-300',
  },
  error: {
    icon: 'exclamationTriangle',
    color: 'bg-amber-50 text-amber-800 dark:bg-amber-400/15 dark:text-amber-300',
  },
  unknown: {
    icon: 'questionCircle',
    color: 'bg-amber-50 text-amber-800 dark:bg-amber-400/15 dark:text-amber-300',
  },
  running: {
    icon: 'refresh',
    color: 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-dark-200',
  },
  queued: {
    icon: 'clock',
    color: 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-dark-200',
  },
}
</script>
