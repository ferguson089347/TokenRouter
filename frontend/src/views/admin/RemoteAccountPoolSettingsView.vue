<template>
  <AppLayout>
    <form class="w-full min-w-0 space-y-4" @submit.prevent="saveAllSettings">
      <div v-if="loading" class="card p-6"><ContentSkeleton variant="form" :rows="4" /></div>
      <template v-else>
        <p
          v-if="settings?.auth_invalid"
          role="status"
          class="rounded-surface border border-amber-200 bg-amber-50 p-4 text-sm text-amber-800 dark:border-amber-500/20 dark:bg-amber-500/10 dark:text-amber-200"
        >
          {{ t('remotePool.rtInvalid') }}
        </p>
        <fieldset :disabled="saving || discarding || loadFailed" class="card space-y-6 p-6">
          <SettingsSection :title="t('remotePool.connection')" :hint="t('remotePool.loginHint')">
            <div class="flex items-center justify-between gap-4">
              <label
                for="remote-pool-enabled"
                class="text-sm font-medium text-gray-900 dark:text-dark-50"
                >{{ t('remotePool.enabled') }}</label
              >
              <Toggle
                id="remote-pool-enabled"
                v-model="form.enabled"
                :disabled="saving || discarding || loadFailed"
              />
            </div>
            <div>
              <label for="remote-pool-url" class="input-label">{{ t('remotePool.baseURL') }}</label>
              <input
                id="remote-pool-url"
                v-model="form.base_url"
                type="url"
                class="input"
                placeholder="https://aishenji.top"
                required
              />
            </div>
            <div>
              <label for="remote-pool-token" class="input-label">Refresh Token (RT)</label>
              <input
                id="remote-pool-token"
                v-model="form.refresh_token"
                type="password"
                class="input"
                autocomplete="new-password"
                :placeholder="settings?.refresh_token_configured ? t('remotePool.keepSecret') : ''"
              />
              <p class="input-hint">
                {{
                  t('remotePool.sessionStatus', {
                    at: settings?.access_token_configured ? t('remotePool.configured') : '—',
                    rt: settings?.refresh_token_configured ? t('remotePool.configured') : '—'
                  })
                }}
              </p>
            </div>
          </SettingsSection>
          <SettingsSection :title="t('remotePool.bindings')" :hint="t('remotePool.bindingHint')">
            <template #actions>
              <button
                type="button"
                class="btn btn-secondary"
                :disabled="loadingSources || saving || discarding || !settings?.enabled"
                @click="loadSourceGroups"
              >
                <Icon name="refresh" size="sm" :class="{ 'animate-spin': loadingSources }" />{{
                  t('remotePool.loadSourceGroups')
                }}
              </button>
            </template>
            <ContentSkeleton v-if="loadingSources && !bindingRows.length" :rows="2" />
            <template v-else>
              <div
                v-for="binding in bindingRows"
                :key="binding.remoteID"
                class="grid items-center gap-2 sm:grid-cols-2"
              >
                <span class="text-sm text-gray-700 dark:text-dark-100"
                  >{{ binding.remoteName }}
                  <span class="text-xs text-gray-400 dark:text-dark-400"
                    >#{{ binding.remoteID }}</span
                  ></span
                >
                <Select
                  :model-value="localBinding(binding.remoteID)"
                  :options="localGroupOptions"
                  :aria-label="t('remotePool.bindLocalGroup')"
                  :disabled="saving || discarding || loadFailed"
                  searchable
                  @update:model-value="setBinding(binding.remoteID, $event)"
                />
              </div>
              <p v-if="!bindingRows.length" class="text-sm text-gray-500 dark:text-dark-400">
                {{ t('remotePool.noBindings') }}
              </p>
            </template>
          </SettingsSection>
        </fieldset>
        <p v-if="error" role="alert" class="input-error-text">{{ error }}</p>
        <button v-if="loadFailed" type="button" class="btn btn-secondary" @click="initialize">
          {{ t('common.retry') }}
        </button>
      </template>
      <SettingsSaveBar
        :visible="saveRegistry.dirty.value && !loading && !loadFailed"
        :message="t('admin.settings.unsavedChanges')"
      >
        <button
          type="button"
          :disabled="saving || discarding"
          class="btn bg-transparent text-white/70 hover:bg-white/10 hover:text-white focus:ring-offset-gray-900 dark:text-dark-200 dark:hover:bg-dark-800 dark:focus:ring-offset-dark-900"
          @click="discardSettings"
        >
          {{ t('admin.settings.discardChanges') }}
        </button>
        <button
          type="submit"
          :disabled="saving || discarding || loadingSources"
          class="btn btn-primary focus:ring-offset-gray-900 dark:focus:ring-offset-dark-900"
        >
          <Icon v-if="saving" name="refresh" size="sm" class="animate-spin" />{{
            saving ? t('admin.settings.saving') : t('admin.settings.saveSettings')
          }}
        </button>
      </SettingsSaveBar>
    </form>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { getAllIncludingInactive } from '@/api/admin/groups'
