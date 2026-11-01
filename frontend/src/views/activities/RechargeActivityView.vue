<template>
  <div
    class="ba-theme-shell relative min-h-screen overflow-hidden pt-[var(--header-h)] text-gray-950 dark:text-white"
  >
    <div class="ba-theme-backdrop pointer-events-none fixed inset-0" />
    <AppHeader public-page />
    <div v-if="loading" class="mx-auto max-w-6xl px-4 pt-12 md:px-6 lg:px-8">
      <ContentSkeleton variant="form" :rows="6" />
    </div>
    <div v-else-if="!data" class="mx-auto max-w-lg space-y-6 px-4 py-20 text-center">
      <Icon name="gift" size="xl" class="mx-auto text-gray-400" />
      <h1 class="text-2xl font-semibold">{{ t('rechargeBonus.notFound') }}</h1>
      <RouterLink to="/purchase" class="btn btn-primary">{{
        t('rechargeBonus.backToRecharge')
      }}</RouterLink>
    </div>
    <template v-else>
      <section
        class="relative border-b border-primary-100 bg-primary-50 dark:border-dark-700 dark:bg-primary-500/8"
      >
        <div
          class="mx-auto grid max-w-6xl items-center gap-10 px-4 py-10 md:px-6 md:py-14 lg:grid-cols-[minmax(0,1fr)_360px] lg:px-8"
        >
          <div>
            <div
              class="inline-flex items-center gap-2 rounded-full border border-primary-200 bg-white px-3 py-1.5 text-sm dark:border-dark-600 dark:bg-dark-900"
            >
              <span
                class="h-2 w-2 rounded-full"
                :class="status === 'ongoing' ? 'bg-emerald-500' : 'bg-gray-400'"
              />
              {{ t(`rechargeBonus.${status}`) }}
            </div>
            <h1
              class="mt-4 text-balance text-3xl font-bold leading-tight tracking-tight sm:text-4xl lg:text-5xl"
              >{{ data.activity.title }}</h1
            >
            <p
              v-if="data.activity.subtitle"
              class="mt-4 max-w-xl whitespace-pre-line text-base leading-7 text-gray-600 dark:text-dark-300"
              >{{ data.activity.subtitle }}</p
            >
            <p class="mt-4 text-sm leading-6 text-gray-500 dark:text-dark-400"
              >{{ periodLabel }}
              <span class="whitespace-nowrap">({{ data.activity.timezone }})</span></p
            >
            <div class="mt-7 flex flex-wrap items-center gap-4">
              <a href="#recharge-calculator" class="btn btn-primary px-6 py-3"
                >{{ t('rechargeBonus.tryAmount') }}<Icon name="arrowDown" size="sm"
              /></a>
              <div
                v-if="countdownCells.length"
                class="flex items-stretch overflow-hidden rounded-control border border-gray-200 bg-white dark:border-dark-600 dark:bg-dark-900"
                data-testid="activity-countdown"
              >
                <span class="self-center px-3 text-xs text-gray-500 dark:text-dark-300">{{
                  t(status === 'upcoming' ? 'rechargeBonus.startsIn' : 'rechargeBonus.endsIn')
                }}</span>
                <div
                  v-for="cell in countdownCells"
                  :key="cell.label"
                  class="border-l border-gray-200 px-4 py-2 text-center dark:border-dark-600"
                >
                  <p class="text-xl font-semibold tabular-nums leading-tight">{{ cell.value }}</p>
                  <p class="text-xs text-gray-500 dark:text-dark-300">{{ cell.label }}</p>
                </div>
              </div>
            </div>
          </div>

          <aside
            class="rounded-dialog border border-gray-200 bg-white p-6 dark:border-dark-600 dark:bg-dark-900"
          >
            <div class="flex items-start justify-between gap-3">
              <p class="text-xs text-gray-500 dark:text-dark-300">{{
                t('rechargeBonus.highestGift')
              }}</p>
              <span
                v-if="sortedTiers.length"
                class="rounded-full bg-primary-100 px-2.5 py-1 text-xs font-medium text-primary-700 dark:bg-primary-500/15 dark:text-primary-300"
                >{{ t('rechargeBonus.topTier') }}</span
              >
            </div>
            <p
              class="mt-2 break-words text-4xl font-bold tracking-tight text-primary-600 tabular-nums dark:text-primary-500"
              data-testid="activity-highest-gift"
              >+{{ formatBalanceAmount(highestReward, { fractionDigits: 2 }) }}</p
            >
            <p class="mt-1 text-xs text-gray-500 dark:text-dark-300">{{
              t('rechargeBonus.topTierCaption', {
                amount: formatBalanceAmount(highestTierMinimum, { fractionDigits: 2 }),
              })
            }}</p>
            <div v-if="sortedTiers.length" class="mt-5 grid grid-cols-7 gap-1" aria-hidden="true">
              <span
                v-for="tier in sortedTiers"
                :key="tier.min_amount"
                class="h-1.5 rounded-full"
                :class="
                  isReached(tier.min_amount)
                    ? 'bg-primary-600'
                    : isNext(tier.min_amount)
                      ? 'bg-primary-300 dark:bg-primary-500/40'
                      : 'bg-gray-200 dark:bg-dark-700'
                "
              />
            </div>
            <div
              v-if="sortedTiers.length"
              class="mt-2 flex justify-between text-xs text-gray-500 dark:text-dark-300"
            >
              <span>{{
                formatBalanceAmount(sortedTiers[0]!.min_amount, { fractionDigits: 0 })
              }}</span>
              <span>{{
                formatBalanceAmount(sortedTiers[sortedTiers.length - 1]!.min_amount, {
                  fractionDigits: 0,
                })
              }}</span>
            </div>
            <div
              class="mt-5 flex flex-wrap items-center gap-x-2 gap-y-1 border-t border-gray-200 pt-4 text-xs text-gray-500 dark:border-dark-600 dark:text-dark-300"
            >
              <span class="font-medium text-gray-700 dark:text-dark-100">{{
                t('rechargeBonus.tierCount', { count: sortedTiers.length })
              }}</span>
              <span aria-hidden="true">·</span>
              <span>{{ t('rechargeBonus.heroFootnote') }}</span>
            </div>
          </aside>
        </div>
      </section>

      <main class="relative mx-auto max-w-6xl px-4 pb-32 pt-8 md:px-6 md:pb-20 lg:px-8">
        <!-- The anchor leaves the fixed header and two rem of reading space above the calculator. -->
        <section
          id="recharge-calculator"
          class="scroll-mt-[calc(var(--header-h)+2rem)] rounded-surface border border-gray-200 bg-white p-6 dark:border-dark-600 dark:bg-dark-950 sm:p-8"
        >
          <div>
            <h2 class="text-xl font-semibold">{{ t('rechargeBonus.calculatorTitle') }}</h2>
            <p class="mt-2 max-w-2xl text-sm leading-6 text-gray-500 dark:text-dark-300">{{
              t('rechargeBonus.calculatorHint')
            }}</p>
          </div>
          <p
            v-if="!auth.isAuthenticated"
            class="mt-4 text-sm leading-6 text-gray-500 dark:text-dark-300"
            data-testid="activity-guest-progress-hint"
            >{{ t('rechargeBonus.guestProgressHint') }}</p
          >
          <div
            v-else-if="progressFailed"
            class="mt-4 flex flex-wrap items-center justify-between gap-4 rounded-control border border-gray-200 p-4 dark:border-dark-600"
            role="alert"
            data-testid="activity-progress-error"
          >
            <p class="text-sm text-gray-600 dark:text-dark-300">{{
              t('rechargeBonus.progressFailed')
            }}</p>
            <button type="button" class="btn btn-secondary" @click="retryGeneration++">{{
              t('common.retry')
            }}</button>
          </div>

          <div class="mt-6 grid gap-8 lg:grid-cols-[minmax(0,1fr)_320px]">
            <div>
              <label for="activity-recharge-amount" class="input-label"
                >{{ t('rechargeBonus.amount') }}
                <span v-if="paymentCurrency" class="font-normal text-gray-500 dark:text-dark-300"
                  >({{ paymentCurrency }})</span
                ></label
              >
              <input
                id="activity-recharge-amount"
                v-model.number="amount"
                class="input text-2xl font-semibold tabular-nums"
                type="number"
                :min="data.min_amount || 1"
                :max="data.max_amount > 0 ? data.max_amount : undefined"
                step="0.01"
                data-testid="activity-recharge-amount"
              />
              <div class="mt-3 flex flex-wrap gap-2">
                <button
                  v-for="credit in quickCredits"
                  :key="credit"
                  type="button"
                  class="btn btn-secondary btn-sm"
                  :disabled="!canPreview"
                  data-testid="activity-quick-add"
                  @click="addCredit(credit)"
                  >+{{
                    formatBalanceAmount(credit, { fractionDigits: 0, withSymbol: false })
                  }}</button
                >
                <button
                  v-if="nextTier"
                  type="button"
                  class="btn btn-secondary btn-sm text-primary-600 dark:text-primary-500"
                  :disabled="!canPreview"
                  data-testid="activity-fill-next-tier"
                  @click="selectTier(nextTier.min_amount)"
                  >{{
                    t('rechargeBonus.fillNextTier', {
                      amount: formatBalanceAmount(nextTier.min_amount - quote.cumulative_amount, {
                        fractionDigits: 2,
                      }),
                    })
                  }}</button
                >
              </div>
              <input
                v-model.number="amount"
                type="range"
                :min="data.min_amount || 1"
                :max="sliderMaximum"
                step="1"
                class="mt-4 w-full"
                :aria-label="t('rechargeBonus.amount')"
              />

              <div
                v-if="canPreview && sortedTiers.length"
                class="mt-6"
                data-testid="activity-progress"
              >
                <div class="flex flex-wrap items-baseline justify-between gap-2 text-sm">
                  <span class="text-gray-500 dark:text-dark-300"
                    >{{ t('rechargeBonus.cumulativeAmount') }}
                    <b class="font-semibold tabular-nums text-gray-950 dark:text-white">{{
                      formatBalanceAmount(quote.cumulative_before ?? 0, { fractionDigits: 2 })
                    }}</b></span
                  >
                  <span v-if="nextTier" class="text-gray-500 dark:text-dark-300"
                    >{{ t('rechargeBonus.nextStep') }}
                    <b class="font-semibold tabular-nums text-gray-950 dark:text-white">{{
                      formatBalanceAmount(nextTier.min_amount, { fractionDigits: 2 })
                    }}</b></span
                  >
                </div>
                <div
                  class="mt-2 h-1.5 overflow-hidden rounded-full bg-gray-100 dark:bg-dark-700"
                  role="progressbar"
                  :aria-valuenow="Math.round(progressPercent)"
                  aria-valuemin="0"
                  aria-valuemax="100"
                  :aria-label="t('rechargeBonus.cumulativeAmount')"
                >
                  <span
                    class="block h-full rounded-full bg-primary-600"
                    :style="{ width: `${progressPercent}%` }"
                  />
                </div>
                <p class="mt-1.5 text-sm leading-6 text-gray-500 dark:text-dark-300">
                  <template v-if="quote.tier_reward_total > 0">
                    {{ t('rechargeBonus.tierBonusAwarded') }}
                    <span class="font-medium text-primary-600 dark:text-primary-500">{{
                      formatBalanceAmount(quote.tier_reward_total, { fractionDigits: 2 })
                    }}</span>
                  </template>
                  <template v-if="nextTier">
                    {{
                      t('rechargeBonus.nextTier', {
                        amount: formatBalanceAmount(nextTier.min_amount - quote.cumulative_amount, {
                          fractionDigits: 2,
                        }),
                        gift: formatBalanceAmount(
                          Math.max(0, nextTier.bonus_amount - quote.tier_reward_total),
                          { fractionDigits: 2 },
                        ),
                      })
                    }}
                  </template>
                  <template v-if="!nextTier && !quote.tier_reward_total">
                    {{ t('rechargeBonus.noTier') }}
                  </template>
                </p>
              </div>

              <div v-if="extraWindows.length" class="mt-6">
                <button
                  v-for="window in extraWindows"
                  :key="window.starts_at"
                  type="button"
                  class="flex w-full items-center gap-3 rounded-control border border-gray-200 p-3 text-left dark:border-dark-600"
                  :class="
                    selectedExtraWindow?.starts_at === window.starts_at
                      ? 'border-primary-600 bg-primary-50 dark:bg-primary-500/8'
                      : 'bg-gray-50 dark:bg-dark-900'
                  "
                  :aria-pressed="selectedExtraWindow?.starts_at === window.starts_at"
                  :disabled="
                    !canPreview || status !== 'ongoing' || Date.parse(window.ends_at) <= serverNow
                  "
                  data-testid="activity-extra-option"
                  @click="
                    selectedExtraStart =
                      selectedExtraWindow?.starts_at === window.starts_at ? null : window.starts_at
                  "
                >
                  <span
                    class="flex h-9 w-9 shrink-0 items-center justify-center rounded-compact border border-gray-200 bg-white text-sm font-semibold text-primary-600 dark:border-dark-600 dark:bg-dark-950 dark:text-primary-500"
                    aria-hidden="true"
                    >{{ new Date(window.starts_at).getDate() }}</span
                  >
                  <span class="min-w-0 flex-1">
                    <span class="block text-sm font-medium"
                      >{{ t('rechargeBonus.timeGift') }}
                      <span class="text-primary-600 dark:text-primary-500"
                        >+{{ window.bonus_percent }}%</span
                      ></span
                    >
                    <span class="block text-xs leading-5 text-gray-500 dark:text-dark-300"
                      >{{ formatDate(window.starts_at) }} ~ {{ formatDate(window.ends_at) }}</span
                    >
                  </span>
                  <span class="btn btn-secondary btn-sm shrink-0 whitespace-nowrap">{{
                    selectedExtraWindow?.starts_at === window.starts_at
                      ? t('rechargeBonus.addedToPreview')
                      : t('rechargeBonus.addToPreview')
                  }}</span>
                </button>
                <p class="input-hint mt-2" data-testid="activity-extra-preview-hint">{{
                  t('rechargeBonus.extraPreviewHint')
                }}</p>
              </div>

              <div
                v-if="sortedTiers.length"
                class="mt-6 border-t border-gray-200 pt-5 dark:border-dark-600"
              >
                <div class="flex flex-wrap items-baseline justify-between gap-2">
                  <p class="text-sm font-medium">{{ t('rechargeBonus.tierLadder') }}</p>
                  <p class="text-xs text-gray-500 dark:text-dark-300">{{
                    t('rechargeBonus.tierLadderHint')
                  }}</p>
                </div>
                <ActivityTierLadder
                  class="mt-4"
                  :tiers="sortedTiers"
                  :cumulative-before="cumulativeBefore"
                  :next-minimum="nextTier?.min_amount ?? 0"
                  :selected-minimum="canPreview ? quote.min_amount : 0"
                  :disabled="!canPreview"
                  @select="selectTier"
                />
              </div>
            </div>

            <aside
              class="h-fit rounded-surface border border-gray-200 bg-gray-50 p-5 dark:border-dark-600 dark:bg-dark-900 lg:sticky lg:top-[calc(var(--header-h)+1rem)]"
            >
              <RechargeBonusBreakdown v-if="canPreview" :quote="quote" estimated />
              <RouterLink
                :to="checkoutLink"
                class="btn btn-primary mt-4 w-full"
                :class="{
                  'pointer-events-none': status !== 'ongoing' || !validPurchase || !canPreview,
                }"
                :aria-disabled="status !== 'ongoing' || !validPurchase || !canPreview"
                :tabindex="status !== 'ongoing' || !validPurchase || !canPreview ? -1 : undefined"
                data-testid="activity-recharge-button"
                >{{
                  auth.isAuthenticated
                    ? t('rechargeBonus.recharge')
                    : t('rechargeBonus.loginRecharge')
                }}<Icon name="arrowRight" size="sm"
              /></RouterLink>
              <p class="mt-3 text-xs leading-5 text-gray-500 dark:text-dark-300"
                >{{ t('rechargeBonus.feeHint') }} {{ t('rechargeBonus.serverTerms') }}</p
              >
              <p
                v-if="auth.isAuthenticated && canPreview"
                class="mt-3 border-t border-gray-200 pt-3 text-xs leading-5 text-gray-600 dark:border-dark-600 dark:text-dark-300"
                >{{ t('rechargeBonus.memberNote') }}</p
              >
            </aside>
          </div>
        </section>

        <section
          class="mt-5 rounded-surface border border-gray-200 bg-white p-6 dark:border-dark-600 dark:bg-dark-950 sm:p-8"
        >
          <h2 class="text-xl font-semibold">{{ t('rechargeBonus.rulesAndFaq') }}</h2>
          <div v-if="noticeHtml" class="markdown-body mt-4 text-sm" v-html="noticeHtml" />
          <div class="mt-6 grid gap-8 lg:grid-cols-2">
            <div>
              <h3 class="text-sm font-semibold">{{ t('rechargeBonus.rules') }}</h3>
              <p
                class="mt-3 whitespace-pre-line text-sm leading-7 text-gray-600 dark:text-dark-300"
                >{{ data.activity.rules || t('rechargeBonus.scheduleLocked') }}</p
              >
              <p class="mt-3 text-sm leading-7 text-gray-500 dark:text-dark-400">{{
                t('rechargeBonus.serverTerms')
              }}</p>
            </div>
            <div>
              <h3 class="text-sm font-semibold">{{ t('rechargeBonus.faqs') }}</h3>
              <div class="mt-3 divide-y divide-gray-200 dark:divide-dark-600">
                <details
                  v-for="(faq, index) in data.activity.faqs"
                  :key="faq.id || index"
                  class="py-3"
                >
                  <summary class="cursor-pointer text-sm font-medium">{{ faq.question }}</summary>
                  <p
                    class="mt-2 whitespace-pre-line text-sm leading-7 text-gray-600 dark:text-dark-300"
                    >{{ faq.answer }}</p
                  >
                </details>
                <p
                  v-if="!data.activity.faqs.length"
                  class="text-sm leading-7 text-gray-600 dark:text-dark-300"
                  >{{ t('rechargeBonus.singleOrderHint') }}</p
                >
              </div>
            </div>
          </div>
        </section>
      </main>

      <div
        v-if="status === 'ongoing' && validPurchase && canPreview"
        class="fixed inset-x-0 bottom-0 z-header flex items-center justify-between gap-4 border-t border-gray-200 bg-white p-4 dark:border-dark-600 dark:bg-dark-950 md:hidden"
      >
        <div>
          <p class="text-xs text-gray-500 dark:text-dark-300">{{
            t('rechargeBonus.estimatedTotal')
          }}</p>
          <p class="text-lg font-semibold tabular-nums">{{
            formatBalanceAmount(quote.amount, { fractionDigits: 2 })
          }}</p>
        </div>
        <RouterLink :to="checkoutLink" class="btn btn-primary">{{
          auth.isAuthenticated ? t('rechargeBonus.recharge') : t('rechargeBonus.loginRecharge')
        }}</RouterLink>
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useNow } from '@vueuse/core'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import AppHeader from '@/components/layout/AppHeader.vue'
import ContentSkeleton from '@/components/common/ContentSkeleton.vue'
import Icon from '@/components/icons/Icon.vue'
import RechargeBonusBreakdown from '@/components/payment/RechargeBonusBreakdown.vue'
import ActivityTierLadder from './ActivityTierLadder.vue'
import { paymentAPI } from '@/api/payment'
import type { PublicRechargeActivity, RechargeBonusProgress } from '@/api/payment.types'
import { useAuthStore } from '@/stores/auth'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import { quoteRechargeBonus, roundRechargeAmount } from '@/utils/payment/rechargeBonus'

