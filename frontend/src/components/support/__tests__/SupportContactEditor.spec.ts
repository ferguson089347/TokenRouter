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
  useI18n: () => ({ t: (key: string) => key })
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
              '<div><button data-add @click="$emit(\'add\')">Add</button><div v-for="(item, index) in items" :key="index"><slot name="row" :item="item" :index="index" /></div></div>'
          },
          ImageUpload: true
        }
      }
    })
    wrapper
      .findComponent(ImageUpload)
      .vm.$emit('update:modelValue', 'data:image/png;base64,aGVsbG8=')
    await wrapper.vm.$nextTick()
    const emitted = wrapper.emitted('update:modelValue')!.at(-1)![0] as string
    expect(parseSupportContacts(emitted)[0]).toEqual({
      label: 'QQ',
      value: '123',
      image: 'data:image/png;base64,aGVsbG8='
    })
    await wrapper.setProps({ modelValue: emitted })
    expect(wrapper.findComponent(ImageUpload).props('modelValue')).toBe(
      'data:image/png;base64,aGVsbG8='
    )
    await wrapper.get('[data-add]').trigger('click')
    expect(
      parseSupportContacts(wrapper.emitted('update:modelValue')!.at(-1)![0] as string)
    ).toHaveLength(2)
    wrapper.unmount()
  })
})
