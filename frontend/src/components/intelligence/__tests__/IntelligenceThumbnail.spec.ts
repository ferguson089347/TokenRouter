import { describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import IntelligenceThumbnail from '../IntelligenceThumbnail.vue'
import { intelligenceResultsKey } from '../useIntelligenceResults'

// IntelligenceThumbnail.spec.ts checks asynchronous screenshot replacement using
// independently resolved image requests. It verifies that changing the selected run
// invalidates an earlier response, so delayed images cannot overwrite the current screenshot
// or its accessible title.

vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string) => key }),
}))

describe('连续悬浮截图切换', () => {
  // Rapid pointer movement selects a new observation before the earlier image arrives.
  // A late response cannot replace the selected node's screenshot in the shared popover.
  it('切换节点后忽略迟到的旧截图', async () => {
    let first!: (url: string) => void
    let second!: (url: string) => void
    const thumbnail = vi
      .fn()
      .mockImplementationOnce(
        () =>
          new Promise<string>((resolve) => {
            first = resolve
          }),
      )
      .mockImplementationOnce(
        () =>
          new Promise<string>((resolve) => {
            second = resolve
          }),
      )
    const wrapper = mount(IntelligenceThumbnail, {
      props: { runId: 'first', title: 'First screenshot' },
      global: { provide: { [intelligenceResultsKey as symbol]: { thumbnail } } },
    })
    await wrapper.setProps({ runId: 'second', title: 'Second screenshot' })
    second('blob:second')
    await flushPromises()
    expect(wrapper.get('img').attributes('src')).toBe('blob:second')
    first('blob:first')
    await flushPromises()
    expect(wrapper.get('img').attributes('src')).toBe('blob:second')
    expect(wrapper.get('img').attributes('alt')).toBe('Second screenshot')
    wrapper.unmount()
  })
})
