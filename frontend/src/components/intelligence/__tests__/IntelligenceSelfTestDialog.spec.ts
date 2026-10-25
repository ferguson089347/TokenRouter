import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import IntelligenceSelfTestDialog from '../IntelligenceSelfTestDialog.vue'
import { runFixture, testFixture } from './fixtures'

// IntelligenceSelfTestDialog.spec.ts checks self-test submission through controlled key and
// configuration APIs. It verifies that the user selects an owned eligible key while system
// configuration supplies model execution parameters, keeping secret credentials out of the
// submitted public request.

const { list, selfTest } = vi.hoisted(() => ({ list: vi.fn(), selfTest: vi.fn() }))
vi.mock('@/api/keys', () => ({ keysAPI: { list } }))
vi.mock('@/api/intelligence', () => ({ intelligenceAPI: { selfTest } }))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string) => key }),
}))

// mountDialog supplies simple controls so tests exercise credential selection
// and submission rules without depending on the shared Select floating panel.
function mountDialog() {
  return mount(IntelligenceSelfTestDialog, {
    props: {
      show: true,
      test: testFixture({
        group_id: 7,
        group_name: 'Channel A',
        benchmark: 'drawing',
        source: 'site',
      }),
    },
    global: {
      stubs: {
        BaseDialog: {
          props: ['show'],
          template: '<div v-if="show"><slot /><slot name="footer" /></div>',
        },
        Select: {
          name: 'Select',
          props: ['id', 'modelValue', 'options', 'disabled'],
          emits: ['update:modelValue'],
          template: '<div :id="id" />',
        },
      },
    },
  })
}

describe('用户公开自测', () => {
  beforeEach(() => {
    list.mockReset().mockResolvedValue({
      pages: 1,
      items: [
        {
          id: 9,
          name: 'Personal key',
          key: 'private-credential',
          group_id: 7,
          status: 'active',
          quota: 0,
          quota_used: 0,
          scope: 'personal',
        },
        {
          id: 11,
          name: 'Unconfigured channel',
          group_id: 99,
          status: 'active',
          quota: 0,
          quota_used: 0,
          scope: 'personal',
        },
        {
          id: 10,
          name: 'Team key',
          group_id: 7,
          status: 'active',
          team_id: 1,
          quota: 0,
          quota_used: 0,
        },
      ],
    })
    selfTest.mockReset().mockResolvedValue(runFixture({ status: 'queued' }))
  })
  // The selected model narrows candidates to valid personal Keys in its group.
  // The first candidate is ready for submission, which sends identifiers and excludes credential text.
  it('过滤当前分组并默认选中可用Key，提交所点模型的配置', async () => {
    const wrapper = mountDialog()
    await flushPromises()
    expect(list).toHaveBeenCalledWith(
      1,
      100,
      { scope: 'personal', status: 'active', group_id: 7 },
      { signal: expect.any(AbortSignal) },
    )
    expect(wrapper.getComponent('#self-test-key').props('options')).toEqual([
      { value: 9, label: 'Personal key' },
    ])
    expect(wrapper.text()).not.toContain('private-credential')
    expect(wrapper.getComponent('#self-test-key').props('modelValue')).toBe(9)
    expect(selfTest).not.toHaveBeenCalled()
    expect(wrapper.findAllComponents({ name: 'Select' })).toHaveLength(1)
    expect(wrapper.find('textarea').exists()).toBe(false)
    expect(wrapper.find('input[type="checkbox"]').exists()).toBe(false)
    await wrapper.get('#intelligence-self-test').trigger('submit')
    await flushPromises()
    expect(selfTest).toHaveBeenCalledWith({ api_key_id: 9, config_id: 1 })
    expect(wrapper.emitted('submitted')).toHaveLength(1)
    expect(wrapper.emitted('close')).toHaveLength(1)
    wrapper.unmount()
  })

  // A group without any usable personal Key has nothing that can fund a test.
  // The editor keeps submission disabled and displays the group-specific empty state.
  it('当前分组没有有效Key时禁止提交', async () => {
    list.mockResolvedValue({ pages: 1, items: [] })
    const wrapper = mountDialog()
    await flushPromises()
    expect(wrapper.text()).toContain('intelligence.noSelfTestKeys')
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined()
    await wrapper.get('#intelligence-self-test').trigger('submit')
    expect(selfTest).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  // Changing the target group starts a new candidate request while the prior one is pending.
  // Its aborted response cannot replace the current group's automatically selected Key.
  it('切换分组后忽略旧候选请求的迟到响应', async () => {
    let resolvePrevious!: (value: unknown) => void
    list.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolvePrevious = resolve
        }),
    )
    const wrapper = mountDialog()
    const previousSignal = list.mock.calls[0][3].signal as AbortSignal
    list.mockResolvedValueOnce({
      pages: 1,
      items: [
        {
          id: 20,
          name: 'Next group key',
          group_id: 8,
          status: 'active',
          quota: 0,
          quota_used: 0,
        },
      ],
    })
    await wrapper.setProps({ test: testFixture({ id: 2, group_id: 8, benchmark: 'drawing' }) })
    await flushPromises()
    expect(previousSignal.aborted).toBe(true)
    resolvePrevious({
      pages: 1,
      items: [{ id: 9, group_id: 7, status: 'active', quota: 0, quota_used: 0 }],
    })
    await flushPromises()
    expect(wrapper.getComponent('#self-test-key').props('modelValue')).toBe(20)
    expect(wrapper.getComponent('#self-test-key').props('options')).toEqual([
      { value: 20, label: 'Next group key' },
    ])
    await wrapper.get('#intelligence-self-test').trigger('submit')
    await flushPromises()
    expect(selfTest).toHaveBeenCalledWith({ api_key_id: 20, config_id: 2 })
    wrapper.unmount()
  })
})
