<template>
  <BaseDialog
    :show="show"
    :title="t('intelligence.selfTest')"
    :close-on-escape="!submitting"
    @close="close"
  >
    <form id="intelligence-self-test" class="space-y-4" @submit.prevent="submit">
      <p class="text-sm text-gray-500">{{ t('intelligence.selfTestHint') }}</p>
      <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
      <div>
        <label for="self-test-key" class="input-label">{{ t('intelligence.selectApiKey') }}</label>
        <Select
          id="self-test-key"
          v-model="selectedKeyID"
          :options="keyOptions"
          :disabled="submitting || loading"
          searchable
          :placeholder="t('intelligence.selectApiKeyPlaceholder')"
        />
        <p v-if="!loading && !availableKeys.length" class="input-hint">{{
          t('intelligence.noSelfTestKeys')
        }}</p>
      </div>
    </form>
    <template #footer>
      <button type="button" class="btn btn-secondary" :disabled="submitting" @click="close">{{
        t('common.cancel')
      }}</button>
      <button
        type="submit"
        form="intelligence-self-test"
        class="btn btn-primary"
        :disabled="!valid || submitting || loading"
        >{{ t(submitting ? 'common.loading' : 'intelligence.startSelfTest') }}</button
      >
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import { intelligenceAPI, type IntelligenceRun, type IntelligenceTest } from '@/api/intelligence'
import { keysAPI } from '@/api/keys'
import type { ApiKey } from '@/api/keys.types'
import { extractApiErrorMessage } from '@/utils/apiError'

// IntelligenceSelfTestDialog.vue lets authenticated users select a personal Key
// and start a public drawing with the channel's system execution settings.
// Credential options display names and channel labels from the page's authorized
// site histories. The request carries the selected identifier; the service resolves
// its secret and copies the first enabled site drawing's execution parameters.
//
// Opening loads personal Keys across paginated reads. Closure and disposal abort
// those reads and invalidate late completions before clearing transient records.
// Current site histories determine which channels support self-testing. A pending
// submit keeps the editor open until its queue record or diagnostic arrives,
// while loading or an unavailable selection disables another submission.

// Dialog state and candidate credentials belong to this mounted editor. The
// parent refreshes public histories after the submitted observation is accepted.
const props = defineProps<{ show: boolean; tests: IntelligenceTest[] }>()
const emit = defineEmits<{ close: []; submitted: [run: IntelligenceRun] }>()
const { t } = useI18n()
const keys = ref<ApiKey[]>([])
const loading = ref(false)
const submitting = ref(false)
const error = ref('')
const selectedKeyID = ref(0)
let controller: AbortController | undefined
let revision = 0
const channels = computed(
  () =>
    new Map(
      props.tests
        .filter((test) => test.source !== 'user' && test.benchmark === 'drawing')
        .map((test) => [test.group_id, test.group_name]),
    ),
)
const availableKeys = computed(() => keys.value.filter(eligible))
const keyOptions = computed(() =>
  availableKeys.value.map((key) => ({
    value: key.id,
    label: `${key.name} · ${(key.routing_mode === 'single'
      ? [{ group_id: key.group_id ?? 0 }]
      : (key.groups ?? [])
    )
      .map((binding) => channels.value.get(binding.group_id))
      .filter(Boolean)
      .join(', ')}`,
  })),
)
const selectedKey = computed(() =>
  availableKeys.value.find((key) => key.id === selectedKeyID.value),
)
const valid = computed(() => !!selectedKey.value)

// eligible reports whether a current personal Key can fund a drawing.
// Group availability, lifecycle, expiry, and quota are checked for display;
// the service validates ownership and these conditions again before queue admission.
function eligible(key: ApiKey): boolean {
  return (
    key.status === 'active' &&
    !key.team_id &&
    key.scope !== 'team' &&
    (!key.expires_at || Date.parse(key.expires_at) > Date.now()) &&
    (key.quota <= 0 || key.quota_used < key.quota) &&
    (key.routing_mode === 'single'
      ? channels.value.has(key.group_id ?? 0)
      : (key.groups ?? []).some((binding) => channels.value.has(binding.group_id)))
  )
}

// clearCandidates aborts the editor's Key reads and invalidates late completions.
// Removing transient Key objects on closure keeps saved secret text outside later
// dialog sessions even though submission uses only the chosen credential identifier.
function clearCandidates() {
  controller?.abort()
  ++revision
  keys.value = []
  loading.value = false
}

// load reads every page of the current user's personal Keys for channel filtering.
// Each open owns a revision and abort signal. Responses are published only while
// that editor remains selected, so closing and reopening cannot restore stale credentials.
// Keys become selectable after every page has been read, and the current page's
// authorized site histories determine their eligible channels. A failed read keeps
// submission disabled and reports the current editor's error for correction.
async function load() {
  clearCandidates()
  const current = revision
  controller = new AbortController()
  const signal = controller.signal
  loading.value = true
  error.value = ''
  try {
    const collected: ApiKey[] = []
    for (let page = 1; ; page++) {
      const response = await keysAPI.list(
        page,
        100,
        { scope: 'personal', status: 'active' },
        { signal },
      )
      if (current !== revision || signal.aborted) return
      collected.push(...response.items)
      if (page >= response.pages || !response.items.length) break
    }
    keys.value = collected
  } catch (err) {
    if (current === revision && !signal.aborted)
      error.value = extractApiErrorMessage(err, t('intelligence.loadApiKeysFailed'))
  } finally {
    if (current === revision) loading.value = false
  }
}

// close dismisses an idle self-test editor and clears its credential candidates.
// An accepted or failed submission finishes before closure becomes available,
// preventing duplicate paid requests from overlapping another editor session.
function close() {
  if (submitting.value) return
  clearCandidates()
  emit('close')
}

// submit queues a public drawing using the selected personal Key identifier.
// Validation and the pending flag block duplicate clicks. Acceptance refreshes the
// parent history; errors keep the dialog's settings available for correction.
async function submit() {
  if (!valid.value || submitting.value) return
  submitting.value = true
  error.value = ''
  try {
    const run = await intelligenceAPI.selfTest({ api_key_id: selectedKeyID.value })
    clearCandidates()
    emit('submitted', run)
    emit('close')
  } catch (err) {
    error.value = extractApiErrorMessage(err, t('intelligence.runFailed'))
  } finally {
    submitting.value = false
  }
}
watch(
  () => props.show,
  (show) => {
    if (show) {
      selectedKeyID.value = 0
      void load()
    } else clearCandidates()
  },
  { immediate: true },
)
onBeforeUnmount(clearCandidates)
</script>