// RechargeActivityView.vue presents the configured recharge campaign to guests
// and authenticated purchasers. A brand-tinted hero states the highest available
// reward, and one calculator card holds the amount field, cumulative progress,
// the optional scheduled extra, and the tier ladder that replaces the repeated
// tier lists. Successful campaign purchases determine the highest fixed reward,
// with already delivered tier gifts subtracted from the next purchase's gift.
// Scheduled extras apply to that purchase's base credit independently.
//
// Guests preview from zero and retain their amount through the existing login
// route. Each scheduled extra starts excluded from the calculator until the
// viewer selects its dated option, including previews for a future day within
// the campaign. Activity and personal progress lookups follow the current route,
// language, and authenticated buyer; watcher cleanup cancels both requests before
// another viewer or component teardown can publish a late response. A logged-in
// preview requires progress for the published campaign; failed or mismatched
// observations leave the public offer visible with a retry action. The server
// clock controls activity and interval expiry, retaining the public response's
// receipt time while personal reads finish.

// Page state owns the public offer, personal campaign observation, editable
// payment principal, and server-clock offset used by preview and retry controls.
const route = useRoute()
const { t, locale } = useI18n()
const auth = useAuthStore()
const { formatBalanceAmount } = useBalanceDisplay()
const data = ref<PublicRechargeActivity | null>(null)
const loading = ref(true)
const amount = ref(100)
const clockOffset = ref(0)
const progress = ref<RechargeBonusProgress | null>(null)
const progressFailed = ref(false)
const retryGeneration = ref(0)
// selectedExtraStart identifies the manually included extra interval. The choice
// survives translated reads of the same campaign and clears for another campaign.
const selectedExtraStart = ref<string | null>(null)
const now = useNow({ interval: 1000 })
const serverNow = computed(() => now.value.getTime() + clockOffset.value)

