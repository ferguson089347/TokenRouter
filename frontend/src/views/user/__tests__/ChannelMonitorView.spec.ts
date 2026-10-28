import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import ChannelMonitorView from '../ChannelMonitorView.vue'

// ChannelMonitorView.spec.ts verifies polling ownership and stale-result rejection
// using deferred API responses. Layout and selectors are replaced with small
// shells while the real page owns its requests, state publication, and cleanup.

// Shared API mocks keep upstream calls outside these page lifecycle fixtures;
// each endpoint returns the same selected generation unless a test delays it.
const api = vi.hoisted(() => ({
  getSnapshot: vi.fn(),
  getDimensions: vi.fn(),
  getMatrix: vi.fn(),
  getModels: vi.fn(),
  getErrors: vi.fn(),
  getUsers: vi.fn(),
  getConfig: vi.fn(),
  updateConfig: vi.fn(),
}))
vi.mock('@/api/channelMonitor', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/channelMonitor')>()),
  ...api,
}))
vi.mock('@/components/layout/AppLayout.vue', () => ({
  default: { template: '<main><slot /></main>' },
}))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string) => key }),
}))

/** summary builds the complete page summary needed by these lifecycle cases. */
function summary(state: 'healthy' | 'critical') {
  return {
    config: { enabled: true, refresh_interval_seconds: 300 },
    coverage: {
      computed_at: '2026-10-28T00:00:00Z',
      aggregation_lag_seconds: 0,
      coverage_complete: true,
    },
    health: { overall: state, cache: state, score: state === 'healthy' ? 100 : 0 },
    metrics: { success_rate: 1, cache_rate: 0, ttft: { p50_ms: 100 } },
  }
}

/** render mounts selectors as event-capable stubs with page-owned request state. */
function render() {
  return mount(ChannelMonitorView, {
    global: { stubs: { Select: true, BaseDialog: true, Toggle: true } },
  })
}

// wrapper owns the currently mounted fixture and is always disposed after the
// case, before real timers are restored, so leaked refreshes remain observable.
let wrapper: ReturnType<typeof render> | undefined
beforeEach(() => {
  vi.useFakeTimers()
  vi.clearAllMocks()
  api.getDimensions.mockResolvedValue({ platforms: [], groups: [], models: [] })
  api.getMatrix.mockResolvedValue({ items: [] })
  api.getModels.mockResolvedValue({ items: [] })
  api.getErrors.mockResolvedValue({ items: [] })
  api.getUsers.mockResolvedValue({ items: [] })
})
afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  vi.useRealTimers()
})

describe('ChannelMonitorView', () => {
  // A transport resolves an aborted older selection after a newer selection.
  // The visible health must remain from the current request generation.
  it('rejects stale responses after filters change', async () => {
    let resolveOld!: (value: ReturnType<typeof summary>) => void
    api.getSnapshot.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolveOld = resolve
        }),
    )
    api.getSnapshot.mockResolvedValue(summary('healthy'))
    wrapper = render()
    const firstSignal = api.getSnapshot.mock.calls[0][2] as AbortSignal
    wrapper.findAllComponents({ name: 'Select' })[0].vm.$emit('update:modelValue', '24h')
    await flushPromises()
    expect(firstSignal.aborted).toBe(true)
    expect(wrapper.text()).toContain('monitor.states.healthy')
    resolveOld(summary('critical'))
    await flushPromises()
    expect(wrapper.text()).not.toContain('monitor.states.critical 0')
    expect(api.getSnapshot.mock.calls[1][0].range).toBe('24h')
  })

  // A successful load installs one periodic refresh owned by its mounted page.
  // Unmount must cancel that request generation and remove the pending timer.
  it('releases requests and the refresh timer on unmount', async () => {
    api.getSnapshot.mockResolvedValue(summary('healthy'))
    wrapper = render()
    await flushPromises()
    const signal = api.getSnapshot.mock.calls[0][2] as AbortSignal
    expect(vi.getTimerCount()).toBe(1)
    wrapper.unmount()
    wrapper = undefined
    expect(signal.aborted).toBe(true)
    expect(vi.getTimerCount()).toBe(0)
  })
})
