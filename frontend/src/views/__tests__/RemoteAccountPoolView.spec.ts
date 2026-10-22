import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import RemoteAccountPoolView from '../RemoteAccountPoolView.vue'
import Select from '@/components/common/Select.vue'
import Pagination from '@/components/common/Pagination.vue'
import UsageProgressBar from '@/components/provider/UsageProgressBar.vue'

// RemoteAccountPoolView.spec.ts verifies the user's directory access and snapshot
// presentation. Scripted API responses reproduce retained snapshots and overlapping
// reads, protecting local group display and request ownership while confirming that
// listing accounts never requires administrator configuration requests.

// Shared API mocks distinguish user directory reads from administrator settings and source-directory operations throughout these page scenarios.
const { api } = vi.hoisted(() => ({
  api: {
    getSettings: vi.fn(),
    saveSettings: vi.fn(),
    sourceGroups: vi.fn(),
    options: vi.fn(),
    list: vi.fn(),
  },
}))
vi.mock('@/components/layout/AppLayout.vue', () => ({
  default: { template: '<main><slot /></main>' },
}))
vi.mock('@/api/admin/remoteAccountPool', () => ({ remoteAccountPoolAPI: api }))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string) => key, locale: { value: 'en' } }),
}))

// Snapshot fixtures contain one visible account with a current local group multiplier and an expired remote session warning for users.
const snapshot = {
  stale: true,
  auth_invalid: true,
  snapshot_at: '2026-10-19T00:00:00Z',
}
const result = {
  ...snapshot,
  items: [
    {
      id: 'AC-LOCALREFERENCE',
      display_name: 'Cached account',
      plan_type: 'pro',
      plan_label: 'Pro',
      usage_windows: [
        { key: '5h', label: '5h', utilization: 4 },
        { key: '7d', label: '7d', utilization: 92 },
      ],
      groups: [{ id: 100, name: 'Local group', rate_multiplier: 2.5 }],
    },
  ],
  total: 1,
}
let wrapper: ReturnType<typeof render> | undefined