// quickCredits lists the credited increments offered beside the amount field.
// Each adds to the current payment's base credit so a buyer can combine them.
const quickCredits = [100, 500]

// amountActivitySlug identifies the activity whose successful lookup initialized
// the buyer's editable amount before subsequent language refreshes retain it.
let amountActivitySlug: string | undefined
// extraPreviewCampaignID keeps manual date selections attached to one campaign,
// preventing an identical interval in a replacement campaign from being opted in.
let extraPreviewCampaignID: string | undefined

// Replacing or unmounting the watcher cancels both reads before a late result
// can replace the next viewer's content or campaign progress. Successful public
// reads remain visible when personal progress fails, but that failure gates
// monetary previews until a retry supplies the matching campaign observation.
// Language refreshes retain the buyer's amount while activity changes initialize it.
watch(
  [
    () => String(route.params.slug ?? ''),
    locale,
    () => (auth.isAuthenticated ? (auth.user?.id ?? -1) : null),
    retryGeneration,
  ],
  async ([slug], _previous, onCleanup) => {
    const controller = new AbortController()
    onCleanup(() => controller.abort())
    loading.value = true
    data.value = null
    progress.value = null
    progressFailed.value = false
    try {
      const authenticated = auth.isAuthenticated
      const [activityResult, checkoutResult] = await Promise.allSettled([
        // Personal progress may finish later; pair the public clock with its own
        // receipt time so that waiting for another request cannot extend activity eligibility.
        paymentAPI.getRechargeActivity(slug, controller.signal).then((response) => ({
          response,
          receivedAt: Date.now(),
        })),
        authenticated ? paymentAPI.getCheckoutInfo(controller.signal) : null,
      ])
      if (controller.signal.aborted) return
      if (activityResult.status === 'rejected') throw activityResult.reason
      const { response, receivedAt } = activityResult.value
      const serverTime = Date.parse(response.data.server_time)
      if (!Number.isFinite(serverTime)) throw new Error('Invalid activity clock')
      data.value = response.data
      if (extraPreviewCampaignID !== response.data.activity.campaign_id) {
        selectedExtraStart.value = null
        extraPreviewCampaignID = response.data.activity.campaign_id
      }
      clockOffset.value = serverTime - receivedAt
      if (authenticated) {
        const personalProgress =
          checkoutResult.status === 'fulfilled'
            ? checkoutResult.value?.data.recharge_bonus_progress
            : undefined
        if (
          personalProgress &&
          personalProgress.campaign_id === response.data.activity.campaign_id &&
          Number.isFinite(personalProgress.cumulative_amount) &&
          personalProgress.cumulative_amount >= 0 &&
          Number.isFinite(personalProgress.tier_reward_total) &&
          personalProgress.tier_reward_total >= 0
        ) {
          progress.value = personalProgress
        } else {
          // Missing or mismatched personal progress cannot be treated as an unused
          // reward allowance; suppressing the preview prevents advertising the same gift twice.
          progressFailed.value = true
        }
      }
      if (slug !== amountActivitySlug) {
        amount.value = Math.max(
          response.data.min_amount || 1,
          principalForCredit(response.data.tiers[0]?.min_amount || 100),
        )
        amountActivitySlug = slug
      }
    } catch {
      // Failed public lookups render the same unavailable state as unpublished
      // activities; no stale offer remains visible after a route change or error.
      if (!controller.signal.aborted) data.value = null
    } finally {
      if (!controller.signal.aborted) loading.value = false
    }
  },
  { immediate: true },
)

