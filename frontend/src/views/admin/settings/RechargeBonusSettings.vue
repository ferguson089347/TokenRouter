<template>
  <SettingsCard
    :title="t('rechargeBonus.settingsTitle')"
    :description="t('rechargeBonus.settingsHint')"
    data-testid="recharge-bonus-settings"
  >
    <ContentSkeleton v-if="loading && !loaded" variant="form" :rows="4" />
    <div v-else-if="!loaded" class="space-y-4">
      <p class="text-sm text-gray-500">{{ t('rechargeBonus.loadFailed') }}</p>
      <button type="button" class="btn btn-secondary" :disabled="loading" @click="loadSettings">{{
        t('common.retry')
      }}</button>
    </div>
    <fieldset v-else :disabled="saving || loading" class="min-w-0 space-y-6">
      <SettingsSection>
        <RuleListEditor
          :items="form.recharge_bonus_tiers"
          :title="t('rechargeBonus.tiers')"
          :add-label="t('rechargeBonus.addTier')"
          :empty-text="t('rechargeBonus.emptyTiers')"
          :max-items="20"
          :reorderable="false"
          test-id="recharge-bonus-tiers"
          @add="addTier"
          @remove="form.recharge_bonus_tiers.splice($event, 1)"
        >
          <template #row="{ item, index }">
            <div class="grid grid-cols-2 gap-4">
              <div
                ><label :for="`${uid}-tier-min-${index}`" class="input-label">{{
                  t('rechargeBonus.threshold')
                }}</label
                ><input
                  :id="`${uid}-tier-min-${index}`"
                  v-model.number="item.min_amount"
                  type="number"
                  min="0"
                  step="0.01"
                  class="input"
                  required
              /></div>
              <div
                ><label :for="`${uid}-tier-percent-${index}`" class="input-label">{{
                  t('rechargeBonus.tierReward')
                }}</label
                ><input
                  :id="`${uid}-tier-percent-${index}`"
                  v-model.number="item.bonus_amount"
                  type="number"
                  min="0"
                  step="0.01"
                  class="input"
                  required
              /></div>
            </div>
          </template>
        </RuleListEditor>
      </SettingsSection>

      <SettingsSection :title="t('rechargeBonus.period')" :hint="t('rechargeBonus.openPeriod')">
        <template #actions>
          <button type="button" class="btn btn-secondary btn-sm" @click="newCampaign">
            {{ t('rechargeBonus.newCampaign') }}
          </button>
        </template>
        <p class="input-hint">{{ t('rechargeBonus.newCampaignHint') }}</p>
        <p class="input-hint">{{
          t('rechargeBonus.editTimezone', { timezone: browserTimezone })
        }}</p>
        <div class="grid gap-4 md:grid-cols-2">
          <div
            ><label :for="`${uid}-start`" class="input-label">{{
              t('rechargeBonus.startsAt')
            }}</label
            ><DateTimePicker
              :id="`${uid}-start`"
              :model-value="dateValue(form.recharge_bonus_activity.starts_at)"
              @update:model-value="setDate(form.recharge_bonus_activity, 'starts_at', $event)"
          /></div>
          <div
            ><label :for="`${uid}-end`" class="input-label">{{ t('rechargeBonus.endsAt') }}</label
            ><DateTimePicker
              :id="`${uid}-end`"
              :model-value="dateValue(form.recharge_bonus_activity.ends_at)"
              @update:model-value="setDate(form.recharge_bonus_activity, 'ends_at', $event)"
          /></div>
        </div>
        <SettingRow
          :id="`${uid}-timezone`"
          field
          :label="t('rechargeBonus.timezone')"
          :label-for="`${uid}-timezone`"
          ><Select
            :id="`${uid}-timezone`"
            v-model="form.recharge_bonus_activity.timezone"
            :options="timezoneOptions"
        /></SettingRow>
        <RuleListEditor
          :items="form.recharge_bonus_activity.time_windows"
          :title="t('rechargeBonus.windows')"
          :hint="t('rechargeBonus.timeWindowsHint')"
          :add-label="t('rechargeBonus.addWindow')"
          :max-items="20"
          :reorderable="false"
          variant="card"
          test-id="recharge-bonus-windows"
          @add="addWindow"
          @remove="form.recharge_bonus_activity.time_windows.splice($event, 1)"
        >
          <template #row="{ item, index }">
            <div class="space-y-4 rounded-surface border border-gray-200 p-4 dark:border-dark-600">
              <SettingToggleRow
                :id="`${uid}-window-enabled-${index}`"
                v-model="item.enabled"
                :label="t('rechargeBonus.enabled')"
              />
              <div class="grid gap-4 md:grid-cols-3">
                <div
                  ><label :for="`${uid}-window-start-${index}`" class="input-label">{{
                    t('rechargeBonus.startsAt')
                  }}</label
                  ><DateTimePicker
                    :id="`${uid}-window-start-${index}`"
                    :model-value="dateValue(item.starts_at)"
                    @update:model-value="setDate(item, 'starts_at', $event)"
                /></div>
                <div
                  ><label :for="`${uid}-window-end-${index}`" class="input-label">{{
                    t('rechargeBonus.endsAt')
                  }}</label
                  ><DateTimePicker
                    :id="`${uid}-window-end-${index}`"
                    :model-value="dateValue(item.ends_at)"
                    @update:model-value="setDate(item, 'ends_at', $event)"
                /></div>
                <div
                  ><label :for="`${uid}-window-percent-${index}`" class="input-label">{{
                    t('rechargeBonus.percent')
                  }}</label
                  ><input
                    :id="`${uid}-window-percent-${index}`"
                    v-model.number="item.bonus_percent"
                    type="number"
                    min="0"
                    max="1000"
                    step="0.01"
                    class="input"
                    required
                /></div>
              </div>
            </div>
          </template>
        </RuleListEditor>
      </SettingsSection>

      <SettingsSection>
        <SettingToggleRow
          :id="`${uid}-publish`"
          v-model="form.recharge_bonus_activity.page_enabled"
          :label="t('rechargeBonus.publish')"
          :hint="t('rechargeBonus.landingHint')"
        />
        <div
          ><label :for="`${uid}-slug`" class="input-label">{{ t('rechargeBonus.slug') }}</label
          ><input
            :id="`${uid}-slug`"
            v-model="form.recharge_bonus_activity.slug"
            class="input"
            maxlength="80"
            placeholder="double11-2026"
          />
        </div>
        <LocalizedFieldsEditor
          v-model="localization"
          :source="localization.source"
          :source-locale="getLocale()"
          :fields="copyFields"
        />
        <RuleListEditor
          :items="form.recharge_bonus_activity.faqs"
          :title="t('rechargeBonus.faqs')"
          :add-label="t('rechargeBonus.addFAQ')"
          :max-items="12"
          :reorderable="false"
          test-id="recharge-bonus-faqs"
          @add="addFAQ"
          @remove="removeFAQ"
        >
          <template #row="{ item }">
            <LocalizedFieldsEditor
              v-model="localization"
              :source="localization.source"
              :source-locale="getLocale()"
              :fields="faqFields(item.id)"
            />
          </template>
        </RuleListEditor>
        <RouterLink
          v-if="form.recharge_bonus_activity.slug"
          :to="`/activities/${form.recharge_bonus_activity.slug}`"
          class="inline-flex items-center gap-2 text-sm text-primary-600 hover:text-primary-700"
          target="_blank"
          >{{ t('rechargeBonus.viewActivity') }}<Icon name="externalLink" size="sm"
        /></RouterLink>
      </SettingsSection>

      <SettingsSection :title="t('rechargeBonus.preview')" :hint="t('rechargeBonus.previewHint')">
        <SettingRow
          :id="`${uid}-cumulative`"
          field
          :label="t('rechargeBonus.cumulativeAmount')"
          :label-for="`${uid}-cumulative`"
        >
          <input
            :id="`${uid}-cumulative`"
            v-model.number="previewCumulativeAmount"
            class="input"
            type="number"
            min="0"
            step="0.01"
          />
        </SettingRow>
        <SettingRow
          :id="`${uid}-amount`"
          field
          :label="t('rechargeBonus.amount')"
          :label-for="`${uid}-amount`"
          ><input
            :id="`${uid}-amount`"
            v-model.number="previewAmount"
            class="input"
            type="number"
            min="1"
            step="0.01"
        /></SettingRow>
        <RechargeBonusBreakdown :quote="preview" estimated />
      </SettingsSection>
    </fieldset>
  </SettingsCard>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, useId } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'