import {
  remoteAccountPoolAPI,
  type RemotePoolSettings,
  type RemotePoolSettingsInput
} from '@/api/admin/remoteAccountPool'
import AppLayout from '@/components/layout/AppLayout.vue'
import SettingsSection from '@/components/common/settings/SettingsSection.vue'
import SettingsSaveBar from '@/components/common/settings/SettingsSaveBar.vue'
import ContentSkeleton from '@/components/common/ContentSkeleton.vue'
import Toggle from '@/components/common/Toggle.vue'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'
import { useDirtyTracker } from '@/composables/useDirtyTracker'
import { provideSettingsSaveRegistry } from '@/composables/useSettingsSaveRegistry'

// RemoteAccountPoolSettingsView.vue configures the source site, refresh credentials,
// and local group bindings used by the user's account directory. The administrator
// form loads redacted settings and the source group directory; dirty tracking
// exposes the shared settings save bar when its writable draft changes.
//
// Saved refresh tokens remain on the server. Saving clears the submitted token
// and records a clean draft before source groups are reloaded. Source directory
// reads have separate request ownership so replacing a source cannot publish an
// older directory. Loading refreshed session status updates read-only settings
// independently of the editable draft, allowing current binding edits to remain intact.

// Configuration state separates the writable draft, redacted session status, local choices, and source directory request lifetime for administrator settings.
const { t } = useI18n()
const appStore = useAppStore()
const form = reactive<RemotePoolSettingsInput>({
  enabled: false,
  base_url: 'https://aishenji.top',
  refresh_token: '',
  bindings: []
})
const settings = ref<RemotePoolSettings>()
const localGroups = ref<Array<{ id: number; name: string; rate_multiplier: number }>>([])
const sourceGroups = ref<Array<{ id: number; name: string }>>([])
const loading = ref(true)
const loadingSources = ref(false)
const loadFailed = ref(false)
const saving = ref(false)
const discarding = ref(false)
const error = ref('')
let controller: AbortController | undefined
let disposed = false
const { dirty, markClean } = useDirtyTracker({ pool: () => form })
const saveRegistry = provideSettingsSaveRegistry()
saveRegistry.registry.register('remotePool', { dirty, save: saveSettings, discard: loadSettings })
const localGroupOptions = computed(() => [
  { value: '', label: t('remotePool.notBound') },
  ...localGroups.value.map((group) => ({
    value: String(group.id),
    label: `${group.name} · ${group.rate_multiplier}x`
  }))
])
const bindingRows = computed(() => {
  const rows = new Map(
    sourceGroups.value.map((group) => [group.id, { remoteID: group.id, remoteName: group.name }])
  )
  for (const binding of form.bindings) {
    if (!rows.has(binding.remote_group_id))
      rows.set(binding.remote_group_id, {
        remoteID: binding.remote_group_id,
        remoteName: t('remotePool.sourceGroup')
      })
  }
  return [...rows.values()]
})

/** message extracts an API failure or supplies the localized settings error.
 * Unknown failures use fixed copy so the form can display a meaningful alert
 * even when a rejected request provides no message string.
 */
function message(cause: unknown) {
  // Nonstandard failures lack a message. Use localized settings copy so a rejected request cannot leave the administrator with an empty alert.
  return cause &&
    typeof cause === 'object' &&
    'message' in cause &&
    typeof cause.message === 'string'
    ? cause.message
    : t('remotePool.loadFailed')
}

/** applySettings replaces the editable draft and records its saved baseline.
 * The refresh-token field is cleared after loading or saving. Source group names
 * are loaded independently, and changes to those labels do not mark the draft dirty.
 */
function applySettings(value: RemotePoolSettings) {
  settings.value = value
  form.enabled = value.enabled
  form.base_url = value.base_url
  form.refresh_token = ''
  form.bindings = value.bindings.map((binding) => ({ ...binding }))
  markClean('pool')
}

/** initialize loads saved settings and the local group choices before enabling edits.
 * A failed read keeps the form disabled and exposes retry. Successful initialization
 * requests source labels for an enabled saved pool without reading account-list APIs.
 */
