import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import IntelligenceDrawing from '../IntelligenceDrawing.vue'
import { intelligenceResultsKey } from '../useIntelligenceResults'

// IntelligenceDrawing.spec.ts checks visibility-driven drawing previews through ticket and
// observer substitutes. It verifies that generated documents use isolated frames, invalid
// destinations remain blocked, and leaving the visible area removes the active preview frame.

vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string) => key }),
}))
const preview = vi.fn()
let visibleCallback: IntersectionObserverCallback

describe('画图预览隔离', () => {
  beforeEach(() => {
    preview.mockReset().mockResolvedValue({
      url: '/api/v1/intelligence-tests/preview-content/a.b',
      expires_at: new Date(Date.now() + 300000).toISOString(),
    })
    vi.stubGlobal(
      'IntersectionObserver',
      class {
        constructor(callback: IntersectionObserverCallback) {
          visibleCallback = callback
        }
        observe() {}
        disconnect() {}
      },
    )
  })
  it('可见后才申请票据，以无同源能力sandbox加载；离屏销毁iframe', async () => {
    const wrapper = mount(IntelligenceDrawing, {
      props: { runId: 'run-1', title: 'drawing' },
      global: { provide: { [intelligenceResultsKey as symbol]: { preview } } },
    })
    expect(preview).not.toHaveBeenCalled()
    visibleCallback(
      [{ isIntersecting: true } as IntersectionObserverEntry],
      {} as IntersectionObserver,
    )
    await flushPromises()
    expect(preview).toHaveBeenCalledWith('run-1')
    const frame = wrapper.get('iframe')
    expect(frame.attributes('sandbox')).toBe('allow-scripts')
    expect(frame.attributes('referrerpolicy')).toBe('no-referrer')
    expect(frame.attributes('srcdoc')).toBeUndefined()
    expect(frame.attributes('src')).toBe('/api/v1/intelligence-tests/preview-content/a.b')
    expect(frame.attributes('scrolling')).not.toBe('no')
    expect(frame.attributes('tabindex')).not.toBe('-1')
    expect(frame.classes()).not.toContain('pointer-events-none')
    visibleCallback(
      [{ isIntersecting: false } as IntersectionObserverEntry],
      {} as IntersectionObserver,
    )
    await flushPromises()
    expect(wrapper.find('iframe').exists()).toBe(false)
    wrapper.unmount()
  })
  // Compact cards scale the complete drawing viewport while retaining active HTML.
  // Resizing changes display scale without requesting another signed document or disabling its controls.
  it('最新作品缩放完整画布，保持动画与交互且不重新加载', async () => {
    const wrapper = mount(IntelligenceDrawing, {
      props: { runId: 'run-1', title: 'drawing', compact: true },
      global: { provide: { [intelligenceResultsKey as symbol]: { preview } } },
    })
    visibleCallback(
      [{ isIntersecting: true } as IntersectionObserverEntry],
      {} as IntersectionObserver,
    )
    await flushPromises()
    Object.defineProperties(wrapper.element, {
      clientWidth: { value: 256 },
      clientHeight: { value: 192 },
    })
    window.dispatchEvent(new Event('resize'))
    await flushPromises()
    const frame = wrapper.get('iframe')
    expect(frame.attributes('scrolling')).toBe('no')
    expect(frame.attributes('style')).toContain('width: 1280px')
    expect(frame.attributes('style')).toContain('height: 960px')
    expect(frame.attributes('style')).toContain('scale(0.2)')
    expect(frame.classes()).not.toContain('pointer-events-none')
    expect(preview).toHaveBeenCalledTimes(1)
    expect(frame.attributes('sandbox')).toBe('allow-scripts')
    expect(frame.attributes('referrerpolicy')).toBe('no-referrer')
    expect(frame.attributes('srcdoc')).toBeUndefined()
    expect(frame.attributes('src')).toBe('/api/v1/intelligence-tests/preview-content/a.b')
    wrapper.unmount()
  })
  it('错误URL不会交给iframe执行', async () => {
    preview.mockResolvedValue({
      url: 'https://evil.example/run',
      expires_at: '',
    })
    const wrapper = mount(IntelligenceDrawing, {
      props: { runId: 'run-1', title: 'drawing' },
      global: { provide: { [intelligenceResultsKey as symbol]: { preview } } },
    })
    visibleCallback(
      [{ isIntersecting: true } as IntersectionObserverEntry],
      {} as IntersectionObserver,
    )
    await flushPromises()
    expect(wrapper.find('iframe').exists()).toBe(false)
    expect(wrapper.text()).toContain('intelligence.noArtwork')
    wrapper.unmount()
  })
})