import { useNow } from '@vueuse/core'
import { adminPaymentAPI } from '@/api/admin/payment'
import type { AdminPaymentConfig } from '@/api/admin/payment'
import type { RechargeBonusSettings } from '@/api/payment.types'
import SettingsCard from '@/components/common/settings/SettingsCard.vue'
import SettingsSection from '@/components/common/settings/SettingsSection.vue'
import SettingRow from '@/components/common/settings/SettingRow.vue'
import SettingToggleRow from '@/components/common/settings/SettingToggleRow.vue'
import ContentSkeleton from '@/components/common/ContentSkeleton.vue'
import DateTimePicker from '@/components/common/DateTimePicker.vue'
import LocalizedFieldsEditor from '@/components/common/LocalizedFieldsEditor.vue'
import type { LocalizedField } from '@/components/common/LocalizedFieldsEditor.vue'
import RuleListEditor from '@/components/common/RuleListEditor.vue'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'
import RechargeBonusBreakdown from '@/components/payment/RechargeBonusBreakdown.vue'
import { useDirtyTracker } from '@/composables/useDirtyTracker'
import { useSettingsSaveTarget } from '@/composables/useSettingsSaveRegistry'
import { useAppStore } from '@/stores'
import { getLocale } from '@/i18n'
import { newContentID, originalContent, type LocalizedUpdate } from '@/i18n/content'
import { updateSource, withDefaultSource } from '@/i18n/contentEdit'
import { extractApiErrorMessage } from '@/utils/apiError'
import { formatDateTimeLocalInput, parseDateTimeLocalInput } from '@/utils/format'
import {
  emptyRechargeBonusActivity,
  matchRechargeBonusTier,
  quoteRechargeBonus,
} from '@/utils/payment/rechargeBonus'