// Derived purchase quantities combine the current server observation with the
// selected buyer's successfully paid campaign progress and editable payment principal.
const canPreview = computed(() => !auth.isAuthenticated || progress.value !== null)
const cumulativeBefore = computed(() => progress.value?.cumulative_amount ?? 0)
const creditMultiplier = computed(() => {
  const multiplier = data.value?.multiplier ?? 1
  return Number.isFinite(multiplier) && multiplier > 0 ? multiplier : 1
})
const validAmount = computed(() =>
  Number.isFinite(amount.value) && amount.value > 0 ? amount.value : 0,
)
const sortedTiers = computed(() =>
  [...(data.value?.tiers ?? [])].sort((a, b) => a.min_amount - b.min_amount),
)
const paymentCurrency = computed(() => {
  const currencies = [
    ...new Set(
      Object.values(data.value?.methods ?? {})
        .map((method) => method.currency)
        .filter(Boolean),
    ),
  ]
  return currencies.length === 1 ? currencies[0] : ''
})
// Administrator Markdown is sanitized before rendering promotional descriptions,
// matching the purchase page's content handling while keeping both previews consistent.
const noticeHtml = computed(() =>
  DOMPurify.sanitize(marked.parse(data.value?.notice ?? '', { async: false })),
)
// Unbounded sliders extend beyond the balance needed to reach the highest remaining tier.
const sliderMaximum = computed(
  () =>
    data.value?.max_amount ||
    Math.max(
      1000,
      ...sortedTiers.value.map(
        (tier) =>
          (Math.max(0, tier.min_amount - cumulativeBefore.value) / creditMultiplier.value) * 1.5,
      ),
    ),
)
const validPurchase = computed(
  () =>
    validAmount.value >= (data.value?.min_amount || 1) &&
    (!data.value?.max_amount || validAmount.value <= data.value.max_amount),
)
// Enabled intervals become separate preview choices; starts are unique because
// saved enabled intervals cannot overlap. Expired choices stop contributing as
// the server-adjusted clock advances, and overall activity dates still gate gifts.
const extraWindows = computed(() =>
  (data.value?.activity.time_windows ?? []).filter((window) => window.enabled),
)
const selectedExtraWindow = computed(() =>
  status.value === 'ongoing'
    ? extraWindows.value.find(
        (window) =>
          window.starts_at === selectedExtraStart.value &&
          Date.parse(window.ends_at) > serverNow.value,
      )
    : undefined,
)