async function initialize() {
  loading.value = true
  loadFailed.value = false
  error.value = ''
  try {
    const [value, groups] = await Promise.all([
      remoteAccountPoolAPI.getSettings(),
      getAllIncludingInactive()
    ])
    if (disposed) return
    localGroups.value = groups
    applySettings(value)
  } catch (cause) {
    if (!disposed) {
      error.value = message(cause)
      loadFailed.value = true
    }
  } finally {
    loading.value = false
  }
  if (!disposed && !loadFailed.value && settings.value?.enabled) await loadSourceGroups()
}

/** loadSettings discards the draft by reloading saved configuration and source labels.
 * Outstanding source reads are cancelled before replacement, and failures leave the
 * existing draft dirty so the administrator can retry the discard operation.
 */
async function loadSettings() {
  controller?.abort()
  const value = await remoteAccountPoolAPI.getSettings()
  if (disposed) return
  applySettings(value)
  sourceGroups.value = []
  if (value.enabled) await loadSourceGroups()
}

/** saveAllSettings submits changed settings through the shared page save registry. */
async function saveAllSettings() {
  if (saving.value || discarding.value || loadingSources.value || loadFailed.value || !dirty.value)
    return
  if (await saveRegistry.saveDirty()) appStore.showSuccess(t('remotePool.saved'))
}

/** saveSettings persists the source session and bindings and records its saved baseline.
 * Source reads are aborted before submission so an older directory cannot replace
 * labels belonging to a changed site. A failed save retains the draft and token
 * input, allowing the administrator to correct the failure and retry.
 *
 * Success clears the token input and marks the submitted form clean before loading
 * source groups. Read-only session status refreshes separately from the draft, and
 * the form stays disabled until the save operation finishes.
 */
async function saveSettings(): Promise<boolean> {
  saving.value = true
  error.value = ''
  // Cancel the old directory before changing source settings so its completion cannot replace group labels loaded from the newly saved site.
  controller?.abort()
  try {
    const value = await remoteAccountPoolAPI.saveSettings({
      ...form,
      bindings: form.bindings.map((binding) => ({ ...binding }))
    })
    if (disposed) return false
    applySettings(value)
    sourceGroups.value = []
    if (value.enabled) await loadSourceGroups()
    return true
  } catch (cause) {
    if (!disposed) error.value = message(cause)
    return false
  } finally {
    saving.value = false
  }
}

/** discardSettings reloads persisted values through the shared settings save registry.
 * The form stays disabled during discard so a later completion cannot overwrite
 * fresh edits. A reload failure leaves the draft available and displays its error.
 */
async function discardSettings() {
  discarding.value = true
  error.value = ''
  try {
    await saveRegistry.discardDirty()
  } catch (cause) {
    if (!disposed) error.value = message(cause)
  } finally {
    discarding.value = false
  }
}

/** localBinding returns the selected local group for one source directory row. */
function localBinding(remoteID: number) {
  return String(
    form.bindings.find((binding) => binding.remote_group_id === remoteID)?.local_group_id ?? ''
  )
}

/** setBinding replaces one source mapping or removes it when its selection clears. */
function setBinding(remoteID: number, value: unknown) {
  form.bindings = form.bindings.filter((binding) => binding.remote_group_id !== remoteID)
  if (value) form.bindings.push({ remote_group_id: remoteID, local_group_id: Number(value) })
}

/** loadSourceGroups refreshes administrator source labels and read-only session status.
 * Each call replaces the previous controller, and only the newest request can
 * publish its directory or clear the source-loading indicator. Leaving settings
 * aborts outstanding reads before they can update this page.
 *
 * Session status is read after source synchronization because that operation may
 * rotate credentials or mark RT invalid. The status update leaves the editable
 * draft intact, retaining binding choices entered while the request was running.
 */
async function loadSourceGroups() {
  controller?.abort()
  const request = new AbortController()
  controller = request
  loadingSources.value = true
  error.value = ''
  try {
    const groups = await remoteAccountPoolAPI.sourceGroups(request.signal)
    if (request.signal.aborted) return
    sourceGroups.value = groups
    const value = await remoteAccountPoolAPI.getSettings(request.signal)
    if (!request.signal.aborted) settings.value = value
  } catch (cause) {
    if (!request.signal.aborted) error.value = message(cause)
  } finally {
    if (controller === request) loadingSources.value = false
  }
}

onMounted(initialize)
onUnmounted(() => {
  disposed = true
  form.refresh_token = ''
  // Cancel source reads before leaving settings so an older response cannot publish labels or credentials status into the unmounted administrator page.
  controller?.abort()
})
</script>