// RechargeBonusSettings.vue edits the payment-owned tier gifts, extra schedule,
// and public activity presentation through the settings page's shared save bar.
// The editor pairs cumulative purchased-credit thresholds with fixed reward
// totals and keeps its monetary preview on the same shared helper as
// the purchase and public pages. Date controls edit browser-local instants;
// the selected activity timezone controls public date presentation.
//
// Initial reads complete before editable controls appear. Each replacement draft read
// cancels its predecessor, and unmount cancels the current read so a late result
// cannot publish into a departed panel. Saving captures an independent draft,
// persists the gift rules and their versioned content together, reads accepted
// values, and records a clean baseline after applying that response. Other payment settings retain their
// existing page controller and independently registered save operation.

// Parent draft changes to the recharge multiplier remain visible in this preview.
const props = defineProps<{
  multiplier?: number // Parent draft recharge multiplier.
}>()

// Section state owns the gift draft, initial-load gate, and persistence feedback.
const { t } = useI18n()
const app = useAppStore()
const uid = useId()
const browserTimezone = Intl.DateTimeFormat().resolvedOptions().timeZone
const now = useNow({ interval: 1000 })
const loading = ref(false)
const loaded = ref(false)
const saving = ref(false)
const previewAmount = ref(100)
const previewCumulativeAmount = ref(0)
const storedMultiplier = ref(1)
const form = reactive<RechargeBonusSettings>({
  recharge_bonus_tiers: [],
  recharge_bonus_notice: '',
  recharge_bonus_activity: emptyRechargeBonusActivity(),
})
// The complete copy block shares one revision; FAQ keys use stable identities so
// deleting a preceding row cannot move its translations onto a different entry.
const localization = ref<LocalizedUpdate<Record<string, string>>>(
  originalContent({ title: '', subtitle: '', rules: '', notice: '' }, getLocale()),
)
const { dirty, markClean } = useDirtyTracker({
  form: () => form,
  localization: () => localization.value,
})