// Manual previews evaluate the selected extra at its inclusive start when that
// date is still ahead. The ordinary clock continues driving expiry and purchase
// availability; successful payment independently determines the actual reward.
const quote = computed(() => {
  const window = selectedExtraWindow.value
  const activity = data.value?.activity
  return quoteRechargeBonus(
    {
      recharge_bonus_tiers: data.value?.tiers,
      recharge_bonus_activity: activity
        ? { ...activity, time_windows: window ? [window] : [] }
        : undefined,
    },
    validAmount.value,
    data.value?.multiplier,
    window ? Math.max(serverNow.value, Date.parse(window.starts_at)) : serverNow.value,
    progress.value ?? undefined,
  )
})
const nextTier = computed(() =>
  sortedTiers.value.find((tier) => tier.min_amount > quote.value.cumulative_amount),
)
const highestReward = computed(() =>
  Math.max(0, ...sortedTiers.value.map((tier) => tier.bonus_amount)),
)
// highestTierMinimum is the cumulative credit behind highestReward, shown as the
// caption under the hero amount. An activity without tiers reports zero.
const highestTierMinimum = computed(
  () =>
    sortedTiers.value.reduce(
      (best, tier) => (tier.bonus_amount > best.bonus_amount ? tier : best),
      { min_amount: 0, bonus_amount: 0 },
    ).min_amount,
)
// progressPercent places the buyer's confirmed credit between the first and last
// threshold, so the bar shows how far the next reward is. A campaign with a single
// threshold or no thresholds reports zero to avoid dividing by zero.
const progressPercent = computed(() => {
  const tiers = sortedTiers.value
  if (tiers.length < 2) return 0
  const first = tiers[0]!.min_amount
  const last = tiers[tiers.length - 1]!.min_amount
  if (last <= first) return 0
  const ratio = (quote.value.cumulative_amount - first) / (last - first)
  return Math.min(100, Math.max(0, ratio * 100))
})
const status = computed(() => {
  const activity = data.value?.activity
  if (activity?.starts_at && serverNow.value < Date.parse(activity.starts_at)) return 'upcoming'
  if (activity?.ends_at && serverNow.value >= Date.parse(activity.ends_at)) return 'ended'
  return 'ongoing'
})
const periodLabel = computed(
  () =>
    [data.value?.activity.starts_at, data.value?.activity.ends_at]
      .filter((value): value is string => Boolean(value))
      .map(formatDate)
      .join(' ~ ') || t('rechargeBonus.permanent'),
)
// countdownCells separates the remaining time into day, hour, and minute cells
// for the hero panel. Seconds are omitted because the display refreshes each
// second anyway and the extra cell adds noise. An ended or open-ended activity
// returns no cells and hides the panel.
const countdownCells = computed(() => {
  const endpoint =
    status.value === 'upcoming' ? data.value?.activity.starts_at : data.value?.activity.ends_at
  if (!endpoint || status.value === 'ended') return []
  // Countdown seconds come from the server-adjusted clock rather than browser zone.
  const seconds = Math.max(0, Math.floor((Date.parse(endpoint) - serverNow.value) / 1000))
  return [
    { value: String(Math.floor(seconds / 86400)).padStart(2, '0'), label: t('rechargeBonus.days') },
    {
      value: String(Math.floor((seconds % 86400) / 3600)).padStart(2, '0'),
      label: t('rechargeBonus.hours'),
    },
    {
      value: String(Math.floor((seconds % 3600) / 60)).padStart(2, '0'),
      label: t('rechargeBonus.minutes'),
    },
  ]
})
const checkoutLink = computed(() => {
  const purchase = `/purchase?amount=${validAmount.value.toFixed(2)}`
  return auth.isAuthenticated ? purchase : { path: '/login', query: { redirect: purchase } }
})

