<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Skeleton from '@/components/common/Skeleton.vue'

// ModelMarketplaceSkeleton.vue supplies loading placeholders for marketplace
// group navigation and the selected model directory. The placeholders follow
// the loaded page's sidebar and model grid, while one translated status label
// announces loading and decorative placeholder content remains hidden.

// The localized status label names the whole loading region; its repeated
// visual skeleton cards are decorative rather than independently announced content.
const { t } = useI18n()
</script>

<template>
  <div
    class="grid min-w-0 items-start gap-4 lg:grid-cols-[16rem_minmax(0,1fr)]"
    role="status"
    :aria-label="t('common.loading')"
    aria-busy="true"
    data-testid="marketplace-skeleton"
  >
    <aside class="card min-w-0 overflow-hidden p-4" aria-hidden="true">
      <Skeleton width="40%" :height="16" />
      <div class="mt-4 flex gap-4 overflow-hidden lg:flex-col">
        <div v-for="group in 4" :key="group" class="w-48 shrink-0 space-y-3 py-3 lg:w-full">
          <Skeleton width="85%" :height="20" />
          <Skeleton width="55%" :height="12" />
        </div>
      </div>
    </aside>
    <section class="min-w-0 space-y-4" aria-hidden="true">
      <div class="card space-y-4 p-4 md:p-5">
        <Skeleton width="35%" :height="20" />
        <div class="flex items-center gap-3">
          <Skeleton :width="48" :height="48" />
          <div class="flex-1 space-y-3">
            <Skeleton width="60%" :height="20" />
            <Skeleton width="80%" :height="12" />
          </div>
        </div>
      </div>
      <Skeleton :width="96" :height="16" />
      <div class="grid grid-cols-1 gap-4 md:grid-cols-2 2xl:grid-cols-3">
        <div v-for="model in 6" :key="model" class="card space-y-4 p-4">
          <Skeleton width="65%" :height="20" />
          <Skeleton width="40%" :height="12" />
          <Skeleton :height="36" />
          <Skeleton width="50%" :height="12" />
        </div>
      </div>
    </section>
  </div>
</template>
