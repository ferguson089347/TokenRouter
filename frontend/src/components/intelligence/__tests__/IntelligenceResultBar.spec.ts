import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import IntelligenceResultBar from '../IntelligenceResultBar.vue'
import { runFixture } from './fixtures'

// IntelligenceResultBar.spec.ts checks shared screenshot popovers through mounted history
// nodes and controlled result loading. It verifies pointer-driven image selection, complete
// timestamps, first-token duration labels, and inert click behavior while keeping the hover
// panel reusable across observations.

vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string) => key }),
}))

afterEach(() => {
  document.body.innerHTML = ''
})

describe('历史节点截图浮层', () => {
  // Adjacent history nodes update one mounted screenshot popover while the pointer
  // stays inside the strip. Clicks perform no navigation, and timing reports first-token milliseconds.
  it('移过节点复用浮层切换截图，显示完整时间和首Token毫秒且点击无动作', async () => {
    const newest = runFixture({
      id: 'newest',
      benchmark: 'drawing',
      has_artifact: true,
      first_token_ms: 1813,
      duration_ms: 126300,
    })
    const older = runFixture({
      id: 'older',
      benchmark: 'drawing',
      has_thumbnail: true,
      verdict: 'failed',
      created_at: '2026-10-02T00:00:00Z',
    })
    const wrapper = mount(IntelligenceResultBar, {
      attachTo: document.body,
      props: { runs: [newest, older] },
      global: {
        stubs: {
          IntelligenceThumbnail: { props: ['runId'], template: '<img :data-thumbnail="runId" />' },
        },
      },
    })
    expect(document.querySelector('[role="tooltip"]')).toBeNull()
    await wrapper.get('[data-run-id="older"]').trigger('mouseenter')
    const panel = document.querySelector('[role="tooltip"]')
    expect(document.querySelector('[data-thumbnail="older"]')).not.toBeNull()
    expect(panel?.querySelector('[data-intelligence-status="incorrect"]')).not.toBeNull()
    expect(document.querySelector('iframe')).toBeNull()
    expect(document.querySelector('[data-testid="history-first-token"]')?.textContent).toContain(
      '—',
    )
    await wrapper.get('[data-run-id="older"]').trigger('mouseleave')
    expect(document.querySelector('[role="tooltip"]')).toBe(panel)
    await wrapper.get('[data-run-id="newest"]').trigger('mouseenter')
    expect(document.querySelector('[role="tooltip"]')).toBe(panel)
    expect(document.querySelector('[data-thumbnail="newest"]')).not.toBeNull()
    expect(panel?.querySelector('[data-intelligence-status="passed"]')).not.toBeNull()
    expect(panel?.querySelector('[data-intelligence-status="incorrect"]')).toBeNull()
    expect(panel?.textContent).toMatch(/2026.*10.*03.*01:00:00/)
    expect(document.querySelector('[data-testid="history-first-token"]')?.textContent).toContain(
      '1813 ms',
    )
    expect(panel?.textContent).not.toContain('126.3')
    await wrapper.get('[data-run-id="newest"]').trigger('click')
    expect(wrapper.emitted('select')).toBeUndefined()
    await wrapper.get('[data-testid="intelligence-history-strip"]').trigger('mouseleave')
    expect(document.querySelector('[role="tooltip"]')).toBeNull()
    await wrapper.get('[data-run-id="older"]').trigger('focus')
    expect(document.querySelector('[data-thumbnail="older"]')).not.toBeNull()
    await wrapper.get('[data-run-id="older"]').trigger('blur')
    expect(document.querySelector('[role="tooltip"]')).toBeNull()
    wrapper.unmount()
  })
})