/** isReached reports whether confirmed purchases already covered a threshold. */
function isReached(minimum: number): boolean {
  return minimum <= cumulativeBefore.value
}

/** isNext reports whether a threshold is the first one still ahead of the buyer. */
function isNext(minimum: number): boolean {
  return nextTier.value?.min_amount === minimum
}

// formatDate renders an activity instant in its explicitly configured display timezone.
function formatDate(value: string): string {
  return new Intl.DateTimeFormat(locale.value, {
    timeZone: data.value?.activity.timezone || 'UTC',
    dateStyle: 'medium',
    timeStyle: 'short',
  }).format(new Date(value))
}

// addCredit raises the payment principal by a credited increment, keeping the
// quick-add buttons additive and within the activity's purchase limits. It uses
// the current preview's base credit rather than the raw input so a multiplier
// converts the increment into the same paid quantity as manual entry.
function addCredit(credit: number): void {
  const target = quote.value.base_amount + credit
  amount.value = clampAmount(principalForCredit(target))
}

// clampAmount keeps a computed principal inside the activity's purchase limits.
// An unbounded activity still applies its minimum.
function clampAmount(principal: number): number {
  const minimum = data.value?.min_amount || 1
  const maximum = data.value?.max_amount || 0
  if (principal < minimum) return minimum
  return maximum > 0 && principal > maximum ? maximum : principal
}

// selectTier fills the payment principal needed to reach a cumulative credit tier.
// The principal uses payment cents and reaches the selected credited threshold
// after conversion. Purchase minimums apply, and channel maximums still gate
// checkout when the selected target requires a larger payment.
function selectTier(minimum: number): void {
  const creditRequired = roundRechargeAmount(Math.max(0, minimum - cumulativeBefore.value))
  amount.value = clampAmount(principalForCredit(creditRequired))
}

// principalForCredit converts a credited balance target into payment cents for
// the selected recharge multiplier. It rounds the direct conversion to cents,
// then checks the shared calculator's base credit because paid and credited
// quantities round separately. A rounded result below the target adds one
// payment cent before returning the principal. This operation handles both
// initial amounts and tier selections; their callers apply channel minimums
// and retain maximum-limit validation before checkout.
function principalForCredit(creditRequired: number): number {
  // Payment inputs accept hundredths, so rounding the converted amount keeps
  // tier shortcuts and initial values within the input's declared monetary step.
  const principal = roundRechargeAmount(creditRequired / creditMultiplier.value)
  const base = quoteRechargeBonus({}, principal, creditMultiplier.value).base_amount
  return base < creditRequired ? roundRechargeAmount(principal + 0.01) : principal
}
</script>
