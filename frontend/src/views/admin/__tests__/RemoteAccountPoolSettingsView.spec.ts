import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import RemoteAccountPoolSettingsView from '../RemoteAccountPoolSettingsView.vue'

// RemoteAccountPoolSettingsView.spec.ts verifies administrator configuration and
// credential input handling independently of the user's directory. Saved settings,
// local group choices, and source group labels drive the form while account-list
// operations remain outside the administrator page's API requests.

// Shared mocks supply redacted pool settings, local groups, source labels, and the application's saved-settings feedback for administrator configuration tests.
const { api, groups, success } = vi.hoisted(() => ({
  api: {
    getSettings: vi.fn(),
    saveSettings: vi.fn(),
    sourceGroups: vi.fn(),
    options: vi.fn(),
    list: vi.fn(),
  },
  groups: vi.fn(),
  success: vi.fn(),
}))
vi.mock('@/components/layout/AppLayout.vue', () => ({
  default: { template: '<main><slot /></main>' },
}))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: success }) }))
vi.mock('@/api/admin/remoteAccountPool', () => ({ remoteAccountPoolAPI: api }))
vi.mock('@/api/admin/groups', () => ({ default: {}, getAllIncludingInactive: groups }))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string) => key }),
}))

// Saved settings contain an expired session and one group binding; the fixture's token is represented exclusively by its configured flag.
const settings = {
  enabled: true,
  base_url: 'https://pool.example',
  refresh_token_configured: true,
  access_token_configured: false,
  auth_invalid: true,
  bindings: [{ remote_group_id: 25, local_group_id: 100 }],
}
let wrapper: ReturnType<typeof render> | undefined

/** render mounts administrator settings with the shared sections and save-bar slots. */
function render() {
  return mount(RemoteAccountPoolSettingsView, {
    global: {
      stubs: {
        SettingsSection: { template: '<section><slot name="actions" /><slot /></section>' },
        SettingsSaveBar: { props: ['visible'], template: '<div v-if="visible"><slot /></div>' },
        Icon: true,
        ContentSkeleton: true,
        Toggle: true,
        Select: true,
      },
    },
  })
}
beforeEach(() => {
  vi.clearAllMocks()
  api.getSettings.mockResolvedValue(settings)
  groups.mockResolvedValue([{ id: 100, name: 'Local group', rate_multiplier: 2.5 }])
  api.sourceGroups.mockResolvedValue([{ id: 25, name: 'Source group' }])
  api.saveSettings.mockResolvedValue({ ...settings, auth_invalid: false })
})
afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
})
describe('RemoteAccountPoolSettingsView', () => {
  // Administrators configure the remote session and bind source groups. The page shows token status and requests settings APIs without loading account rows.
  it('loads configuration and source labels without account-list requests', async () => {
    wrapper = render()
    await flushPromises()
    expect(wrapper.text()).toContain('remotePool.rtInvalid')
    expect(wrapper.text()).toContain('Source group')
    expect(api.list).not.toHaveBeenCalled()
    expect(api.options).not.toHaveBeenCalled()
    expect(wrapper.find('input[type="password"]').exists()).toBe(true)
  })
  // An administrator submits a replacement RT with existing group bindings. Successful persistence clears the input and removes the dirty save bar.
  it('saves RT and bindings and clears the secret input', async () => {
    wrapper = render()
    await flushPromises()
    await wrapper.find('#remote-pool-token').setValue('replacement-rt')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(api.saveSettings).toHaveBeenCalledWith({
      enabled: true,
      base_url: 'https://pool.example',
      refresh_token: 'replacement-rt',
      bindings: [{ remote_group_id: 25, local_group_id: 100 }],
    })
    expect((wrapper.find('#remote-pool-token').element as HTMLInputElement).value).toBe('')
    expect(success).toHaveBeenCalledWith('remotePool.saved')
    expect(api.list).not.toHaveBeenCalled()
  })
  // Discard reloads the saved source configuration. Unsaved token text is cleared and no settings mutation is sent to the backend.
  it('discards unsaved credentials by reloading saved configuration', async () => {
    wrapper = render()
    await flushPromises()
    await wrapper.find('#remote-pool-token').setValue('discard-me')
    await wrapper
      .findAll('button')
      .find((button) => button.text() === 'admin.settings.discardChanges')!
      .trigger('click')
    await flushPromises()
    expect((wrapper.find('#remote-pool-token').element as HTMLInputElement).value).toBe('')
    expect(api.saveSettings).not.toHaveBeenCalled()
  })
})
