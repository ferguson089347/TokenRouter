<template>
  <SettingsCard
    :title="t('tickets.settings.title')"
    :description="t('tickets.settings.description')"
  >
    <template #actions>
      <button
        type="button"
        class="btn btn-secondary btn-sm"
        :disabled="loading || saving"
        @click="load"
      >
        {{ t('common.refresh') }}
      </button>
    </template>
    <ContentSkeleton v-if="loading && !form" variant="form" :rows="4" />
    <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
    <template v-if="form">
      <SettingsSection>
        <SettingToggleRow
          id="tickets-enabled"
          v-model="form.enabled"
          :label="t('tickets.settings.enabled')"
          :hint="t('tickets.settings.enabledHint')"
          :disabled="saving"
        />
        <SettingRow
          v-for="field in numberFields"
          :id="`ticket-setting-${field.key}`"
          :key="field.key"
          field
          :label-for="`ticket-setting-${field.key}`"
          :label="t(`tickets.settings.${field.key}`)"
          :hint="t(`tickets.settings.${field.key}Hint`)"
        >
          <input
            :id="`ticket-setting-${field.key}`"
            v-model.number="form[field.key]"
            type="number"
            step="1"
            :min="field.min"
            :max="field.max"
            :disabled="saving"
            class="input w-full"
          />
        </SettingRow>
      </SettingsSection>
      <SettingsSection>
        <SettingToggleRow
          id="tickets-notify"
          v-model="form.notify_on_staff_reply"
          :label="t('tickets.settings.notify')"
          :hint="t('tickets.settings.notifyHint')"
          :disabled="saving"
        />
      </SettingsSection>
    </template>
  </SettingsCard>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import SettingsCard from '@/components/common/settings/SettingsCard.vue'
import SettingsSection from '@/components/common/settings/SettingsSection.vue'
import SettingRow from '@/components/common/settings/SettingRow.vue'
import SettingToggleRow from '@/components/common/settings/SettingToggleRow.vue'
import ContentSkeleton from '@/components/common/ContentSkeleton.vue'
import { useSettingsSaveTarget } from '@/composables/useSettingsSaveRegistry'
import { ticketAPI, saveTicketSettings, type TicketSettings } from '@/api/tickets'
import { useAppStore } from '@/stores/app'
import { extractI18nErrorMessage } from '@/utils/apiError'

// TicketSettingsTab.vue edits support limits and availability using shared
// settings rows and the page save registry. It loads server values before
// enabling edits, validates numeric ranges, and publishes confirmed module
// availability to navigation after saving. Failed saves keep the edited form
// dirty for retry.

const { t } = useI18n()
const appStore = useAppStore()
const form = ref<TicketSettings>()
const loading = ref(false)
const saving = ref(false)
const error = ref('')
const saved = ref('')
const dirty = computed(() => !!form.value && JSON.stringify(form.value) !== saved.value)
const numberFields = [
  { key: 'max_open_tickets', min: 1, max: 100 },
  { key: 'max_attachments', min: 0, max: 10 },
  { key: 'max_attachment_size_mb', min: 1, max: 20 },
  { key: 'auto_expire_hours', min: 0, max: 8760 },
] as const
const valid = computed(
  () =>
    !!form.value &&
    numberFields.every(
      ({ key, min, max }) =>
        Number.isInteger(form.value![key]) && form.value![key] >= min && form.value![key] <= max,
    ),
)

// load reads the persisted ticket settings and records their saved form
// snapshot. Failures leave an error instead of installing default values,
// preventing a failed read from replacing live configuration.
async function load() {
  loading.value = true
  error.value = ''
  try {
    form.value = await ticketAPI(true).config()
    saved.value = JSON.stringify(form.value)
  } catch (err) {
    error.value = extractI18nErrorMessage(err, t, 'tickets.errors', t('tickets.loadFailed'))
  } finally {
    loading.value = false
  }
}
// save validates and persists the complete edited ticket form through its
// dedicated settings API. Success updates the dirty snapshot and navigation;
// failure retains the form and returns false to the page save registry.
async function save(): Promise<boolean> {
  if (!form.value || !valid.value || saving.value) return false
  saving.value = true
  error.value = ''
  // The dedicated settings endpoint owns these support keys; confirmed persistence
  // updates the saved snapshot before publishing module availability to
  // navigation.
  try {
    form.value = await saveTicketSettings({ ...form.value })
    await appStore.syncTicketModuleEnabled(form.value.enabled)
    saved.value = JSON.stringify(form.value)
    appStore.showSuccess(t('tickets.settings.saved'))
    return true
  } catch (err) {
    error.value = extractI18nErrorMessage(err, t, 'tickets.errors', t('tickets.actionFailed'))
    return false
  } finally {
    saving.value = false
  }
}
useSettingsSaveTarget('tickets', { dirty, save, discard: load })
onMounted(load)
</script>
