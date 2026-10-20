import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import SupportContactEditor from '../SupportContactEditor.vue'
import ImageUpload from '@/components/common/ImageUpload.vue'
import { parseSupportContacts } from '@/utils/supportContacts'

// SupportContactEditor.spec.ts checks legacy contact editing and uploaded images
// through the localized settings string. The row-editor stub exposes its items
// and add event so assertions focus on emitted configuration and row identity
// when the parent echoes a draft update.

vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({
    t: (key: string, params?: { max?: string }) =>
      key === 'common.fileTooLargeKb' ? `Limit ${params?.max} KB` : key,
  }),
}))

describe('SupportContactEditor', () => {
  // A parent echoes each edited draft. Row objects remain stable, and the image
  // uploader's result remains attached to its original contact during that echo.
  it('adds contact methods and retains uploaded images across parent updates', async () => {
    const wrapper = mount(SupportContactEditor, {
      props: { modelValue: 'QQ:123' },
      global: {
        stubs: {
          RuleListEditor: {
            props: ['items'],
            template:
              '<div><button data-add @click="$emit(\'add\')">Add</button><div v-for="(item, index) in items" :key="index"><slot name="row" :item="item" :index="index" /></div></div>',
          },
          ImageUpload: true,
        },
      },
    })
    wrapper
      .findComponent(ImageUpload)
      .vm.$emit('update:modelValue', 'data:image/png;base64,aGVsbG8=')
    await wrapper.vm.$nextTick()
    const emitted = wrapper.emitted('update:modelValue')!.at(-1)![0] as string
    expect(parseSupportContacts(emitted)[0]).toEqual({
      label: 'QQ',
      value: '123',
      image: 'data:image/png;base64,aGVsbG8=',
    })
    await wrapper.setProps({ modelValue: emitted })
    expect(wrapper.findComponent(ImageUpload).props('modelValue')).toBe(
      'data:image/png;base64,aGVsbG8=',
    )
    await wrapper.get('[data-add]').trigger('click')
    expect(
      parseSupportContacts(
        wrapper.emitted('update:modelValue')!.at(-1)![0] as string,
      ),
    ).toHaveLength(2)
    wrapper.unmount()
  })
  // A support image larger than the former limit is read through the real
  // uploader and saved with its contact. Oversized replacement files show the
  // current limit and retain the preceding saved image.
  it('accepts a 402 KB contact image and rejects files above 500 KB', async () => {
    const wrapper = mount(SupportContactEditor, {
      props: { modelValue: 'QQ:123' },
      global: {
        stubs: {
          RuleListEditor: {
            props: ['items'],
            template:
              '<div><div v-for="item in items"><slot name="row" :item="item" /></div></div>',
          },
        },
      },
    })
    const input = wrapper.get('input[type="file"]')
    const accepted = new File([new Uint8Array(402 * 1024)], 'contact.png', {
      type: 'image/png',
    })
    Object.defineProperty(input.element, 'files', {
      configurable: true,
      value: [accepted],
    })
    await input.trigger('change')
    await vi.waitFor(() =>
      expect(wrapper.emitted('update:modelValue')).toHaveLength(1),
    )
    const saved = wrapper.emitted('update:modelValue')![0][0] as string
    expect(parseSupportContacts(saved)[0].image).toMatch(
      /^data:image\/png;base64,/,
    )
    const oversized = new File(
      [new Uint8Array(500 * 1024 + 1)],
      'oversized.png',
      { type: 'image/png' },
    )
    Object.defineProperty(input.element, 'files', {
      configurable: true,
      value: [oversized],
    })
    await input.trigger('change')
    expect(wrapper.text()).toContain('500 KB')
    expect(wrapper.emitted('update:modelValue')).toHaveLength(1)
    wrapper.unmount()
  })
})
