<template>
  <!-- 默认变体没有生效订阅时整体隐藏；状态变体始终渲染插槽内容，只在有订阅时显示状态点并可展开。 -->
  <div v-if="variant === 'status' || hasActiveSubscriptions" ref="containerRef" class="relative">
    <button
      :class="triggerClass"
      :title="hasActiveSubscriptions ? t('subscriptionProgress.viewDetails') : undefined"
      :disabled="!hasActiveSubscriptions"
      @click="toggleTooltip"
    >
      <template v-if="variant === 'status'">
        <slot />
        <!-- 状态点取用量最高的订阅，颜色表示用量是否接近上限。 -->
        <span
          v-if="statusDotClass"
          data-testid="subscription-status-dot"
          class="ml-1 h-2 w-2 shrink-0 rounded-full"
          :class="statusDotClass"
        />
      </template>
      <div v-else class="flex items-center gap-2">
        <Icon name="creditCard" size="sm" class="text-primary-600 dark:text-primary-400" />
        <div class="flex items-center gap-0.5">
          <div
            v-for="(dotClass, index) in displayDots"
            :key="index"
            class="rounded-full"
            :class="['h-2 w-2', dotClass]"
          />
        </div>
        <span class="text-xs font-medium text-primary-700 dark:text-primary-300">
          {{ statusCount }}
        </span>
      </div>
    </button>

    <MotionTransition name="dropdown-fade">
      <div
        v-if="tooltipOpen"
        class="dropdown subscription-progress-popover right-0 z-50 mt-2 w-[340px] overflow-hidden py-0"
      >
        <div class="border-b border-gray-100 p-3 dark:border-dark-700">
          <h3 class="text-sm font-semibold text-gray-900 dark:text-white">
            {{ t('subscriptionProgress.title') }}
          </h3>
          <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">
            {{ t('subscriptionProgress.activeCount', { count: activeSubscriptions.length }) }}
          </p>
        </div>

        <SubscriptionUsageList
          class="max-h-64 overflow-y-auto"
          :subscriptions="activeSubscriptions"
          item-class="border-b border-gray-50 p-3 last:border-b-0 dark:border-dark-700/50"
        />

        <div v-if="appStore.cachedPublicSettings?.subscription_entry_enabled !== false" class="border-t border-gray-100 p-2 dark:border-dark-700">
          <router-link
            to="/subscriptions"
            class="block w-full py-1 text-center text-xs text-primary-600 hover:underline dark:text-primary-400"
            @click="closeTooltip"
          >
            {{ t('subscriptionProgress.viewAll') }}
          </router-link>
        </div>
      </div>
    </MotionTransition>
  </div>
</template>

<script setup lang="ts">
import MotionTransition from '@/components/common/MotionTransition.vue'
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import SubscriptionUsageList from '@/components/common/SubscriptionUsageList.vue'
import { useSubscriptionUsage } from '@/composables/useSubscriptionUsage'
import { useAppStore, useSubscriptionStore } from '@/stores'

// SubscriptionProgressMini.vue displays active subscription usage in a compact
// trigger and expandable summary. The status variant keeps its balance slot
// visible for accounts with no active subscriptions, while usage dots summarize
// the subscriptions supplied by the shared subscription store.
//
// Subscription navigation visibility controls the detail link below the usage
// summary. The usage figures and status indicators remain available when that
// link is hidden, allowing existing subscribers to monitor their allowances.
// Outside clicks and component teardown close the popover through its local
// interaction handlers and document listener lifecycle.

const props = withDefaults(defineProps<{
  variant?: 'default' | 'status'
}>(), {
  variant: 'default'
})

const { t } = useI18n()
const subscriptionStore = useSubscriptionStore()
const appStore = useAppStore()
const { sortByUsage, getProgressDotClass } = useSubscriptionUsage()

const containerRef = ref<HTMLElement | null>(null)
const tooltipOpen = ref(false)

const activeSubscriptions = computed(() => subscriptionStore.activeSubscriptions)
const hasActiveSubscriptions = computed(() => subscriptionStore.hasActiveSubscriptions)
const variant = computed(() => props.variant)
const statusCount = computed(() => activeSubscriptions.value.length)
const triggerClass = computed(() => {
  if (variant.value === 'status') {
    // 状态变体与顶栏图标按钮同为无边框按钮，没有订阅时不响应悬停。
    return 'flex h-9 items-center gap-1 rounded-control px-3 text-sm transition-colors hover:bg-primary-100 disabled:cursor-default disabled:hover:bg-transparent dark:hover:bg-dark-700 dark:disabled:hover:bg-transparent'
  }
  return 'flex cursor-pointer items-center gap-2 rounded-control bg-primary-50 px-3 py-1.5 transition-colors hover:bg-primary-100 dark:bg-primary-900/20 dark:hover:bg-primary-900/30'
})

const displayDots = computed(() =>
  sortByUsage(activeSubscriptions.value)
    .slice(0, 3)
    .map((subscription) => getProgressDotClass(subscription))
)
const statusDotClass = computed(() => displayDots.value[0] ?? '')

function toggleTooltip() {
  if (!hasActiveSubscriptions.value) return
  tooltipOpen.value = !tooltipOpen.value
}

function closeTooltip() {
  tooltipOpen.value = false
}

function handleClickOutside(event: MouseEvent) {
  if (containerRef.value && !containerRef.value.contains(event.target as Node)) {
    closeTooltip()
  }
}

onMounted(() => {
  document.addEventListener('click', handleClickOutside)
  subscriptionStore.fetchActiveSubscriptions().catch((error) => {
    console.error('Failed to load subscriptions in SubscriptionProgressMini:', error)
  })
})

onBeforeUnmount(() => {
  document.removeEventListener('click', handleClickOutside)
})
</script>

<style scoped>

/* 手机端弹层在视口内居中，并在两侧留边。 */
@media (max-width: 639px) {
  .subscription-progress-popover {
    position: fixed;
    top: 4.5rem;
    right: 0.75rem;
    left: 0.75rem;
    width: auto;
    margin-top: 0;
  }
}
</style>
