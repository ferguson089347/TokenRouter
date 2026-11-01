import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import RechargeBonusSettings from '../RechargeBonusSettings.vue'
import {
  createSettingsSaveRegistry,
  settingsSaveRegistryKey,
} from '@/composables/useSettingsSaveRegistry'
import { emptyRechargeBonusActivity } from '@/utils/payment/rechargeBonus'
import LocalizedFieldsEditor from '@/components/common/LocalizedFieldsEditor.vue'
import RuleListEditor from '@/components/common/RuleListEditor.vue'
import { updateSource, updateTranslation } from '@/i18n/contentEdit'

// RechargeBonusSettings.spec.ts exercises the independent promotion editor
// through its real row controls and shared settings save registry. API replacements
// retain accepted values and acknowledge writes separately from reads, exposing
// persistence failures, normalized readback, dirty baselines, and request cancellation.

// Hoisted API spies isolate the editor's persistence and toast effects from other settings.
const { getConfig, updateConfig, showError } = vi.hoisted(() => ({
  getConfig: vi.fn(),
  updateConfig: vi.fn(),
  showError: vi.fn(),
}))
vi.mock('@/api/admin/payment', () => ({ adminPaymentAPI: { getConfig, updateConfig } }))
vi.mock('@/stores', () => ({ useAppStore: () => ({ showError }) }))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string) => key }),
}))

// configuration supplies only fields consumed by this independently persisted card.
let configuration = {
  balance_recharge_multiplier: 1,
  recharge_bonus_tiers: [] as { min_amount: number; bonus_amount: number }[],
  recharge_bonus_notice: '',
  recharge_bonus_activity: emptyRechargeBonusActivity(),
}

// mountSettings provides a real save registry and stubs unrelated visual controls.
// Callers await initial promises before editing and unmount the returned wrapper
// after assertions so its clock and outstanding reads release their resources.
function mountSettings() {
  const registry = createSettingsSaveRegistry()
  const wrapper = mount(RechargeBonusSettings, {
    global: {
      provide: { [settingsSaveRegistryKey as symbol]: registry.registry },
      stubs: {
        DateTimePicker: true,
        Select: true,
        Icon: true,
        RouterLink: true,
        RechargeBonusBreakdown: true,
      },
    },
  })
  return { registry, wrapper }
}

beforeEach(() => {
  vi.clearAllMocks()
  configuration = {
    balance_recharge_multiplier: 1,
    recharge_bonus_tiers: [],
    recharge_bonus_notice: '',
    recharge_bonus_activity: emptyRechargeBonusActivity(),
  }
  getConfig.mockImplementation(async () => ({ data: structuredClone(configuration) }))
  updateConfig.mockImplementation(async (payload) => {
    configuration = { ...configuration, ...structuredClone(payload) }
    return { data: { message: 'updated' } }
  })
})