/** render mounts visible directory table slots and the standard filter layout. */
function render() {
  return mount(RemoteAccountPoolView, {
    global: {
      stubs: {
        TablePageLayout: {
          template:
            '<section><slot name="filters" /><slot name="table" /><slot name="pagination" /></section>',
        },
        DataTable: {
          props: ['data'],
          template:
            '<div><div v-for="row in data" :key="row.id"><slot name="cell-display_name" :row="row" /><slot name="cell-platform" :row="row" /><slot name="cell-groups" :row="row" /><slot name="cell-rate_multiplier" :row="row" /><slot name="cell-usage_windows" :row="row" /></div></div>',
        },
        Icon: true,
        Select: true,
        Pagination: true,
      },
    },
  })
}
beforeEach(() => {
  vi.clearAllMocks()
  api.options.mockResolvedValue({
    ...snapshot,
    groups: [{ id: 100, name: 'Local group', rate_multiplier: 2.5 }],
    plan_types: [
      { value: 'pro', label: 'Pro' },
      { value: 'free', label: 'Free' },
    ],
  })
  api.list.mockResolvedValue(result)
})
afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
})
describe('RemoteAccountPoolView', () => {
  // Users can inspect a retained snapshot after remote credentials expire. The page displays local groups and rates and reads no administrator configuration.
  it('uses user APIs and displays the retained snapshot with local rates', async () => {
    wrapper = render()
    await flushPromises()
    expect(api.getSettings).not.toHaveBeenCalled()
    expect(api.sourceGroups).not.toHaveBeenCalled()
    expect(api.options).toHaveBeenCalledWith(expect.any(AbortSignal))
    expect(api.list).toHaveBeenCalledWith(expect.any(Object), expect.any(AbortSignal))
    expect(wrapper.text()).toContain('remotePool.staleHint')
    expect(wrapper.text()).toContain('Cached account')
    expect(wrapper.text()).toContain('Local group')
    expect(wrapper.text()).toMatch(/2\.5\s*×/)
    expect(wrapper.find('input[type="password"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('AC-LOCALREFERENCE')
    const windows = wrapper.findAllComponents(UsageProgressBar)
    expect(windows.map((window) => window.props('utilization'))).toEqual([4, 92])
    expect(
      wrapper.findAll('[role="progressbar"]').map((window) => window.attributes('aria-valuenow')),
    ).toEqual(['4', '92'])
  })
  // Source measurements can be missing or exceed the displayed progress range.
  // Unknown usage has its own text, and bar geometry remains bounded while the
  // original percentage stays available to assistive technology.
  it('keeps missing usage distinct from zero and bounds progress values', async () => {
    api.list.mockResolvedValue({
      ...result,
      items: [
        {
          ...result.items[0],
          usage_windows: [
            { key: 'missing', label: '5h', utilization: null },
            { key: 'zero', label: '7d', utilization: 0 },
            { key: 'full', label: 'Other', utilization: 140 },
          ],
        },
      ],
    })
    wrapper = render()
    await flushPromises()
    expect(wrapper.text()).toContain('remotePool.usageUnknown')
    const windows = wrapper.findAll('[role="progressbar"]')
    expect(windows.map((window) => window.attributes('aria-valuenow'))).toEqual(['0', '100'])
    expect(windows[1].attributes('aria-valuetext')).toBe('140%')
  })
  // Two list requests complete in reverse order. The older aborted response cannot replace rows belonging to the latest selected filters.
  it('ignores an older completion after a subsequent directory request', async () => {
    wrapper = render()
    await flushPromises()
    let resolveOld!: (value: typeof result) => void
    api.list.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolveOld = resolve
        }),
    )
    await wrapper.find('form').trigger('submit')
    const signal = api.list.mock.calls.at(-1)![1] as AbortSignal
    api.list.mockResolvedValueOnce({
      ...result,
      items: [{ ...result.items[0], display_name: 'Current result' }],
    })
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(signal.aborted).toBe(true)
    resolveOld({
      ...result,
      items: [{ ...result.items[0], display_name: 'Old result' }],
    })
    await flushPromises()
    expect(wrapper.text()).toContain('Current result')
    expect(wrapper.text()).not.toContain('Old result')
  })
  // Initial reads use twenty rows. Paging and combined type, status, and local
  // group filters preserve the page size and restart at the first matching page.
  it('defaults to twenty rows and applies visible combined filters', async () => {
    api.list.mockResolvedValue({ ...result, total: 100 })
    wrapper = render()
    await flushPromises()
    expect(api.list).toHaveBeenLastCalledWith(
      expect.objectContaining({ page: 1, page_size: 20 }),
      expect.any(AbortSignal),
    )
    const pagination = wrapper.findComponent(Pagination)
    expect(pagination.props('pageSize')).toBe(20)
    pagination.vm.$emit('update:page', 3)
    await flushPromises()
    const selects = wrapper.findAllComponents(Select)
    expect(selects).toHaveLength(3)
    for (const [index, value] of [
      [0, 'pro'],
      [1, 'active'],
      [2, '100'],
    ] as const) {
      selects[index].vm.$emit('update:modelValue', value)
      selects[index].vm.$emit('change', value)
      await flushPromises()
    }
    expect(api.list).toHaveBeenLastCalledWith(
      expect.objectContaining({
        page: 1,
        page_size: 20,
        plan_type: 'pro',
        status: 'active',
        group_id: 100,
      }),
      expect.any(AbortSignal),
    )
    await wrapper
      .findAll('button')
      .find((button) => button.text().includes('remotePool.clear'))!
      .trigger('click')
    await flushPromises()
    expect(api.list).toHaveBeenLastCalledWith(
      expect.objectContaining({
        page: 1,
        plan_type: undefined,
        status: undefined,
        group_id: undefined,
      }),
      expect.any(AbortSignal),
    )
  })
})
