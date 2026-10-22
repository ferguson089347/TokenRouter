import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ApiKeyDefaults from '../ApiKeyDefaults.vue'
import type { SettingsSaveTarget } from '@/composables/useSettingsSaveRegistry'

// ApiKeyDefaults.spec.ts exercises administrator routing drafts with controlled
// persistence and the shared save registration. Ordered writes, failed saves and
// edits made during a pending write retain the correct dirty state for review.

// Network and registry spies expose each panel's independently owned save target.
const mocks = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), groups: vi.fn(), register: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: { get: mocks.get, put: mocks.put } }))
vi.mock('@/api/admin/groups', () => ({ groupsAPI: { getAll: mocks.groups } }))
vi.mock('@/composables/useSettingsSaveRegistry', () => ({ useSettingsSaveTarget: mocks.register }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

// renderPanel retains the real row editor and substitutes its external selection menu.
// Callers settle loading before retrieving the registered section save callbacks.
async function renderPanel() {
  const wrapper = mount(ApiKeyDefaults, {
    global: {
      stubs: {
        Select: {
          name: 'Select',
          props: ['modelValue', 'options'],
          emits: ['update:modelValue'],
          template: '<div />',
        },
      },
    },
  })
  await flushPromises()
  const target = mocks.register.mock.calls.at(-1)![1] as SettingsSaveTarget
  return { wrapper, target }
}

beforeEach(() => {
  vi.clearAllMocks()
  mocks.get.mockResolvedValue({ data: { group_ids: [1, 2] } })
  mocks.put.mockResolvedValue({})
  mocks.groups.mockResolvedValue([
    { id: 1, name: 'First' },
    { id: 2, name: 'Second' },
  ])
})

describe('administrator Key channels', () => {
  // Reordering loaded defaults changes the complete persisted priority list.
  // A duplicate selection is rejected before another server write can occur.
  it('persists ordered identifiers and rejects duplicates', async () => {
    const { wrapper, target } = await renderPanel()
    wrapper.findComponent({ name: 'RuleListEditor' }).vm.$emit('move', 1, 0)
    expect(await target.save()).toBe(true)
    expect(mocks.put).toHaveBeenCalledWith('/admin/api-keys/defaults', {
      group_ids: [2, 1],
      cooldown_seconds: 60,
    })
    const selects = wrapper.findAllComponents({ name: 'Select' })
    selects[0]!.vm.$emit('update:modelValue', 1)
    await flushPromises()
    expect(await target.save()).toBe(false)
    expect(mocks.put).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  // A user changes the channel order while an earlier save is pending.
  // Completion marks only the submitted snapshot clean, leaving subsequent edits dirty.
  it('retains changes made during persistence', async () => {
    const { wrapper, target } = await renderPanel()
    let finish!: () => void
    mocks.put.mockImplementationOnce(
      () =>
        new Promise<void>((resolve) => {
          finish = resolve
        }),
    )
    wrapper.findComponent({ name: 'RuleListEditor' }).vm.$emit('move', 1, 0)
    const saving = target.save()
    wrapper.findComponent({ name: 'RuleListEditor' }).vm.$emit('move', 0, 1)
    finish()
    expect(await saving).toBe(true)
    expect(typeof target.dirty === 'boolean' ? target.dirty : target.dirty.value).toBe(true)
    wrapper.unmount()
  })

  // Defaults fail to load before the initial editor becomes available.
  // The panel exposes the failure and prevents an unread empty list from saving.
  it('blocks saving after an initial read failure', async () => {
    mocks.get.mockRejectedValueOnce(new Error('unavailable'))
    const { wrapper, target } = await renderPanel()
    expect(wrapper.find('[role="alert"]').exists()).toBe(true)
    expect(await target.save()).toBe(false)
    expect(mocks.put).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})