describe('RechargeBonusSettings', () => {
  // An administrator adds a tier through the row editor and saves from the page registry.
  // The acknowledgement must be followed by readback before the draft becomes clean.
  it('saves only gift fields and records the accepted baseline', async () => {
    const { wrapper, registry } = mountSettings()
    await flushPromises()
    expect(registry.dirty.value).toBe(false)
    await wrapper.get('[data-testid="recharge-bonus-tiers-add"]').trigger('click')
    expect(registry.dirty.value).toBe(true)
    expect(await registry.saveDirty()).toBe(true)
    expect(updateConfig).toHaveBeenCalledWith(
      expect.objectContaining({ recharge_bonus_tiers: [{ min_amount: 100, bonus_amount: 11 }] }),
    )
    expect(updateConfig.mock.calls[0][0]).not.toHaveProperty('enabled')
    expect(getConfig).toHaveBeenCalledTimes(2)
    expect(registry.dirty.value).toBe(false)
    wrapper.unmount()
  })

  // A rejected configuration write must preserve the administrator's edited tier.
  // The shared save registry reports failure and keeps the section dirty for retry.
  it('retains edits after a failed save', async () => {
    const { wrapper, registry } = mountSettings()
    await flushPromises()
    await wrapper.get('[data-testid="recharge-bonus-tiers-add"]').trigger('click')
    updateConfig.mockRejectedValueOnce(new Error('rejected'))
    expect(await registry.saveDirty()).toBe(false)
    expect(registry.dirty.value).toBe(true)
    expect(showError).toHaveBeenCalledOnce()
    wrapper.unmount()
  })

  // Editing an existing activity keeps its cumulative ledger identity until an
  // administrator starts the next activity. The new draft must use independent progress.
  it('keeps the current campaign identity until starting another period', async () => {
    configuration.recharge_bonus_activity.campaign_id = 'current-event'
    const { wrapper, registry } = mountSettings()
    await flushPromises()
    await wrapper.get('[data-testid="recharge-bonus-tiers-add"]').trigger('click')
    expect(await registry.saveDirty()).toBe(true)
    expect(updateConfig.mock.calls[0][0].recharge_bonus_activity.campaign_id).toBe('current-event')
    const button = wrapper
      .findAll('button')
      .find((item) => item.text() === 'rechargeBonus.newCampaign')!
    await button.trigger('click')
    expect(await registry.saveDirty()).toBe(true)
    expect(updateConfig.mock.calls[1][0].recharge_bonus_activity.campaign_id).not.toBe(
      'current-event',
    )
    expect(updateConfig.mock.calls[1][0].recharge_bonus_activity.campaign_id).toBeTruthy()
    wrapper.unmount()
  })

  // Removing a preceding FAQ must retain the remaining entry's translation identity.
  // Source copy and reviewed translations travel together in the versioned save payload.
  it('saves localized content using stable FAQ identities', async () => {
    const { wrapper, registry } = mountSettings()
    await flushPromises()
    await wrapper.get('[data-testid="recharge-bonus-faqs-add"]').trigger('click')
    await wrapper.get('[data-testid="recharge-bonus-faqs-add"]').trigger('click')
    const faqList = wrapper
      .findAllComponents(RuleListEditor)
      .find((item) => item.props('testId') === 'recharge-bonus-faqs')!
    const originalIDs = faqList.props('items').map((item) => (item as { id: string }).id)
    const editor = wrapper.findComponent(LocalizedFieldsEditor)
    const content = updateSource(
      editor.props('modelValue')!,
      {
        ...editor.props('modelValue')!.source,
        title: 'Recharge gifts',
        [`faq.${originalIDs[1]}.question`]: 'Which tier applies?',
        [`faq.${originalIDs[1]}.answer`]: 'The highest qualifying tier for this payment.',
      },
      'en',
    )
    editor.vm.$emit('update:modelValue', content)
    await flushPromises()
    faqList.vm.$emit('remove', 0)
    await flushPromises()
    const edited = editor.props('modelValue')!
    editor.vm.$emit(
      'update:modelValue',
      updateTranslation(edited, 'zh-Hans', { ...edited.source, title: '充值赠送' }),
    )
    await flushPromises()
    expect(await registry.saveDirty()).toBe(true)
    const payload = updateConfig.mock.calls[0][0]
    expect(payload.recharge_bonus_activity.title).toBe('Recharge gifts')
    expect(payload.recharge_bonus_activity.faqs).toEqual([
      {
        id: originalIDs[1],
        question: 'Which tier applies?',
        answer: 'The highest qualifying tier for this payment.',
      },
    ])
    expect(payload.recharge_bonus_localization.reviewed_locales).toContain('zh-Hans')
    expect(payload.recharge_bonus_localization.translations['zh-Hans'].value.title).toBe('充值赠送')
    expect(payload.recharge_bonus_localization.source).not.toHaveProperty(
      `faq.${originalIDs[0]}.question`,
    )
    wrapper.unmount()
  })

  // The initial read can remain pending while navigation removes the settings page.
  // Teardown must abort its signal and suppress feedback from the departed panel.
  it('cancels its initial read on unmount', async () => {
    getConfig.mockImplementationOnce(() => new Promise(() => {}))
    const { wrapper } = mountSettings()
    const signal = getConfig.mock.calls[0][0] as AbortSignal
    wrapper.unmount()
    expect(signal.aborted).toBe(true)
    expect(showError).not.toHaveBeenCalled()
  })
})
