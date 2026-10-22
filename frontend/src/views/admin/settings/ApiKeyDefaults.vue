<template>
  <SettingsCard
    :title="t('keys.routing.defaultsTitle')"
    :description="t('keys.routing.defaultsHint')"
  >
    <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
    <div class="space-y-2">
      <label class="input-label" for="routing-cooldown">{{ t('keys.routing.cooldown') }}</label>
      <input
        id="routing-cooldown"
        v-model.number="cooldown"
        type="number"
        min="0"
        max="3600"
        class="input"
        :disabled="!loaded"
      />
      <p class="input-hint">{{ t('keys.routing.cooldownHint') }}</p>
    </div>
    <RuleListEditor
      v-if="loaded"
      :items="rows"
      :item-key="(row) => row.localId"
      reorderable
      :add-label="t('keys.routing.addChannel')"
      :max="20"
      @add="add"
      @remove="remove"
      @move="move"
    >
      <template #row="{ item }">
        <Select
          v-model="item.groupId"
          :options="options"
          searchable
          :placeholder="t('keys.selectGroup')"
        />
      </template>
    </RuleListEditor>
  </SettingsCard>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import SettingsCard from '@/components/common/settings/SettingsCard.vue'
import RuleListEditor from '@/components/common/RuleListEditor.vue'
import Select from '@/components/common/Select.vue'
import { apiClient } from '@/api/client'
import { groupsAPI } from '@/api/admin/groups'
import type { AdminGroup } from '@/api/groups.types'
import { useSettingsSaveTarget } from '@/composables/useSettingsSaveRegistry'
import { extractApiErrorMessage } from '@/utils/apiError'

// ApiKeyDefaults.vue edits the administrator channel order used by inherited
// API keys. The shared settings save registry owns persistence and discard, while
// this panel retains its draft across hidden tabs. Failed initial loading keeps
// editing unavailable so an unread configuration cannot become an empty write.
//
// Rows keep stable local identities while users change channel priority. Saving
// copies the ordered identifiers before awaiting the server, then records that
// submitted snapshot as the clean baseline. Edits made during the write stay in
// the visible draft and remain dirty for the registry's next save. Each request
// replaces the entire default list, so duplicate or unselected channels reject
// locally before the shared settings save bar can report success.

// ChannelRow keeps editor identity stable while channel selections move in priority order.
interface ChannelRow {
  localId: number // Stable editor row identifier.
  groupId: number | null // Selected channel identifier.
}

// Loaded groups supply labels; rows and saved identifiers belong to this mounted
// panel and participate in the parent settings page's dirty-state tracking.
const { t } = useI18n()
const groups = ref<AdminGroup[]>([])
const rows = ref<ChannelRow[]>([])
const loaded = ref(false)
const error = ref('')
const saved = ref('[]')
const cooldown = ref(60)
const savedCooldown = ref(60)
let sequence = 0
const identifiers = computed(() => rows.value.map((row) => row.groupId))
const dirty = computed(
  () =>
    loaded.value &&
    (JSON.stringify(identifiers.value) !== saved.value || cooldown.value !== savedCooldown.value),
)
const options = computed(() =>
  groups.value.map((group) => ({ value: group.id, label: group.display_name || group.name })),
)

// add appends one unselected channel requiring validation before defaults can save.
function add() {
  rows.value.push({ localId: ++sequence, groupId: null })
}

// remove deletes the requested channel from the pending administrator default order.
function remove(index: number) {
  rows.value.splice(index, 1)
}

// move reorders one channel while retaining its stable editor row identifier.
function move(from: number, to: number) {
  const [row] = rows.value.splice(from, 1)
  if (row) rows.value.splice(to, 0, row)
}

// load reads defaults and selectable groups before publishing an editable snapshot.
// A failed read leaves the prior draft intact and reports its error; initial
// failure keeps the form disabled until a later successful reload.
async function load() {
  error.value = ''
  try {
    const [response, available] = await Promise.all([
      apiClient.get<{ group_ids: number[]; cooldown_seconds?: number }>('/admin/api-keys/defaults'),
      groupsAPI.getAll(),
    ])
    groups.value = available
    cooldown.value = response.data.cooldown_seconds ?? 60
    savedCooldown.value = cooldown.value
    rows.value = response.data.group_ids.map((groupId) => ({ localId: ++sequence, groupId }))
    saved.value = JSON.stringify(identifiers.value)
    loaded.value = true
  } catch (failure) {
    error.value = extractApiErrorMessage(failure, t('keys.failedToLoad'))
  }
}

// save validates the complete ordered selection before replacing server defaults.
// Success records the submitted snapshot as its clean baseline. Edits made during
// the write remain dirty, and failure keeps the pending order available so
// administrators can correct it or retry through the shared settings save bar.
// The saved baseline uses the copied submitted identifiers, while the visible
// draft continues to belong to the editor during persistence. This lets the
// registry detect a subsequent change even when the preceding write succeeds.
async function save(): Promise<boolean> {
  if (!loaded.value) return false
  if (
    identifiers.value.some((id) => !id) ||
    new Set(identifiers.value).size !== identifiers.value.length
  ) {
    error.value = t('keys.composite.groupDuplicate')
    return false
  }
  if (!Number.isInteger(cooldown.value) || cooldown.value < 0 || cooldown.value > 3600) return false
  const groupIDs = [...identifiers.value]
  const cooldownSeconds = cooldown.value
  try {
    await apiClient.put('/admin/api-keys/defaults', {
      group_ids: groupIDs,
      cooldown_seconds: cooldownSeconds,
    })
    saved.value = JSON.stringify(groupIDs)
    savedCooldown.value = cooldownSeconds
    error.value = ''
    return true
  } catch (failure) {
    error.value = extractApiErrorMessage(failure, t('keys.failedToUpdate'))
    return false
  }
}
useSettingsSaveTarget('api-key-defaults', { dirty, save, discard: load })
onMounted(load)
</script>