// Copy field definitions keep every public operating text in the existing
// translation editor while numeric rules and route identity remain independent.
const copyFields = computed<LocalizedField<Record<string, string>>[]>(() => [
  { key: 'title', label: t('rechargeBonus.title') },
  { key: 'subtitle', label: t('rechargeBonus.subtitle'), multiline: true, rows: 2 },
  { key: 'notice', label: t('rechargeBonus.notice'), multiline: true, rows: 2 },
  { key: 'rules', label: t('rechargeBonus.rules'), multiline: true, rows: 4 },
])

// Read ownership covers refresh replacement and component teardown; saves persist
// captured values through the shared registry even if a page navigation follows.
let readController: AbortController | undefined
let disposed = false

// Timezone choices include the saved site timezone so uncommon zones remain editable.
const timezoneOptions = computed(() =>
  [
    ...new Set([
      form.recharge_bonus_activity.timezone,
      browserTimezone,
      'UTC',
      'Asia/Shanghai',
      'Asia/Tokyo',
      'America/New_York',
      'Europe/London',
    ]),
  ]
    .filter(Boolean)
    .map((value) => ({ value, label: value })),
)
const preview = computed(() =>
  quoteRechargeBonus(
    form,
    previewAmount.value,
    props.multiplier ?? storedMultiplier.value,
    now.value.getTime(),
    {
      campaign_id: form.recharge_bonus_activity.campaign_id,
      cumulative_amount: Math.max(0, previewCumulativeAmount.value || 0),
      tier_reward_total:
        matchRechargeBonusTier(form.recharge_bonus_tiers, previewCumulativeAmount.value)
          ?.bonus_amount ?? 0,
    },
  ),
)

useSettingsSaveTarget('rechargeBonus', { dirty, save: saveSettings, discard: loadSettings })
onMounted(loadSettings)
onBeforeUnmount(() => {
  // Cancel the section-owned read before marking the panel disposed so its late
  // completion cannot replace a future page instance's draft or baseline.
  disposed = true
  readController?.abort()
})

// applyConfig copies accepted gift data into this panel's independently owned draft.
// Missing fields select empty upstream-compatible defaults, allowing older server
// responses and unconfigured sites to retain ordinary recharge behavior.
//
// The editable content block supplies original text even when the administrator's
// display language selects a translation in the ordinary activity fields. Legacy
// responses acquire FAQ identities before their source map is assembled, so each
// question keeps its text under the same key on the next save. Copying accepted
// data also separates reactive edits from the response used as the clean baseline.
function applyConfig(value: AdminPaymentConfig): void {
  form.recharge_bonus_tiers = structuredClone(value.recharge_bonus_tiers ?? [])
  form.recharge_bonus_notice = value.recharge_bonus_notice ?? ''
  form.recharge_bonus_activity = {
    ...emptyRechargeBonusActivity(),
    ...structuredClone(value.recharge_bonus_activity ?? {}),
  }
  form.recharge_bonus_activity.time_windows ??= []
  form.recharge_bonus_activity.faqs ??= []
  form.recharge_bonus_activity.campaign_id ||= newContentID()
  for (const faq of form.recharge_bonus_activity.faqs) faq.id ||= newContentID()
  const activity = form.recharge_bonus_activity
  localization.value = withDefaultSource(
    structuredClone(
      value.recharge_bonus_localization ??
        originalContent(
          {
            title: activity.title,
            subtitle: activity.subtitle,
            rules: activity.rules,
            notice: form.recharge_bonus_notice,
            ...Object.fromEntries(
              activity.faqs.flatMap((faq) => [
                [`faq.${faq.id}.question`, faq.question],
                [`faq.${faq.id}.answer`, faq.answer],
              ]),
            ),
          },
          getLocale(),
        ),
    ),
    getLocale(),
  )
  storedMultiplier.value = value.balance_recharge_multiplier
}

