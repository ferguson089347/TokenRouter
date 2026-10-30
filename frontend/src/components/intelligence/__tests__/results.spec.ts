import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import IntelligenceResultBar from '../IntelligenceResultBar.vue'
import {
  groupTests,
  intelligencePreviewURL,
  recentRuns,
  resultColor,
  resultMarkdown,
  resultStatus,
  observationTime,
  summarizeTestRuns,
} from '../results'
import { runFixture, testFixture } from './fixtures'

// results.spec.ts checks model quality result ordering, grouping, and display through
// synthetic observations. It verifies status colors, bounded history, run selection, Markdown
// sanitization, and permitted signed preview paths without executing generated model output.

vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string) => key }),
}))

describe('检测结果展示契约', () => {
  // More than sixty active or diagnostic observations accompany an assessed history.
  // Pending work cannot displace valid samples, and a newer drawing cannot illustrate the selected completed result.
  it('分别选择有效历史与活动任务，保持结果和作品属于同一记录', () => {
    const assessed = runFixture({ id: 'assessed', has_artifact: true })
    const failed = runFixture({
      id: 'failed',
      verdict: 'failed',
      created_at: '2026-10-02T01:00:00Z',
    })
    const pending = Array.from({ length: 65 }, (_, index) =>
      runFixture({
        id: `pending-${index}`,
        status: 'running',
        verdict: 'pending',
        has_artifact: true,
        created_at: new Date(Date.UTC(2026, 9, 4, 0, index)).toISOString(),
      }),
    )
    const test = testFixture({
      runs: [
        failed,
        assessed,
        ...pending,
        runFixture({ id: 'error', status: 'error', verdict: 'failed' }),
        runFixture({ id: 'unknown', status: 'unknown', verdict: 'passed' }),
      ],
      artifacts: [pending.at(-1)!, assessed],
    })
    const summary = summarizeTestRuns(test)
    expect(summary.history.map((run) => run.id)).toEqual(['failed', 'assessed'])
    expect(summary.latest?.id).toBe('assessed')
    expect(summary.active?.id).toBe('pending-64')
    expect(summary.artwork?.id).toBe('assessed')
    expect(summary.passedCount).toBe(1)
    expect(summary.failedCount).toBe(1)
    expect(
      summarizeTestRuns({
        ...test,
        runs: [{ ...assessed, has_artifact: false }],
        artifacts: pending,
      }).artwork,
    ).toBeUndefined()
    expect(test.runs[0].id).toBe('failed')
  })

  // Completed legacy summaries lack a finish timestamp while pending tasks still have submission times.
  // Timestamp selection keeps those meanings separate and honors an explicit finish time when supplied.
  it('完成记录优先使用结束时间，旧记录使用更新时间，活动任务使用提交时间', () => {
    const run = runFixture({ finished_at: '2026-10-03T01:02:00Z' })
    expect(observationTime(run)).toBe(run.finished_at)
    expect(observationTime(runFixture())).toBe(run.updated_at)
    expect(observationTime({ ...run, status: 'running' })).toBe(run.created_at)
  })

  it('明确未通过为红，执行异常与未知为黄，运行中保持灰色', () => {
    expect(resultColor(runFixture())).toContain('emerald')
    expect(resultColor(runFixture({ verdict: 'failed' }))).toContain('rose')
    expect(resultColor(runFixture({ status: 'error', verdict: 'failed' }))).toContain('amber')
    expect(resultStatus(runFixture({ status: 'running', verdict: 'failed' }))).toBe('running')
    expect(resultStatus(runFixture({ verdict: 'pending' }))).toBe('unknown')
  })

  // Seventy reverse-ordered observations exceed the visible history limit.
  // Selection retains sixty chronological results while the supplied array keeps its original ordering for other consumers.
  it('只保留最近60条，状态条从旧到新且不修改输入', () => {
    const runs = Array.from({ length: 70 }, (_, index) =>
      runFixture({
        id: `run-${index}`,
        has_artifact: true,
        created_at: new Date(Date.UTC(2026, 9, 3, 0, index)).toISOString(),
      }),
    ).reverse()
    expect(recentRuns(runs)).toHaveLength(60)
    expect(recentRuns(runs)[0].id).toBe('run-10')
    expect(recentRuns(runs)[59].id).toBe('run-69')
    expect(runs[0].id).toBe('run-69')
  })

  it('同组多个模型和同模型多个类型分层分组，不用分组名合并不同分组', () => {
    const grouped = groupTests([
      testFixture(),
      testFixture({ id: 2, benchmark: 'drawing' }),
      testFixture({ id: 3, model: 'model-b' }),
      testFixture({ id: 4, group_id: 2 }),
    ])
    expect(grouped).toHaveLength(2)
    expect(grouped[0].models).toHaveLength(2)
    expect(grouped[0].models[0].tests).toHaveLength(2)
  })

  // History nodes expose observations for inspection even when details are missing.
  // Pointer activation does not emit a result selection or open another page.
  it('历史节点可聚焦检查，点击不发出选择事件', async () => {
    const first = runFixture()
    const second = runFixture({ id: 'run-2', has_detail: false })
    const wrapper = mount(IntelligenceResultBar, {
      props: { runs: [first, second] },
    })
    await wrapper.get('[data-run-id="run-1"]').trigger('click')
    expect(wrapper.emitted('select')).toBeUndefined()
    expect(wrapper.get('[data-run-id="run-2"]').attributes('tabindex')).toBe('0')
    wrapper.unmount()
  })

  it('模型Markdown清理脚本、危险链接与远程图片', () => {
    const rendered = resultMarkdown(
      '**通过** <script>alert(1)</script><img src="https://evil.example/track"><a href="javascript:alert(2)">坏链接</a><iframe src="/admin"></iframe>',
    )
    expect(rendered).toContain('<strong>通过</strong>')
    expect(rendered).not.toMatch(/<script|<img|<iframe|javascript:/)
  })

  it.each([
    'https://evil.example/a',
    '//evil.example/a',
    '/api/v1/admin/settings',
    '/api/v1/intelligence-tests/preview-content/a.b?x=1',
    '/api/v1/intelligence-tests/preview-content/a.b#x',
    'javascript:alert(1)',
  ])('拒绝非签名相对预览路径 %s', (value) => {
    expect(intelligencePreviewURL(value)).toBe('')
  })

  it('允许本站签名预览路径', () => {
    const url = '/api/v1/intelligence-tests/preview-content/eyJhbGciOi.a-_b123'
    expect(intelligencePreviewURL(url)).toBe(url)
  })
})
