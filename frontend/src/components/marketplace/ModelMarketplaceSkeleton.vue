<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Skeleton from '@/components/common/Skeleton.vue'

// ModelMarketplaceSkeleton.vue supplies loading placeholders for the marketplace's
// vertical group list. Each placeholder reserves a summary and centered model
// disclosure entry matching a collapsed group, while one translated status label
// announces loading and decorative placeholder content remains hidden.

// The localized status label names the whole loading region; its repeated
// visual skeleton cards are decorative rather than independently announced content.
const { t } = useI18n()
</script>

<template>
  <div
    class="min-w-0 space-y-4"
    role="status"
    :aria-label="t('common.loading')"
    aria-busy="true"
    data-testid="marketplace-skeleton"
  >
    <section
      v-for="group in 4"
      :key="group"
      class="card min-w-0 overflow-hidden"
      aria-hidden="true"
    >
      <div class="space-y-4 p-4 md:p-6">
        <div class="flex items-center gap-3">
          <Skeleton :width="48" :height="48" />
          <div class="flex-1 space-y-3">
            <Skeleton width="35%" :height="20" />
            <Skeleton width="60%" :height="12" />
          </div>
        </div>
        <div class="flex items-center gap-2">
          <Skeleton :width="80" :height="24" />
          <Skeleton :width="96" :height="24" />
        </div>
      </div>
      <div
        class="flex min-h-11 items-center justify-center border-t border-gray-100 px-4 py-2 dark:border-dark-700"
      >
        <Skeleton :width="112" :height="16" />
      </div>
    </section>
  </div>
</template>
