import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import IntelligenceView from '../IntelligenceView.vue'
import { runFixture, testFixture } from '@/components/intelligence/__tests__/fixtures'

// IntelligenceView.spec.ts checks the user model quality page with synthetic observations and
// controlled result APIs. It verifies grouping, drawing order, detail loading, and removal of
// stale results when refreshed permissions deny access to a group.

const { list, detail, preview } = vi.hoisted(() => ({
  list: vi.fn(),
  detail: vi.fn(),
  preview: vi.fn(),
}))
vi.mock('@/api/intelligence', () => ({
  intelligenceAPI: { list, detail, preview },
}))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string) => key }),
}))

const mountPage = () =>
  mount(IntelligenceView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        IntelligenceDrawing: {
          props: ['runId'],
          template: '<div class="drawing" :data-id="runId" />',
        },
        IntelligenceSelfTestDialog: {
          name: 'IntelligenceSelfTestDialog',
          props: ['show', 'tests'],
          emits: ['close', 'submitted'],
          template: '<div v-if="show" data-testid="self-test-dialog" />',
        },
        IntelligenceRunDialog: {
          props: ['show', 'run', 'loading', 'error'],
          template: '<div v-if="show" data-testid="detail">{{ run?.id }} {{ error }}</div>',
        },
      },
    },
  })

describe('用户降智检测页面', () => {
  beforeEach(() => {
    list.mockReset().mockResolvedValue([])
    detail.mockReset()
    preview.mockReset()
  })
  it('分组与模型标题聚合，仅播放最新作品，不触发检测提交', async () => {
    const artifacts = Array.from({ length: 12 }, (_, i) =>
      runFixture({
        id: `art-${i}`,
        benchmark: 'drawing',
        has_artifact: true,
        created_at: new Date(Date.UTC(2026, 9, 3, 0, i)).toISOString(),
      }),
    )
    list.mockResolvedValue([
      testFixture(),
      testFixture({ id: 2, benchmark: 'drawing', artifacts }),
      testFixture({ id: 3, model: 'model-b' }),
    ])
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.findAll('article')).toHaveLength(4)
    expect(wrapper.findAll('h2').every((item) => item.text() === 'Group A')).toBe(true)
    expect(wrapper.text()).toContain('intelligence.userTests')
    expect(wrapper.text()).toContain('model-b')
    const drawings = wrapper.findAll('.drawing')
    expect(drawings).toHaveLength(1)
    expect(drawings[0].attributes('data-id')).toBe('art-11')
    expect(preview).not.toHaveBeenCalled()
    wrapper.unmount()
  })
  // Authenticated self-test submission refreshes the public user row in place.
  // The page uses the dialog's explicit action and never queues work merely by loading.
  it('页头打开自测入口，受理后刷新共享结果', async () => {
    const wrapper = mountPage()
    await flushPromises()
    await wrapper
      .findAll('button')
      .find((button) => button.text() === 'intelligence.selfTest')!
      .trigger('click')
    expect(wrapper.find('[data-testid="self-test-dialog"]').exists()).toBe(true)
    wrapper.getComponent({ name: 'IntelligenceSelfTestDialog' }).vm.$emit('submitted', runFixture())
    await flushPromises()
    expect(list).toHaveBeenCalledTimes(2)
    expect(wrapper.text()).toContain('intelligence.submitted')
    wrapper.unmount()
  })
  // Result nodes are inspected through hover and focus previews on this page.
  // Clicking a node issues no detail request and opens no result dialog.
  it('点击历史节点不再请求详情或打开弹窗', async () => {
    const run = runFixture({ id: 'specific-run' })
    list.mockResolvedValue([testFixture({ runs: [run] })])
    detail.mockResolvedValue({ ...run, answer: 'response' })
    const wrapper = mountPage()
    await flushPromises()
    await wrapper.get('[data-run-id="specific-run"]').trigger('click')
    await flushPromises()
    expect(detail).not.toHaveBeenCalled()
    expect(wrapper.find('[data-testid="detail"]').exists()).toBe(false)
    wrapper.unmount()
  })
  it('重新读取403时清理旧结果，避免展示已撤销访问的分组', async () => {
    list
      .mockResolvedValueOnce([testFixture()])
      .mockRejectedValueOnce({ status: 403, message: 'Disabled' })
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.text()).toContain('Group A')
    await wrapper
      .findAll('button')
      .find((button) => button.text() === 'common.refresh')!
      .trigger('click')
    await flushPromises()
    expect(wrapper.text()).not.toContain('Group A')
    expect(wrapper.get('[role="alert"]').text()).toContain('Disabled')
    wrapper.unmount()
  })
})
