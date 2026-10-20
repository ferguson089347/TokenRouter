<template>
  <AppLayout fit-viewport>
    <!-- 不加 mx-auto：app-main 是 flex 列容器，auto 边距会让内容收缩到内容宽度并与页头错位。
         窄屏按兑换、历史、订阅顺序堆叠；宽屏锁定视口高度，左栏上下放兑换与订阅，右栏历史跨两行并在卡内滚动。 -->
    <div
      class="grid gap-4 lg:min-h-0 lg:flex-1 lg:grid-cols-[22.5rem_minmax(0,1fr)] lg:grid-rows-[auto_minmax(0,1fr)]"
    >
      <section data-testid="redeem-panel" class="card p-4 sm:p-6">
        <div class="flex flex-col gap-4">
          <RedeemCelebration
            :sequence="celebration?.sequence ?? 0"
            :title="celebration?.title ?? ''"
            :detail="celebration?.detail ?? ''"
          >
            <dl class="grid grid-cols-2 gap-4">
              <div>
                <dt class="text-xs font-medium text-gray-500 dark:text-dark-400">
                  {{ t('redeem.currentBalance') }}
                </dt>
                <dd class="mt-1 text-2xl font-semibold tabular-nums text-gray-900 dark:text-white">
                  {{ formatBalanceAmount(user?.balance, { fractionDigits: 2 }) }}
                </dd>
              </div>
              <div>
                <dt class="text-xs font-medium text-gray-500 dark:text-dark-400">
                  {{ t('redeem.concurrency') }}
                </dt>
                <dd class="mt-1 text-2xl font-semibold tabular-nums text-gray-900 dark:text-white">
                  {{ user?.concurrency || 0 }}
                  <span class="text-sm font-normal text-gray-500 dark:text-dark-400">{{ t('redeem.requests') }}</span>
                </dd>
              </div>
            </dl>
          </RedeemCelebration>

          <!-- 中等宽度下输入框与按钮同排，宽屏左栏较窄时恢复纵向排列。 -->
          <form
            ref="redeemForm"
            class="flex flex-col gap-3 border-t border-gray-100 pt-4 dark:border-dark-700 sm:flex-row lg:flex-col"
            @submit.prevent="handleRedeem"
          >
            <label for="code" class="sr-only">{{ t('redeem.redeemCodeLabel') }}</label>
            <div class="input-icon-wrap min-w-0 flex-1">
              <div class="input-icon">
                <Icon name="gift" size="md" class="text-gray-400 dark:text-dark-500" />
              </div>
              <input
                id="code"
                ref="codeInput"
                v-model="redeemCode"
                type="text"
                required
                autocomplete="off"
                spellcheck="false"
                :placeholder="t('redeem.redeemCodePlaceholder')"
                :disabled="submitting"
                class="input input-has-icon"
              />
            </div>
            <button
              type="submit"
              :disabled="!redeemCode || submitting"
              class="btn btn-primary w-full sm:w-auto sm:px-6 lg:w-full"
            >
              <Icon
                v-if="submitting"
                name="loader"
                size="sm"
                :animate-on-hover="false"
                class="animate-spin"
              />
              {{ submitting ? t('redeem.redeeming') : t('redeem.redeemButton') }}
            </button>
          </form>
        </div>
      </section>

      <!-- 最近活动用分隔线划分列表项，使窄屏行内容共用一层内边距。 -->
      <section
        data-testid="redeem-history"
        class="card flex min-w-0 flex-col overflow-hidden lg:col-start-2 lg:row-span-2 lg:row-start-1 lg:min-h-0"
      >
        <div class="border-b border-gray-100 px-4 py-3 dark:border-dark-700 sm:px-6 sm:py-4">
          <h2 class="text-base font-semibold text-gray-900 dark:text-white">
            {{ t('redeem.recentActivity') }}
          </h2>
        </div>

        <div class="lg:min-h-0 lg:flex-1 lg:overflow-y-auto">
          <!-- 首次加载显示骨架，翻页时当前列表降低透明度并保持占位。 -->
          <ContentSkeleton v-if="loadingHistory && history.length === 0" variant="list" :rows="4" class="p-6" />

          <ul
            v-else-if="history.length > 0"
            :class="[
              'divide-y divide-gray-100 transition-opacity dark:divide-dark-700',
              loadingHistory ? 'opacity-60' : ''
            ]"
          >
            <li
              v-for="item in history"
              :key="item.id"
              class="flex items-center gap-3 px-4 py-3 sm:gap-4 sm:px-6"
            >
              <div
                :class="[
                  'flex h-9 w-9 shrink-0 items-center justify-center rounded-compact',
                  getHistoryTone(item).bg
                ]"
              >
                <BalanceIcon
                  v-if="isBalanceType(item.type)"
                  size="sm"
                  :class="getHistoryTone(item).text"
                />
                <Icon
                  v-else-if="isSubscriptionType(item.type)"
                  name="badge"
                  size="sm"
                  :class="getHistoryTone(item).text"
                />
                <Icon v-else name="bolt" size="sm" :class="getHistoryTone(item).text" />
              </div>

              <div class="min-w-0 flex-1">
                <p class="truncate text-sm font-medium text-gray-900 dark:text-white">
                  {{ getHistoryItemTitle(item) }}
                </p>
                <p class="mt-0.5 flex min-w-0 items-center gap-1.5 text-xs text-gray-500 dark:text-dark-400">
                  <span v-if="item.used_at" class="shrink-0">{{ formatDateTime(item.used_at) }}</span>
                  <template v-if="getHistoryItemMeta(item)">
                    <span v-if="item.used_at" aria-hidden="true">·</span>
                    <span
                      :class="['truncate', isAdminAdjustment(item.type) ? '' : 'font-mono']"
                      :title="getHistoryItemMeta(item)"
                    >
                      {{ getHistoryItemMeta(item) }}
                    </span>
                  </template>
                </p>
              </div>

              <p
                :class="[
                  'max-w-40 shrink-0 truncate text-right text-sm font-semibold tabular-nums',
                  getHistoryTone(item).text
                ]"
              >
                {{ formatHistoryValue(item) }}
              </p>
            </li>
          </ul>

          <div v-else class="empty-state lg:h-full">
            <div
              class="mb-4 flex h-12 w-12 items-center justify-center rounded-surface bg-gray-100 dark:bg-dark-800"
            >
              <Icon name="clock" size="lg" class="text-gray-400 dark:text-dark-500" />
            </div>
            <p class="text-sm text-gray-500 dark:text-dark-400">
              {{ t('redeem.historyWillAppear') }}
            </p>
          </div>
        </div>

        <Pagination
          v-if="historyTotal > HISTORY_PAGE_SIZE"
          class="shrink-0"
          :total="historyTotal"
          :page="historyPage"
          :page-size="HISTORY_PAGE_SIZE"
          :show-page-size-selector="false"
          @update:page="fetchHistory"
        />
      </section>

      <!-- 我的订阅：宽屏位于兑换卡下方并撑满左栏剩余高度，窄屏排在历史之后。 -->
      <section
        data-testid="redeem-subscriptions"
        class="card flex min-w-0 flex-col overflow-hidden lg:col-start-1 lg:row-start-2 lg:min-h-0"
      >
        <div
          class="flex items-center justify-between gap-4 border-b border-gray-100 px-4 py-3 dark:border-dark-700 sm:px-6 sm:py-4"
        >
          <h2 class="text-base font-semibold text-gray-900 dark:text-white">
            {{ t('subscriptionProgress.title') }}
          </h2>
          <router-link
            v-if="appStore.cachedPublicSettings?.subscription_entry_enabled !== false"
            to="/subscriptions"
            class="shrink-0 text-sm text-primary-600 hover:underline dark:text-primary-400"
          >
            {{ t('subscriptionProgress.viewAll') }}
          </router-link>
        </div>

        <SubscriptionUsageList
          v-if="activeSubscriptions.length > 0"
          class="lg:min-h-0 lg:flex-1 lg:overflow-y-auto"
          :subscriptions="activeSubscriptions"
          item-class="border-b border-gray-100 px-4 py-3 last:border-b-0 dark:border-dark-700 sm:px-6"
        />
        <p
          v-else
          class="flex items-center justify-center px-4 py-8 text-sm text-gray-500 dark:text-dark-400 lg:flex-1"
        >
          {{ t('subscriptionProgress.noSubscriptions') }}
        </p>
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import ContentSkeleton from '@/components/common/ContentSkeleton.vue'
import { ref, computed, nextTick, onMounted, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import { useAppStore } from '@/stores/app'
import { useSubscriptionStore } from '@/stores/subscriptions'
import { redeemAPI, type RedeemHistoryItem } from '@/api'
import { extractApiErrorMessage } from '@/utils/apiError'
import AppLayout from '@/components/layout/AppLayout.vue'
import BalanceIcon from '@/components/common/BalanceIcon.vue'
import Pagination from '@/components/common/Pagination.vue'
import SubscriptionUsageList from '@/components/common/SubscriptionUsageList.vue'
import Icon from '@/components/icons/Icon.vue'
import RedeemCelebration from '@/components/user/RedeemCelebration.vue'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import { formatDateTime } from '@/utils/format'

// RedeemView.vue accepts redemption codes and displays their history alongside
// active subscription allowances. Successful redemption refreshes the account and
// entitlement information, while public subscription visibility controls the link
// from the allowance summary to the subscription details page.

const { t } = useI18n()
const authStore = useAuthStore()
const appStore = useAppStore()
const subscriptionStore = useSubscriptionStore()
const { formatBalanceAmount } = useBalanceDisplay()

const user = computed(() => authStore.user)

const redeemCode = ref('')
const redeemForm = ref<HTMLFormElement | null>(null)
const codeInput = ref<HTMLInputElement | null>(null)
const submitting = ref(false)
const celebration = ref<{ sequence: number; title: string; detail: string } | null>(null)
let celebrationSequence = 0
let disposed = false

// 兑换历史按固定页容量分页，宽屏下页面高度固定，列表在卡内滚动。
const HISTORY_PAGE_SIZE = 10
const history = ref<RedeemHistoryItem[]>([])
const historyPage = ref(1)
const historyTotal = ref(0)
const loadingHistory = ref(false)
// 快速连续翻页时只采纳最后一次请求的结果。
let historyRequestId = 0

const activeSubscriptions = computed(() => subscriptionStore.activeSubscriptions)
const redeemErrorMap = computed<Record<string, string>>(() => ({
  REDEEM_PAYMENT_REQUIRED: t('redeem.paymentRequired'),
  REDEEM_CODE_EXPIRED: t('redeem.codeExpired'),
  REDEEM_CODE_MAX_USED: t('redeem.codeMaxUsed'),
  REDEEM_CODE_ALREADY_USED: t('redeem.codeAlreadyUsed'),
  REDEEM_CODE_USED: t('redeem.codeMaxUsed')
}))

// 余额类记录共用金额与正负号格式。
const isBalanceType = (type: string) => {
  return type === 'balance' || type === 'admin_balance' || type === 'affiliate_balance'
}

const isSubscriptionType = (type: string) => {
  return type === 'subscription'
}

const isAdminAdjustment = (type: string) => {
  return type === 'admin_balance' || type === 'admin_concurrency'
}

// getHistoryTone 按记录类型和增减方向返回图标底色与文字色，列表各处共用同一套配色。
const getHistoryTone = (item: RedeemHistoryItem) => {
  if (isBalanceType(item.type)) {
    return item.value >= 0
      ? { bg: 'bg-emerald-100 dark:bg-emerald-900/30', text: 'text-emerald-600 dark:text-emerald-400' }
      : { bg: 'bg-red-100 dark:bg-red-900/30', text: 'text-red-600 dark:text-red-400' }
  }
  if (isSubscriptionType(item.type)) {
    return { bg: 'bg-purple-100 dark:bg-purple-900/30', text: 'text-purple-600 dark:text-purple-400' }
  }
  return item.value >= 0
    ? { bg: 'bg-blue-100 dark:bg-blue-900/30', text: 'text-blue-600 dark:text-blue-400' }
    : { bg: 'bg-orange-100 dark:bg-orange-900/30', text: 'text-orange-600 dark:text-orange-400' }
}

const getHistoryItemTitle = (item: RedeemHistoryItem) => {
  if (item.type === 'balance') {
    return t('redeem.balanceAddedRedeem')
  } else if (item.type === 'admin_balance') {
    return item.value >= 0 ? t('redeem.balanceAddedAdmin') : t('redeem.balanceDeductedAdmin')
  } else if (item.type === 'concurrency') {
    return t('redeem.concurrencyAddedRedeem')
  } else if (item.type === 'admin_concurrency') {
    return item.value >= 0 ? t('redeem.concurrencyAddedAdmin') : t('redeem.concurrencyReducedAdmin')
  } else if (item.type === 'subscription') {
    return t('redeem.subscriptionAssigned')
  } else if (item.type === 'affiliate_balance') {
    return t('redeem.affiliateBalance')
  }
  return t('common.unknown')
}

// getHistoryItemMeta 返回时间后的补充信息：管理员调整显示备注，兑换记录显示兑换码。
const getHistoryItemMeta = (item: RedeemHistoryItem) => {
  if (isAdminAdjustment(item.type)) {
    return item.notes || ''
  }
  return item.code
}

const formatHistoryValue = (item: RedeemHistoryItem) => {
  if (isBalanceType(item.type)) {
    return formatSignedBalanceAmount(item.value, 2)
  } else if (isSubscriptionType(item.type)) {
    return item.plan?.name || (item.plan_id ? `Plan #${item.plan_id}` : t('redeem.subscriptionAssigned'))
  } else {
    const sign = item.value >= 0 ? '+' : ''
    return `${sign}${item.value} ${t('redeem.requests')}`
  }
}

const formatSignedBalanceAmount = (value: number, fractionDigits: number) => {
  const sign = value >= 0 ? '+' : '-'
  return `${sign}${formatBalanceAmount(Math.abs(value), { fractionDigits })}`
}

const fetchHistory = async (page = historyPage.value) => {
  const requestId = ++historyRequestId
  loadingHistory.value = true
  try {
    const result = await redeemAPI.getHistory(page, HISTORY_PAGE_SIZE)
    if (disposed || requestId !== historyRequestId) return true
    history.value = result.items
    historyTotal.value = result.total
    historyPage.value = page
    return true
  } catch (error) {
    console.error('Failed to fetch history:', error)
    return false
  } finally {
    if (requestId === historyRequestId) {
      loadingHistory.value = false
    }
  }
}

// 成功内容来自兑换响应，不依赖后续刷新，也不从余额差额推算到账金额。
const getCelebrationDetail = (result: RedeemHistoryItem) => {
  if (result.type === 'balance') {
    return t('redeem.balanceReceived', { amount: formatSignedBalanceAmount(result.value, 2) })
  }
  if (result.type === 'concurrency') {
    const count = `${result.value >= 0 ? '+' : ''}${result.value}`
    return t('redeem.concurrencyReceived', { count })
  }
  return t('redeem.subscriptionReceived')
}

const handleRedeem = async () => {
  if (submitting.value) return
  if (!redeemCode.value.trim()) {
    appStore.showError(t('redeem.pleaseEnterCode'))
    return
  }

  const restoreInputFocus = redeemForm.value?.contains(document.activeElement) ?? false
  submitting.value = true
  celebration.value = null

  try {
    let result: RedeemHistoryItem
    try {
      result = await redeemAPI.redeem(redeemCode.value.trim())
    } catch (error) {
      if (!disposed) {
        appStore.showError(extractApiErrorMessage(error, t('redeem.failedToRedeem'), redeemErrorMap.value))
      }
      return
    }
    if (disposed) return

    redeemCode.value = ''
    celebration.value = {
      sequence: ++celebrationSequence,
      title: t('redeem.codeRedeemSuccess'),
      detail: getCelebrationDetail(result)
    }

    // 权益已发放，后续刷新独立完成；局部失败不会把兑换成功改报成失败。
    // 历史回到第一页，订阅兑换强制刷新，任一刷新失败也不阻止其余数据更新。
    const results = await Promise.allSettled([
      authStore.refreshUser(),
      fetchHistory(1),
      result.type === 'subscription'
        ? subscriptionStore.fetchActiveSubscriptions(true)
        : Promise.resolve()
    ])
    const historyResult = results[1]
    const refreshFailed = results.some((entry) => entry.status === 'rejected') ||
      (historyResult.status === 'fulfilled' && historyResult.value === false)
    if (!disposed && refreshFailed) {
      appStore.showWarning(t('redeem.dataRefreshFailed'))
    }
  } finally {
    // 提交状态随请求结束而清除，庆祝动画和成功信息按各自的时长展示。
    submitting.value = false
    await nextTick()
    // 禁用控件可能让焦点落到 body；用户已移到其他控件时不抢回焦点。
    if (!disposed && restoreInputFocus && document.activeElement === document.body) {
      codeInput.value?.focus({ preventScroll: true })
    }
  }
}

onBeforeUnmount(() => {
  disposed = true
  historyRequestId++
})

onMounted(() => {
  fetchHistory()
  subscriptionStore.fetchActiveSubscriptions().catch((error) => {
    console.error('Failed to load subscriptions on redeem page:', error)
  })
})
</script>
