import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import SupportContactList from '../SupportContactList.vue'
import { serializeSupportContacts } from '@/utils/supportContacts'

// SupportContactList.spec.ts checks shared support rendering for profile and
// header consumers. Mixed contacts retain copyable numbers, group navigation, and
// image previews, while unsupported navigation protocols remain text and cannot
// become clickable browser destinations.

const { copy } = vi.hoisted(() => ({ copy: vi.fn().mockResolvedValue(true) }))
vi.mock('@/composables/useClipboard', () => ({ useClipboard: () => ({ copyToClipboard: copy }) }))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string) => key }),
}))

describe('SupportContactList', () => {
  // Three support methods include a number, group link, and QR image. Users can
  // copy the number and open an enlarged image while links keep safe attributes.
  it('renders numbers, group links, and image previews', async () => {
    const value = serializeSupportContacts([
      { label: 'Support QQ', value: '123456', image: '' },
      { label: 'Telegram', value: 'https://t.me/example', image: '' },
      { label: 'QQ group QR', value: '', image: 'data:image/png;base64,aGVsbG8=' },
    ])
    const wrapper = mount(SupportContactList, {
      props: { value },
      global: {
        stubs: {
          Icon: true,
          BaseDialog: {
            props: ['show', 'title'],
            template: '<aside v-if="show" data-preview><slot /></aside>',
          },
        },
      },
    })
    expect(wrapper.get('a').attributes('href')).toBe('https://t.me/example')
    expect(wrapper.get('a').attributes('rel')).toBe('noopener noreferrer')
    await wrapper.findAll('button')[0].trigger('click')
    expect(copy).toHaveBeenCalledWith('123456')
    await wrapper.findAll('button').at(-1)!.trigger('click')
    expect(wrapper.get('[data-preview] img').attributes('src')).toBe(
      'data:image/png;base64,aGVsbG8=',
    )
    wrapper.unmount()
  })
})
