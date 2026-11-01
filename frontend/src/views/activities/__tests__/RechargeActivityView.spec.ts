import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useI18n } from 'vue-i18n'
import RechargeActivityView from '../RechargeActivityView.vue'
import type { PublicRechargeActivity } from '@/api/payment.types'
import { emptyRechargeBonusActivity } from '@/utils/payment/rechargeBonus'
import { useAuthStore } from '@/stores/auth'

// RechargeActivityView.spec.ts verifies campaign progress, fixed reward previews,
// and login return links through the published activity page. Real routing and
// gift breakdowns expose cumulative catch-up rewards, independent scheduled
// extras, failed personal reads, and request replacement, while API/store
// replacements isolate payment fulfillment and persisted sessions from this fixture.

// Public response spies and deterministic balance formatting expose cumulative
// purchase quantities through the rendered page for guest and authenticated buyers.
const { getRechargeActivity, getCheckoutInfo } = vi.hoisted(() => ({
  getRechargeActivity: vi.fn(),
  getCheckoutInfo: vi.fn(),
}))
vi.mock('@/api/payment', () => ({ paymentAPI: { getRechargeActivity, getCheckoutInfo } }))
vi.mock('@/stores/auth', async () => {
  const { reactive } = await import('vue')
  const auth = reactive({ isAuthenticated: false, user: null as { id: number } | null })
  return { useAuthStore: () => auth }
})
vi.mock('@/stores', () => ({ useAppStore: () => ({ siteName: 'TokenRouter' }) }))
vi.mock('@/composables/useBalanceDisplay', () => ({
  useBalanceDisplay: () => ({ formatBalanceAmount: (value: number) => value.toFixed(2) }),
}))
vi.mock('vue-i18n', async (importOriginal) => {
  const { ref } = await import('vue')
  const locale = ref('en')
  const t = vi.fn((key: string) => key)
  return {
    ...(await importOriginal<typeof import('vue-i18n')>()),
    useI18n: () => ({ t, locale }),
  }
})

// Activity timestamps use UTC and a server clock inside the extra gift interval.
const response: PublicRechargeActivity = {
  activity: {
    ...emptyRechargeBonusActivity(),
    page_enabled: true,
    campaign_id: 'double11-2026',
    slug: 'double11',
    title: 'Recharge event',
    starts_at: '2026-11-01T00:00:00Z',
    ends_at: '2026-11-12T00:00:00Z',
    time_windows: [
      {
        enabled: true,
        starts_at: '2026-11-11T00:00:00Z',
        ends_at: '2026-11-12T00:00:00Z',
        bonus_percent: 5,
      },
    ],
  },
  tiers: [
    { min_amount: 100, bonus_amount: 11 },
    { min_amount: 300, bonus_amount: 33 },
    { min_amount: 500, bonus_amount: 66 },
    { min_amount: 1000, bonus_amount: 121 },
    { min_amount: 3000, bonus_amount: 231 },
    { min_amount: 5000, bonus_amount: 451 },
    { min_amount: 10000, bonus_amount: 1111 },
  ],
  notice: '',
  multiplier: 1,
  methods: { card: { currency: 'USD', fee_rate: 0, fee_fixed: 0 } },
  min_amount: 1,
  max_amount: 10000,
  server_time: '2026-11-11T12:00:00Z',
}

// mountActivity installs real route parameters before mounting the public page.
// Callers await API promises before interacting and unmount the returned wrapper
// to release the route watcher and its VueUse clock timer.
async function mountActivity() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/activities/:slug', component: RechargeActivityView },
      { path: '/login', component: { template: '<div />' } },
    ],
  })
  await router.push('/activities/double11')
  const wrapper = mount(RechargeActivityView, {
    global: { plugins: [router], stubs: { AppHeader: true, Icon: true } },
  })
  await flushPromises()
  return { wrapper, router }
}

beforeEach(() => {
  vi.clearAllMocks()
  useI18n().locale.value = 'en'
  Object.assign(useAuthStore(), { isAuthenticated: false, user: null })
  getRechargeActivity.mockResolvedValue({ data: structuredClone(response) })
  getCheckoutInfo.mockResolvedValue({
    data: {
      recharge_bonus_progress: {
        campaign_id: 'double11-2026',
        cumulative_amount: 100,
        tier_reward_total: 11,
      },
    },
  })
})

