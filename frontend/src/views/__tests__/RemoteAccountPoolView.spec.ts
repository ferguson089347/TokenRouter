import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import RemoteAccountPoolView from '../RemoteAccountPoolView.vue'

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
    list: vi.fn()
  }
}))
vi.mock('@/components/layout/AppLayout.vue', () => ({
  default: { template: '<main><slot /></main>' }
}))
vi.mock('@/api/admin/remoteAccountPool', () => ({ remoteAccountPoolAPI: api }))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string) => key })
}))

// Snapshot fixtures contain one visible account with a current local group multiplier and an expired remote session warning for users.
const snapshot = { stale: true, auth_invalid: true, snapshot_at: '2026-10-19T00:00:00Z' }
const result = {
  ...snapshot,
  items: [
    {
      id: 1,
      display_name: 'Cached account',
      groups: [{ id: 100, name: 'Local group', rate_multiplier: 2.5 }]
    }
  ],
  total: 1
}
let wrapper: ReturnType<typeof render> | undefined

/** render mounts visible directory table slots and the standard filter layout. */
function render() {
  return mount(RemoteAccountPoolView, {
    global: {
      stubs: {
        TablePageLayout: {
          template:
            '<section><slot name="filters" /><slot name="table" /><slot name="pagination" /></section>'
        },
        DataTable: {
          props: ['data'],
          template:
            '<div><div v-for="row in data" :key="row.id"><slot name="cell-display_name" :row="row" /><slot name="cell-groups" :row="row" /><slot name="cell-rate_multiplier" :row="row" /></div></div>'
        },
        FilterDropdown: { template: '<div><slot /></div>' },
        FilterField: { template: '<div><slot /></div>' },
        Icon: true,
        Select: true,
        Pagination: true
      }
    }
  })
}
beforeEach(() => {
  vi.clearAllMocks()
  api.options.mockResolvedValue({ ...snapshot, groups: [], plan_types: [] })
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
    expect(wrapper.text()).toContain('2.5x')
    expect(wrapper.find('input[type="password"]').exists()).toBe(false)
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
        })
    )
    await wrapper.find('form').trigger('submit')
    const signal = api.list.mock.calls.at(-1)![1] as AbortSignal
    api.list.mockResolvedValueOnce({
      ...result,
      items: [{ ...result.items[0], display_name: 'Current result' }]
    })
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(signal.aborted).toBe(true)
    resolveOld({ ...result, items: [{ ...result.items[0], display_name: 'Old result' }] })
    await flushPromises()
    expect(wrapper.text()).toContain('Current result')
    expect(wrapper.text()).not.toContain('Old result')
  })
})