// loadSettings replaces the draft from the latest completed section-owned read.
// Discard uses this operation deliberately; normal refresh controls stay disabled
// during a read. Aborted requests and unmounted panels leave draft publication untouched.
//
// Starting a replacement read cancels the previous controller, and completion
// checks cancellation before applying any data or recording the clean snapshot.
// The controller identity check in cleanup leaves a newer request's loading state
// intact. A failed read retains existing edits and reports its error while this
// panel is mounted, allowing the administrator to retry from the same page.
async function loadSettings(): Promise<void> {
  // Replacing the request cancels its predecessor so an older response cannot
  // overwrite the settings returned by the administrator's latest reload action.
  readController?.abort()
  const controller = new AbortController()
  readController = controller
  loading.value = true
  try {
    const { data } = await adminPaymentAPI.getConfig(controller.signal)
    if (controller.signal.aborted || disposed) return
    applyConfig(data)
    loaded.value = true
    await nextTick()
    markClean()
  } catch (error) {
    if (!controller.signal.aborted && !disposed)
      app.showError(extractApiErrorMessage(error, t('rechargeBonus.loadFailed')))
  } finally {
    if (readController === controller && !disposed) loading.value = false
  }
}

// saveSettings persists a captured gift draft and updates its accepted baseline.
// The shared save registry receives false on validation or persistence failure;
// edited values remain available for correction and another save attempt.
//
// The payload carries original presentation fields and their localized revision
// together so the server can validate and commit one offer. The write endpoint
// returns an acknowledgement, so a subsequent read supplies normalized dates,
// assigned revisions, and accepted translations before the panel becomes clean.
// If that read fails after persistence, the draft remains dirty; retrying keeps
// version conflicts visible instead of recording an unconfirmed local baseline.
async function saveSettings(): Promise<boolean> {
  saving.value = true
  try {
    const payload = JSON.parse(JSON.stringify(form)) as RechargeBonusSettings
    const content = JSON.parse(JSON.stringify(localization.value)) as LocalizedUpdate<
      Record<string, string>
    >
    // Submit source display fields alongside their content revision so ordinary
    // activity validation and atomic localization persistence inspect the same copy.
    payload.recharge_bonus_localization = content
    payload.recharge_bonus_notice = content.source.notice ?? ''
    payload.recharge_bonus_activity.title = content.source.title ?? ''
    payload.recharge_bonus_activity.subtitle = content.source.subtitle ?? ''
    payload.recharge_bonus_activity.rules = content.source.rules ?? ''
    for (const faq of payload.recharge_bonus_activity.faqs) {
      faq.question = content.source[`faq.${faq.id}.question`] ?? ''
      faq.answer = content.source[`faq.${faq.id}.answer`] ?? ''
    }
    await adminPaymentAPI.updateConfig(payload)
    if (disposed) return true
    // The write endpoint acknowledges persistence without returning configuration.
    // Read the normalized values before replacing the draft and recording its baseline.
    const { data } = await adminPaymentAPI.getConfig()
    if (!disposed) {
      applyConfig(data)
      await nextTick()
      markClean()
    }
    return true
  } catch (error) {
    app.showError(extractApiErrorMessage(error, t('rechargeBonus.saveFailed')))
    return false
  } finally {
    saving.value = false
  }
}

// addTier appends a cumulative threshold and fixed total reward for editing.
function addTier(): void {
  // New rows start one hundred purchased-credit units above the largest configured threshold.
  const threshold = Math.max(0, ...form.recharge_bonus_tiers.map((tier) => tier.min_amount)) + 100
  // Draft rewards advance by eleven balance units, matching the first reference tier.
  const reward = Math.max(0, ...form.recharge_bonus_tiers.map((tier) => tier.bonus_amount)) + 11
  form.recharge_bonus_tiers.push({ min_amount: threshold, bonus_amount: reward })
}