describe('RechargeActivityView', () => {
  // A guest starts with fixed rewards and manually adds or removes a dated extra.
  // That choice changes the estimate while the login link retains only the purchase amount.
  it('requires a manual extra selection and retains the amount through login', async () => {
    const { wrapper } = await mountActivity()
    await wrapper.get('[data-testid="activity-recharge-amount"]').setValue(1000)
    const extra = wrapper.get('[data-testid="activity-extra-option"]')
    expect(extra.attributes('aria-pressed')).toBe('false')
    expect(wrapper.get('[data-testid="recharge-bonus-total"]').text()).toBe('1121.00')
    expect(wrapper.find('[data-testid="recharge-bonus-time-gift"]').exists()).toBe(false)
    await extra.trigger('click')
    expect(extra.attributes('aria-pressed')).toBe('true')
    expect(wrapper.get('[data-testid="recharge-bonus-total"]').text()).toBe('1171.00')
    expect(wrapper.get('[data-testid="recharge-bonus-time-gift"]').text()).toBe('+50.00')
    await extra.trigger('click')
    expect(extra.attributes('aria-pressed')).toBe('false')
    expect(wrapper.get('[data-testid="recharge-bonus-total"]').text()).toBe('1121.00')
    expect(wrapper.get('[data-testid="recharge-bonus-tier-gift"]').text()).toBe('+121.00')
    expect(wrapper.get('[data-testid="activity-guest-progress-hint"]').exists()).toBe(true)
    expect(getCheckoutInfo).not.toHaveBeenCalled()
    const link = wrapper.get('[data-testid="activity-recharge-button"]').attributes('href')
    expect(decodeURIComponent(link)).toContain('/purchase?amount=1000.00')
    expect(link).toContain('/login')
    wrapper.unmount()
  })

  // A buyer has already received eleven credits after one hundred successful campaign credits.
  // Paying another two hundred adds twenty-two tier credits and ten independent scheduled credits.
  // The next-tier hint compares the 500 tier with the current projected 300 tier.
  it('subtracts previously delivered rewards from the next cumulative tier', async () => {
    Object.assign(useAuthStore(), { isAuthenticated: true, user: { id: 7 } })
    const { wrapper } = await mountActivity()
    await wrapper.get('[data-testid="activity-recharge-amount"]').setValue(200)
    await wrapper.get('[data-testid="activity-extra-option"]').trigger('click')
    expect(wrapper.get('[data-testid="recharge-bonus-cumulative-before"]').text()).toBe('100.00')
    expect(wrapper.get('[data-testid="recharge-bonus-previous-tier-reward"]').text()).toBe('11.00')
    expect(wrapper.get('[data-testid="recharge-bonus-cumulative-after"]').text()).toBe('300.00')
    expect(wrapper.get('[data-testid="recharge-bonus-tier-gift"]').text()).toBe('+22.00')
    expect(wrapper.get('[data-testid="recharge-bonus-time-gift"]').text()).toBe('+10.00')
    expect(wrapper.get('[data-testid="recharge-bonus-total"]').text()).toBe('232.00')
    expect(useI18n().t).toHaveBeenCalledWith('rechargeBonus.nextTier', {
      amount: '200.00',
      gift: '33.00',
    })
    expect(wrapper.text()).toContain('rechargeBonus.estimatedTotal')
    expect(wrapper.get('[data-testid="activity-recharge-button"]').attributes('href')).toBe(
      '/purchase?amount=200.00',
    )
    wrapper.unmount()
  })

  // A buyer has already received the campaign's maximum cumulative fixed reward.
  // Another payment still earns its scheduled percentage while tier gifts remain exhausted.
  it('keeps scheduled extras available after the fixed reward cap', async () => {
    Object.assign(useAuthStore(), { isAuthenticated: true, user: { id: 7 } })
    getCheckoutInfo.mockResolvedValueOnce({
      data: {
        recharge_bonus_progress: {
          campaign_id: 'double11-2026',
          cumulative_amount: 10000,
          tier_reward_total: 1111,
        },
      },
    })
    const { wrapper } = await mountActivity()
    await wrapper.get('[data-testid="activity-recharge-amount"]').setValue(1000)
    await wrapper.get('[data-testid="activity-extra-option"]').trigger('click')
    expect(wrapper.find('[data-testid="recharge-bonus-tier-gift"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="recharge-bonus-time-gift"]').text()).toBe('+50.00')
    expect(wrapper.get('[data-testid="recharge-bonus-total"]').text()).toBe('1050.00')
    wrapper.unmount()
  })

  // A recharge multiplier converts payment principal into the balance used by cumulative tiers.
  // Selecting a tier fills the remaining principal and keeps the cumulative cap visible above single-payment limits.
  it('selects tier targets using credited units and shows the cumulative cap', async () => {
    const converted = structuredClone(response)
    converted.multiplier = 2
    converted.max_amount = 200
    getRechargeActivity.mockResolvedValueOnce({ data: converted })
    const { wrapper } = await mountActivity()
    const tierButton = wrapper
      .findAll('[data-testid="activity-tier-node"]')
      .find((node) => node.text().includes('300.00'))!
    await tierButton.trigger('click')
    await wrapper.get('[data-testid="activity-extra-option"]').trigger('click')
    expect(
      (wrapper.get('[data-testid="activity-recharge-amount"]').element as HTMLInputElement).value,
    ).toBe('150')
    expect(wrapper.get('[data-testid="recharge-bonus-total"]').text()).toBe('348.00')
    expect(wrapper.get('[data-testid="activity-highest-gift"]').text()).toBe('+1111.00')
    wrapper.unmount()
  })

  // A tier target can require a payment principal with a repeating fractional cent.
  // Selecting it rounds to valid payment cents while still reaching the credited threshold.
  it('rounds tier selections to payment cents that qualify for the reward', async () => {
    const converted = structuredClone(response)
    converted.multiplier = 3
    getRechargeActivity.mockResolvedValueOnce({ data: converted })
    const { wrapper } = await mountActivity()
    const tierButton = wrapper
      .findAll('[data-testid="activity-tier-node"]')
      .find((node) => node.text().includes('100.00'))!
    await tierButton.trigger('click')
    expect(
      (wrapper.get('[data-testid="activity-recharge-amount"]').element as HTMLInputElement).value,
    ).toBe('33.34')
    expect(wrapper.get('[data-testid="recharge-bonus-tier-gift"]').text()).toBe('+11.00')
    expect(wrapper.get('[data-testid="recharge-bonus-cumulative-after"]').text()).toBe('100.02')
    wrapper.unmount()
  })

  // A personal progress failure leaves the published offer readable for an authenticated buyer.
  // Its retry restores the actual accumulated reward allowance before another monetary preview appears.
  it('gates personal previews until failed progress reads are retried', async () => {
    Object.assign(useAuthStore(), { isAuthenticated: true, user: { id: 7 } })
    getCheckoutInfo.mockRejectedValueOnce(new Error('unavailable'))
    const { wrapper } = await mountActivity()
    expect(wrapper.get('h1').text()).toBe('Recharge event')
    expect(wrapper.get('[data-testid="activity-progress-error"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="recharge-bonus-total"]').exists()).toBe(false)
    expect(
      wrapper.get('[data-testid="activity-recharge-button"]').attributes('aria-disabled'),
    ).toBe('true')
    await wrapper.get('[data-testid="activity-progress-error"] button').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="activity-recharge-amount"]').setValue(200)
    await wrapper.get('[data-testid="activity-extra-option"]').trigger('click')
    expect(wrapper.get('[data-testid="recharge-bonus-tier-gift"]').text()).toBe('+22.00')
    expect(wrapper.get('[data-testid="recharge-bonus-total"]').text()).toBe('232.00')
    wrapper.unmount()
  })

  // Published activity settings and checkout progress can observe different campaigns during configuration changes.
  // The page requests another observation instead of advertising rewards from an unrelated campaign balance.
  it('rejects progress belonging to another campaign', async () => {
    Object.assign(useAuthStore(), { isAuthenticated: true, user: { id: 7 } })
    getCheckoutInfo.mockResolvedValueOnce({
      data: {
        recharge_bonus_progress: {
          campaign_id: 'different-campaign',
          cumulative_amount: 100,
          tier_reward_total: 11,
        },
      },
    })
    const { wrapper } = await mountActivity()
    expect(wrapper.get('[data-testid="activity-progress-error"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="recharge-bonus-total"]').exists()).toBe(false)
    wrapper.unmount()
  })

  // An account switch can finish its personal progress lookup before the previous buyer's read.
  // A late result from the first account must leave the replacement buyer's accumulated amount visible.
  it('cancels personal reads when the authenticated buyer changes', async () => {
    Object.assign(useAuthStore(), { isAuthenticated: true, user: { id: 1 } })
    let complete: ((value: unknown) => void) | undefined
    getCheckoutInfo.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          complete = resolve
        }),
    )
    const { wrapper } = await mountActivity()
    const firstSignal = getCheckoutInfo.mock.calls[0][0] as AbortSignal
    Object.assign(useAuthStore(), { user: { id: 2 } })
    await flushPromises()
    expect(firstSignal.aborted).toBe(true)
    complete?.({
      data: {
        recharge_bonus_progress: {
          campaign_id: 'double11-2026',
          cumulative_amount: 10000,
          tier_reward_total: 1111,
        },
      },
    })
    await flushPromises()
    expect(wrapper.get('[data-testid="recharge-bonus-cumulative-before"]').text()).toBe('100.00')
    expect(wrapper.get('[data-testid="recharge-bonus-previous-tier-reward"]').text()).toBe('11.00')
    wrapper.unmount()
  })

  // Personal progress may finish after the activity ends while its public response arrived earlier.
  // Waiting for that second read must not move the campaign clock backward and reopen eligibility.
  it('keeps the public clock advancing while personal progress is pending', async () => {
    vi.useFakeTimers({ toFake: ['Date', 'setInterval', 'clearInterval'] })
    vi.setSystemTime(new Date('2026-11-11T12:00:00Z'))
    Object.assign(useAuthStore(), { isAuthenticated: true, user: { id: 7 } })
    const ending = structuredClone(response)
    ending.activity.ends_at = '2026-11-11T12:00:02Z'
    ending.activity.time_windows[0]!.ends_at = ending.activity.ends_at
    getRechargeActivity.mockResolvedValueOnce({ data: ending })
    let complete: ((value: unknown) => void) | undefined
    getCheckoutInfo.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          complete = resolve
        }),
    )
    const { wrapper } = await mountActivity()
    try {
      vi.advanceTimersByTime(10000)
      complete?.({
        data: {
          recharge_bonus_progress: {
            campaign_id: 'double11-2026',
            cumulative_amount: 100,
            tier_reward_total: 11,
          },
        },
      })
      await flushPromises()
      expect(
        wrapper.get('[data-testid="activity-recharge-button"]').attributes('aria-disabled'),
      ).toBe('true')
      expect(wrapper.text()).toContain('rechargeBonus.ended')
    } finally {
      wrapper.unmount()
      vi.useRealTimers()
    }
  })

  // A published activity whose start lies after server time remains readable.
  // Its purchase action stays disabled and the preview contains ordinary base credit.
  it('keeps an upcoming activity readable without offering an active gift', async () => {
    const upcoming = structuredClone(response)
    upcoming.activity.starts_at = '2026-11-12T00:00:00Z'
    upcoming.activity.ends_at = '2026-11-13T00:00:00Z'
    upcoming.activity.time_windows = []
    getRechargeActivity.mockResolvedValueOnce({ data: upcoming })
    const { wrapper } = await mountActivity()
    expect(
      wrapper.get('[data-testid="activity-recharge-button"]').attributes('aria-disabled'),
    ).toBe('true')
    expect(wrapper.get('[data-testid="recharge-bonus-total"]').text()).toBe('100.00')
    wrapper.unmount()
  })

  // A route change can finish its lookup while the previous activity request is pending.
  // Resolving that older request afterward must retain the newly selected activity title.
  it('ignores a late result for the preceding route', async () => {
    let complete: ((value: { data: PublicRechargeActivity }) => void) | undefined
    getRechargeActivity.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          complete = resolve
        }),
    )
    const { wrapper, router } = await mountActivity()
    const firstSignal = getRechargeActivity.mock.calls[0][1] as AbortSignal
    const next = structuredClone(response)
    next.activity.title = 'Next activity'
    getRechargeActivity.mockResolvedValueOnce({ data: next })
    await router.push('/activities/next')
    await flushPromises()
    expect(firstSignal.aborted).toBe(true)
    complete?.({ data: structuredClone(response) })
    await flushPromises()
    expect(wrapper.get('h1').text()).toBe('Next activity')
    wrapper.unmount()
  })

  // Repeated language changes can finish their activity lookups out of order.
  // The newest translation remains visible and retains the buyer's selected amount.
  it('refreshes translated content while retaining the purchase amount', async () => {
    const { wrapper } = await mountActivity()
    await wrapper.get('[data-testid="activity-recharge-amount"]').setValue(1000)
    await wrapper.get('[data-testid="activity-extra-option"]').trigger('click')
    let complete: ((value: { data: PublicRechargeActivity }) => void) | undefined
    getRechargeActivity.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          complete = resolve
        }),
    )
    useI18n().locale.value = 'zh-Hans'
    await flushPromises()
    const previousSignal = getRechargeActivity.mock.calls[1][1] as AbortSignal
    const translated = structuredClone(response)
    translated.activity.title = 'Translated activity'
    getRechargeActivity.mockResolvedValueOnce({ data: translated })
    useI18n().locale.value = 'en'
    await flushPromises()
    expect(previousSignal.aborted).toBe(true)
    expect(getRechargeActivity.mock.calls.map(([slug]) => slug)).toEqual([
      'double11',
      'double11',
      'double11',
    ])
    complete?.({ data: structuredClone(response) })
    await flushPromises()
    expect(wrapper.get('h1').text()).toBe('Translated activity')
    expect(wrapper.get('[data-testid="recharge-bonus-total"]').text()).toBe('1171.00')
    wrapper.unmount()
  })

  // An ongoing activity can advertise a later day's extra before that day arrives.
  // Selecting its dated option estimates that future reward while the ordinary campaign clock stays current.
  it('previews a future extra only after the viewer opts in', async () => {
    const beforeDouble11 = structuredClone(response)
    beforeDouble11.server_time = '2026-11-01T12:00:00Z'
    getRechargeActivity.mockResolvedValueOnce({ data: beforeDouble11 })
    const { wrapper } = await mountActivity()
    expect(wrapper.get('[data-testid="recharge-bonus-total"]').text()).toBe('111.00')
    const extra = wrapper.get('[data-testid="activity-extra-option"]')
    expect(extra.text()).toContain('Nov 11, 2026')
    await extra.trigger('click')
    expect(wrapper.get('[data-testid="recharge-bonus-time-gift"]').text()).toBe('+5.00')
    expect(wrapper.get('[data-testid="recharge-bonus-total"]').text()).toBe('116.00')
    expect(wrapper.get('[data-testid="activity-extra-preview-hint"]').text()).toBe(
      'rechargeBonus.extraPreviewHint',
    )
    expect(getRechargeActivity).toHaveBeenCalledOnce()
    expect(getCheckoutInfo).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  // The viewer can leave a selected extra open until its payment window expires.
  // Advancing the server-adjusted clock must remove that gift and disable its expired option.
  it('removes a manually selected extra when its interval ends', async () => {
    vi.useFakeTimers({ toFake: ['Date', 'setInterval', 'clearInterval'] })
    vi.setSystemTime(new Date(response.server_time))
    const ending = structuredClone(response)
    ending.activity.time_windows[0]!.ends_at = '2026-11-11T12:00:01Z'
    getRechargeActivity.mockResolvedValueOnce({ data: ending })
    const { wrapper } = await mountActivity()
    try {
      const extra = wrapper.get('[data-testid="activity-extra-option"]')
      await extra.trigger('click')
      expect(wrapper.get('[data-testid="recharge-bonus-total"]').text()).toBe('116.00')
      vi.advanceTimersByTime(2000)
      await flushPromises()
      expect(extra.attributes('aria-pressed')).toBe('false')
      expect(extra.attributes('disabled')).toBeDefined()
      expect(wrapper.find('[data-testid="recharge-bonus-time-gift"]').exists()).toBe(false)
      expect(wrapper.get('[data-testid="recharge-bonus-total"]').text()).toBe('111.00')
    } finally {
      wrapper.unmount()
      vi.useRealTimers()
    }
  })

  // Another campaign can reuse the same extra dates while replacing the public activity.
  // The earlier campaign's manual selection must not opt the viewer into the replacement estimate.
  it('clears the selected extra when the campaign changes', async () => {
    const { wrapper, router } = await mountActivity()
    await wrapper.get('[data-testid="activity-extra-option"]').trigger('click')
    expect(wrapper.get('[data-testid="recharge-bonus-total"]').text()).toBe('116.00')
    const replacement = structuredClone(response)
    replacement.activity.campaign_id = 'replacement-campaign'
    getRechargeActivity.mockResolvedValueOnce({ data: replacement })
    await router.push('/activities/replacement')
    await flushPromises()
    expect(wrapper.get('[data-testid="activity-extra-option"]').attributes('aria-pressed')).toBe(
      'false',
    )
    expect(wrapper.get('[data-testid="recharge-bonus-total"]').text()).toBe('111.00')
    wrapper.unmount()
  })

  // A language change can replace the first lookup before any activity has loaded.
  // The completed replacement still initializes a purchase amount meeting its minimum.
  it('initializes the amount when language changes during the first lookup', async () => {
    getRechargeActivity.mockImplementationOnce(() => new Promise(() => {}))
    const { wrapper } = await mountActivity()
    const translated = structuredClone(response)
    translated.min_amount = 500
    getRechargeActivity.mockResolvedValueOnce({ data: translated })
    useI18n().locale.value = 'zh-Hans'
    await flushPromises()
    expect(
      (wrapper.get('[data-testid="activity-recharge-amount"]').element as HTMLInputElement).value,
    ).toBe('500')
    expect(
      wrapper.get('[data-testid="activity-recharge-button"]').attributes('aria-disabled'),
    ).toBe('false')
    wrapper.unmount()
  })
})
