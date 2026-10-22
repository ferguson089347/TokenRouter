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
      tests: [
        testFixture({ group_id: 7, group_name: 'Channel A', benchmark: 'drawing', source: 'site' }),
      ],
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
  // The editor requires one personal Key bound to a system drawing channel.
  // Submission sends the selected Key ID while keeping its secret outside request data and labels.
  it('只选择自己的Key提交，模型和执行参数由系统配置提供', async () => {
    const wrapper = mountDialog()
    await flushPromises()
    expect(list).toHaveBeenCalledWith(
      1,
      100,
      { scope: 'personal', status: 'active' },
      { signal: expect.any(AbortSignal) },
    )
    expect(wrapper.getComponent('#self-test-key').props('options')).toEqual([
      { value: 9, label: 'Personal key · Channel A' },
    ])
    expect(wrapper.text()).not.toContain('private-credential')
    await wrapper.get('#intelligence-self-test').trigger('submit')
    expect(selfTest).not.toHaveBeenCalled()
    expect(wrapper.findAllComponents({ name: 'Select' })).toHaveLength(1)
    expect(wrapper.find('textarea').exists()).toBe(false)
    expect(wrapper.find('input[type="checkbox"]').exists()).toBe(false)
    wrapper.getComponent('#self-test-key').vm.$emit('update:modelValue', 9)
    await flushPromises()
    await wrapper.get('#intelligence-self-test').trigger('submit')
    await flushPromises()
    expect(selfTest).toHaveBeenCalledWith({ api_key_id: 9 })
    expect(wrapper.emitted('submitted')).toHaveLength(1)
    expect(wrapper.emitted('close')).toHaveLength(1)
    wrapper.unmount()
  })
})
