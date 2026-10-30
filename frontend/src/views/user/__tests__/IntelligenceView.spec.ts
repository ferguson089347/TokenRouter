import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import IntelligenceView from '../IntelligenceView.vue'
import { runFixture, testFixture } from '@/components/intelligence/__tests__/fixtures'

// IntelligenceView.spec.ts checks the user model quality page with synthetic observations and
// controlled result APIs. It verifies grouping, drawing order, detail loading, and removal of
// stale results when refreshed permissions deny access to a group. Completed result
// times and drawings remain tied to one observation while active tasks advance independently.

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
  useI18n: () => ({
    t: (key: string, values?: Record<string, unknown>) =>
      values ? `${key} ${JSON.stringify(values)}` : key,
    locale: { value: 'en' },
  }),
}))

// mountPage mounts grouped histories with controlled preview and submission components.
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
          props: ['show', 'test'],
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

describe('用户智商测试页面', () => {
  beforeEach(() => {
    list.mockReset().mockResolvedValue([])
    detail.mockReset()
    preview.mockReset()
  })
  afterEach(() => {
    vi.useRealTimers()
  })
  // Several histories share one group, and a second group has its own multiplier.
  // Group and model headings appear once while source rows retain their latest artwork.
  it('按分组和模型聚合标题、倍率及对应来源，仅播放最新作品', async () => {
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
      testFixture({ id: 2, benchmark: 'drawing', artifacts, runs: artifacts }),
      testFixture({ id: 3, model: 'model-b', benchmark: 'drawing' }),
      testFixture({ id: 4, group_id: 2, group_name: 'Group B', rate_multiplier: 2 }),
    ])
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.findAll('article')).toHaveLength(6)
    expect(wrapper.findAll('h2').map((item) => item.text())).toEqual(['Group A', 'Group B'])
    expect(
      wrapper
        .findAll('[data-testid="group-rate"]')
        .map((item) => item.findAll('span').map((part) => part.text())),
    ).toEqual([
      ['intelligence.rateMultiplier', '1.5x'],
      ['intelligence.rateMultiplier', '2x'],
    ])
    const model = wrapper.get('[data-group-id="1"] [data-model="model-b"]')
    expect(model.findAll('h3')).toHaveLength(1)
    expect(model.findAll('article').map((row) => row.attributes('data-source'))).toEqual([
      'site',
      'user',
    ])
    expect(wrapper.text()).toContain('intelligence.userTests')
    expect(wrapper.text()).toContain('model-b')
    const drawings = wrapper.findAll('.drawing')
    expect(drawings).toHaveLength(1)
    expect(drawings[0].attributes('data-id')).toBe('art-11')
    expect(preview).not.toHaveBeenCalled()
    wrapper.unmount()
  })
  // A user starts testing from a specific model's shared user row.
  // The dialog receives that site's configuration, and acceptance refreshes the visible history.
  it('对应用户行打开当前模型自测，受理后刷新共享结果', async () => {
    const target = testFixture({ id: 5, group_id: 2, model: 'model-b', benchmark: 'drawing' })
    list.mockResolvedValue([testFixture({ benchmark: 'drawing' }), target])
    const wrapper = mountPage()
    await flushPromises()
    await wrapper.get('[data-group-id="2"] [data-source="user"] button').trigger('click')
    expect(wrapper.find('[data-testid="self-test-dialog"]').exists()).toBe(true)
    expect(wrapper.getComponent({ name: 'IntelligenceSelfTestDialog' }).props('test')).toEqual(
      target,
    )
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
  // A generation starts after an assessed drawing and receives newer artwork.
  // The row keeps the completed drawing and finish time, separates active progress, and excludes diagnostics from quality counts.
  it('同时展示最近有效结果的完成时间与本轮进度，作品不串记录', async () => {
    vi.useFakeTimers({ toFake: ['Date', 'setInterval', 'clearInterval'] })
    vi.setSystemTime(new Date('2026-10-03T01:05:00Z'))
    const completed = runFixture({
      id: 'assessed',
      benchmark: 'drawing',
      has_artifact: true,
      first_token_ms: 1813,
      created_at: '2026-10-03T01:00:00Z',
      finished_at: '2026-10-03T01:03:00Z',
    })
    const active = runFixture({
      id: 'active',
      benchmark: 'drawing',
      status: 'submitting',
      verdict: 'pending',
      phase: 'generating',
      has_artifact: true,
      created_at: '2026-10-03T01:04:00Z',
    })
    list.mockResolvedValue([
      testFixture({
        benchmark: 'drawing',
        runs: [
          completed,
          active,
          runFixture({
            id: 'failed-assessment',
            verdict: 'failed',
            created_at: '2026-10-03T00:59:00Z',
          }),
          runFixture({ id: 'diagnostic', status: 'error', verdict: 'error' }),
          runFixture({ id: 'unknown', status: 'unknown', verdict: 'unknown' }),
        ],
        artifacts: [active, completed],
      }),
    ])
    const wrapper = mountPage()
    await flushPromises()
    const row = wrapper.get('[data-source="site"]')
    expect(row.get('.drawing').attributes('data-id')).toBe('assessed')
    expect(row.get('[data-intelligence-status="passed"]').exists()).toBe(true)
    expect(row.get('[data-testid="result-time"] time').attributes('datetime')).toBe(
      completed.finished_at,
    )
    expect(row.get('[data-testid="result-age"]').text()).toBe('2 minutes ago')
    expect(row.get('[data-testid="current-task"]').text()).toContain(
      'intelligence.phases.generating',
    )
    expect(row.get('[data-testid="result-time"] time').text()).toContain('01:03')
    expect(row.get('[data-testid="result-counts"]').text()).toContain('"passed":1,"failed":1')
    expect(row.findAll('[data-run-id]')).toHaveLength(2)
    expect(row.find('[data-run-id="active"]').exists()).toBe(false)
    expect(row.text()).not.toContain('1813')
    expect(row.text()).not.toContain('intelligence.firstToken')
    await vi.advanceTimersByTimeAsync(60000)
    expect(row.get('[data-testid="result-age"]').text()).toBe('3 minutes ago')
    wrapper.unmount()
    expect(vi.getTimerCount()).toBe(0)
  })

  // The first task has no evaluated predecessor and waits for a retry.
  // Its row exposes progress with an empty quality history, then publishes the assessed completion on refresh.
  it('首次检测等待重试时保留空态，完成后同步结果与时间', async () => {
    const active = runFixture({ status: 'queued', verdict: 'pending', phase: 'retrying' })
    list.mockResolvedValueOnce([testFixture({ runs: [active] })])
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.text()).toContain('intelligence.noValidResult')
    expect(wrapper.text()).toContain('intelligence.phases.retrying')
    expect(wrapper.findAll('[data-run-id]')).toHaveLength(0)
    expect(wrapper.find('[data-testid="result-time"]').exists()).toBe(false)
    const completed = runFixture({ finished_at: '2026-10-03T01:02:00Z' })
    list.mockResolvedValueOnce([testFixture({ runs: [completed] })])
    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="current-task"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="result-time"] time').attributes('datetime')).toBe(
      completed.finished_at,
    )
    expect(wrapper.findAll('[data-run-id]')).toHaveLength(1)
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