// newCampaign creates a draft activity with independent cumulative reward tracking.
// Existing rules remain available for reuse, while dates and extra windows clear
// for the next period. The shared save bar can discard this draft before persistence.
function newCampaign(): void {
  form.recharge_bonus_activity.campaign_id = newContentID()
  form.recharge_bonus_activity.page_enabled = false
  form.recharge_bonus_activity.starts_at = undefined
  form.recharge_bonus_activity.ends_at = undefined
  form.recharge_bonus_activity.time_windows = []
}

// addWindow appends an editable extra interval inside the configured activity endpoints.
// An open activity starts the draft interval now and ends it one day later;
// administrators adjust overlapping draft windows before saving their configuration.
function addWindow(): void {
  const startsAt = form.recharge_bonus_activity.starts_at ?? new Date().toISOString()
  // Default open-ended drafts cover one day for convenient calendar editing.
  const endsAt =
    form.recharge_bonus_activity.ends_at ??
    new Date(Date.parse(startsAt) + 24 * 60 * 60 * 1000).toISOString()
  form.recharge_bonus_activity.time_windows.push({
    enabled: true,
    starts_at: startsAt,
    ends_at: endsAt,
    bonus_percent: 5,
  })
}

// addFAQ appends a blank question with stable translation identity and source fields.
// New rows invalidate the shared source revision's translation approvals, keeping
// incomplete translations from describing a changed list after it is saved.
//
// The activity list and localized map receive the same generated ID in this
// synchronous edit. Subsequent controls address that ID even if another row is
// removed. Empty question and answer values remain editable locally; the server
// requires both parts when the administrator saves the completed activity.
function addFAQ(): void {
  const id = newContentID()
  form.recharge_bonus_activity.faqs.push({ id, question: '', answer: '' })
  localization.value = updateSource(
    localization.value,
    { ...localization.value.source, [`faq.${id}.question`]: '', [`faq.${id}.answer`]: '' },
    getLocale(),
  )
}

// removeFAQ removes a row and its source fields from the shared content block.
// Translation review becomes stale after the source list changes; existing
// translation keys remain associated with their original IDs until server normalization.
//
// Removing the corresponding source keys advances the content change seen by
// revision preparation. Surviving questions keep their IDs and translations,
// while the server removes submitted translation keys owned by deleted entries.
// The row editor supplies the current nonnegative row index; an index beyond
// the final row returns before changing the localized source map.
function removeFAQ(index: number): void {
  const [faq] = form.recharge_bonus_activity.faqs.splice(index, 1)
  if (!faq) return
  const source = { ...localization.value.source }
  delete source[`faq.${faq.id}.question`]
  delete source[`faq.${faq.id}.answer`]
  localization.value = updateSource(localization.value, source, getLocale())
}

// faqFields names the question and answer controls for one stable FAQ identity.
function faqFields(id: string | undefined): LocalizedField<Record<string, string>>[] {
  return [
    { key: `faq.${id}.question`, label: t('rechargeBonus.question') },
    { key: `faq.${id}.answer`, label: t('rechargeBonus.answer'), multiline: true, rows: 2 },
  ]
}

// dateValue renders an optional stored instant for the shared browser-local date control.
// Missing or invalid timestamps become empty controls; their submitted instants
// remain UTC strings interpreted independently of the activity's display timezone.
function dateValue(value?: string): string {
  return formatDateTimeLocalInput(value ? Date.parse(value) / 1000 : null)
}

// setDate writes a picker observation into the supplied draft's UTC endpoint.
// Clearing the control omits the endpoint; valid local calendar values use the
// browser timezone, which the section identifies beside its date controls.
function setDate(
  target: { starts_at?: string; ends_at?: string },
  field: 'starts_at' | 'ends_at',
  value: string,
): void {
  const seconds = parseDateTimeLocalInput(value)
  if (!value) delete target[field]
  else if (seconds !== null) target[field] = new Date(seconds * 1000).toISOString()
}
</script>
