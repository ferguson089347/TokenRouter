<template>
  <AppLayout>
    <div class="space-y-4">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <p class="max-w-3xl text-sm text-gray-500 dark:text-dark-400">{{
          t('monitor.description')
        }}</p>
        <div class="flex gap-2">
          <button class="btn btn-secondary" :disabled="loading" @click="load">{{
            t('monitor.refresh')
          }}</button>
          <button v-if="admin" class="btn btn-primary" @click="openSettings">{{
            t('monitor.settings')
          }}</button>
        </div>
      </div>
      <div class="card grid gap-3 p-4 sm:grid-cols-2 xl:grid-cols-5">
        <Select v-model="range" :options="rangeOptions" :aria-label="t('monitor.range')" />
        <Select v-model="platform" :options="platformOptions" :aria-label="t('monitor.platform')" />
        <Select v-model="group" :options="groupOptions" :aria-label="t('monitor.group')" />
        <Select v-model="model" :options="modelOptions" :aria-label="t('monitor.model')" />
        <Select v-model="groupBy" :options="groupByOptions" :aria-label="t('monitor.groupBy')" />
      </div>
      <p
        v-if="error"
        role="alert"
        class="rounded-control bg-red-50 p-4 text-sm text-red-700 dark:bg-red-500/10 dark:text-red-400"
        >{{ error }}</p
      >
      <template v-if="snapshot">
        <div
          class="flex flex-wrap items-center gap-3 text-xs text-gray-500 dark:text-dark-400"
          role="status"
        >
          <span>{{ t('monitor.updated') }}: {{ date(snapshot.coverage.computed_at) }}</span>
          <span>{{
            t('monitor.lag', { seconds: snapshot.coverage.aggregation_lag_seconds })
          }}</span>
          <span v-if="!snapshot.coverage.coverage_complete">{{ t('monitor.partial') }}</span>
          <span v-if="snapshot.coverage.bootstrap?.active">{{
            t('monitor.backfill', {
              percent: snapshot.coverage.bootstrap.progress_percent.toFixed(0),
            })
          }}</span>
        </div>
        <div class="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
          <div class="card p-4"
            ><p class="text-xs text-gray-500 dark:text-dark-400">{{ t('monitor.health') }}</p
            ><p class="mt-2 text-xl font-semibold"
              >{{ t(`monitor.states.${snapshot.health.overall}`) }}
              <span v-if="snapshot.health.score != null" class="text-sm text-gray-500">{{
                snapshot.health.score.toFixed(0)
              }}</span></p
            ></div
          >
          <div class="card p-4"
            ><p class="text-xs text-gray-500 dark:text-dark-400">{{ t('monitor.success') }}</p
            ><p class="mt-2 text-xl font-semibold">{{
              snapshot.health.overall === 'unknown' ? '—' : percent(snapshot.metrics.success_rate)
            }}</p></div
          >
          <div class="card p-4"
            ><p class="text-xs text-gray-500 dark:text-dark-400">{{ t('monitor.ttft') }}</p
            ><p class="mt-2 text-xl font-semibold">{{
              milliseconds(snapshot.metrics.ttft.p50_ms)
            }}</p></div
          >
          <div class="card p-4"
            ><p class="text-xs text-gray-500 dark:text-dark-400">{{ t('monitor.cache') }}</p
            ><p class="mt-2 text-xl font-semibold">{{
              snapshot.health.cache === 'unknown' ? '—' : percent(snapshot.metrics.cache_rate)
            }}</p></div
          >
        </div>
      </template>
      <div class="card overflow-hidden">
        <div
          class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-200 p-4 dark:border-dark-700"
        >
          <p class="text-sm font-medium">{{ t('monitor.history') }}</p>
          <div class="flex gap-3 text-xs"
            ><span v-for="state in healthStates" :key="state" class="flex items-center gap-1"
              ><span class="h-2 w-2 rounded-full" :class="healthColor(state)" />{{
                t(`monitor.states.${state}`)
              }}</span
            ></div
          >
        </div>
        <div v-if="loading && !matrix" class="p-8 text-center text-sm text-gray-500">{{
          t('monitor.loading')
        }}</div>
        <div
          v-else-if="!matrix?.items.length"
          class="p-8 text-center text-sm text-gray-500 dark:text-dark-400"
          >{{ t('monitor.empty') }}</div
        >
        <div v-else class="overflow-x-auto">
          <table class="table w-full">
            <thead
              ><tr
                ><th>{{ t('monitor.channel') }}</th
                ><th>{{ t('monitor.health') }}</th
                ><th>{{ t('monitor.success') }}</th
                ><th>{{ t('monitor.ttft') }}</th
                ><th>{{ t('monitor.history') }}</th
                ><th v-if="admin">{{ t('monitor.requests') }}</th></tr
              ></thead
            >
            <tbody
              ><tr
                v-for="row in matrix.items"
                :key="`${row.platform}/${row.group_id}/${row.model}`"
              >
                <td
                  ><div class="font-medium">{{ row.group_name || row.platform }}</div
                  ><div class="text-xs text-gray-500 dark:text-dark-400">{{
                    [row.platform, row.model].filter(Boolean).join(' · ')
                  }}</div></td
                >
                <td>{{ t(`monitor.states.${row.health.overall}`) }}</td>
                <td>{{
                  row.health.overall === 'unknown' ? '—' : percent(row.metrics.success_rate)
                }}</td>
                <td>{{ milliseconds(row.metrics.ttft.p50_ms) }}</td>
                <td
                  ><div class="flex min-w-48 gap-1"
                    ><span
                      v-for="bucket in row.buckets"
                      :key="bucket.bucket_start"
                      class="h-7 min-w-1 flex-1 rounded-control"
                      :class="healthColor(bucket.health.overall)"
                      :title="`${date(bucket.bucket_start)} · ${t(`monitor.states.${bucket.health.overall}`)}`" /></div
                ></td>
                <td v-if="admin">{{ row.metrics.request_count }}</td>
              </tr></tbody
            >
          </table>
        </div>
      </div>
      <div class="card overflow-hidden">
        <div class="flex gap-2 border-b border-gray-200 p-3 dark:border-dark-700">
          <button
            v-for="item in tabs"
            :key="item"
            class="btn"
            :class="tab === item ? 'btn-primary' : 'btn-secondary'"
            @click="tab = item"
            >{{ t(`monitor.tabs.${item}`) }}</button
          >
        </div>
        <div class="overflow-x-auto">
          <table v-if="tab === 'models'" class="table w-full"
            ><thead
              ><tr
                ><th>{{ t('monitor.model') }}</th
                ><th>{{ t('monitor.platform') }}</th
                ><th>{{ t('monitor.success') }}</th
                ><th>{{ t('monitor.ttft') }}</th
                ><th>{{ t('monitor.cache') }}</th></tr
              ></thead
            ><tbody
              ><tr v-for="row in models" :key="`${row.platform}/${row.model}`"
                ><td>{{ row.model }}</td
                ><td>{{ row.platform }}</td
                ><td>{{
                  row.health.overall === 'unknown' ? '—' : percent(row.metrics.success_rate)
                }}</td
                ><td>{{ milliseconds(row.metrics.ttft.p50_ms) }}</td
                ><td>{{ percent(row.metrics.cache_rate) }}</td></tr
              ></tbody
            ></table
          >
          <table v-else-if="tab === 'errors'" class="table w-full"
            ><thead
              ><tr
                ><th>{{ t('monitor.category') }}</th
                ><th>{{ t('monitor.share') }}</th
                ><th>{{ t('monitor.scoring') }}</th
                ><th v-if="admin">{{ t('monitor.details') }}</th></tr
              ></thead
            ><tbody
              ><tr v-for="row in errors" :key="row.category"
                ><td>{{ t(`monitor.categories.${row.category}`) }}</td
                ><td>{{ percent(row.rate) }}</td
                ><td>{{ row.ignored ? t('monitor.ignored') : t('monitor.included') }}</td
                ><td v-if="admin"
                  ><p
                    v-for="(detail, index) in row.details"
                    :key="index"
                    class="max-w-xl break-words text-xs"
                    >{{ detail.message }}</p
                  ></td
                ></tr
              ></tbody
            ></table
          >
          <table v-else class="table w-full"
            ><thead
              ><tr
                ><th>{{ t('monitor.user') }}</th
                ><th>{{ t('monitor.success') }}</th
                ><th>{{ t('monitor.ttft') }}</th></tr
              ></thead
            ><tbody
              ><tr v-for="row in users" :key="row.rank"
                ><td>{{
                  row.is_self
                    ? t('monitor.me')
                    : admin
                      ? row.username || row.email || row.display_label
                      : t('monitor.otherUser', { rank: row.rank })
                }}</td
                ><td>{{ percent(row.metrics.success_rate) }}</td
                ><td>{{ milliseconds(row.metrics.ttft.p50_ms) }}</td></tr
              ></tbody
            ></table
          >
        </div>
      </div>
      <BaseDialog
        :show="draft !== null"
        :title="t('monitor.settings')"
        width="wide"
        @close="draft = null"
      >
        <form v-if="draft" class="space-y-5" @submit.prevent="saveSettings">
          <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{
            error
          }}</p>
          <label class="block space-y-1 text-sm"
            >{{ t('monitor.visibleGroupIds') }}
            <input
              :value="draft.group_ids.join(', ')"
              class="input"
              inputmode="numeric"
              pattern="[0-9, ]*"
              :placeholder="t('monitor.allGroups')"
              @change="
                draft.group_ids = splitModels(($event.target as HTMLInputElement).value).map(Number)
              "
            />
          </label>
          <div class="flex items-center justify-between"
            ><label for="monitor-enabled" class="input-label">{{ t('monitor.enabled') }}</label
            ><Toggle id="monitor-enabled" v-model="draft.enabled"
          /></div>
          <div class="grid gap-4 sm:grid-cols-2">
            <label class="space-y-1 text-sm"
              >{{ t('monitor.refreshInterval')
              }}<Select
                v-model="draft.refresh_interval_seconds"
                :options="[
                  { value: 60, label: t('monitor.minute') },
                  { value: 300, label: t('monitor.fiveMinutes') },
                ]"
            /></label>
            <label class="space-y-1 text-sm"
              >{{ t('monitor.minimumSample')
              }}<input
                v-model.number="draft.health_thresholds.minimum_sample"
                class="input"
                type="number"
                min="1"
                max="10000"
                required
            /></label>
            <label v-for="field in thresholdFields" :key="field" class="space-y-1 text-sm"
              >{{ t(`monitor.thresholds.${field}`)
              }}<input
                v-model.number="draft.health_thresholds[field]"
                class="input"
                type="number"
                min="0"
                :max="field.includes('rate') || field.includes('weight') ? 1 : undefined"
                :step="field.includes('rate') || field.includes('weight') ? 0.01 : 1"
                required
            /></label>
          </div>
          <fieldset class="space-y-3"
            ><legend class="mb-2 text-sm font-medium">{{ t('monitor.platforms') }}</legend
            ><div
              v-for="item in draft.platforms"
              :key="item.platform"
              class="grid items-center gap-2 sm:grid-cols-3"
              ><label class="flex items-center gap-2 text-sm"
                ><input v-model="item.enabled" type="checkbox" />{{ item.platform }}</label
              ><input
                :value="item.models.join(', ')"
                class="input sm:col-span-2"
                :aria-label="`${item.platform} ${t('monitor.displayModels')}`"
                :placeholder="t('monitor.allModels')"
                @input="
                  item.models = splitModels(($event.target as HTMLInputElement).value)
                " /></div
          ></fieldset>
          <fieldset class="grid gap-2 sm:grid-cols-2"
            ><legend class="mb-2 text-sm font-medium">{{ t('monitor.ignoreCategories') }}</legend
            ><label
              v-for="category in MONITOR_ERROR_CATEGORIES"
              :key="category"
              class="flex items-center gap-2 text-sm"
              ><input
                v-model="draft.ignored_error_categories"
                type="checkbox"
                :value="category"
              />{{ t(`monitor.categories.${category}`) }}</label
            ></fieldset
          >
          <div class="flex justify-end gap-2"
            ><button class="btn btn-secondary" type="button" @click="draft = null">{{
              t('monitor.cancel')
            }}</button
            ><button class="btn btn-primary" type="submit" :disabled="saving">{{
              t('monitor.save')
            }}</button></div
          >
        </form>
      </BaseDialog>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Select from '@/components/common/Select.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Toggle from '@/components/common/Toggle.vue'
import * as api from '@/api/channelMonitor'
import { MONITOR_ERROR_CATEGORIES } from '@/api/channelMonitor'
import type {
  HealthState,
  MonitorConfig,
  MonitorDimensions,
  MonitorErrorRow,
  MonitorMatrixGroupBy,
  MonitorMatrixResponse,
  MonitorModelRow,
  MonitorRange,
  MonitorSnapshot,
  MonitorUserRow,
} from '@/api/channelMonitor'
import { extractApiErrorMessage } from '@/utils/apiError'

/**
 * ChannelMonitorView.vue presents Kedaya V2 passive health and fixed time buckets.
 * Requests load a consistent selected scope through authenticated endpoints;
 * administrators can replace versioned configuration while users see only
 * authorized groups and anonymized rankings. No page action calls a provider.
 *
 * Filter changes abort the previous generation, and publication checks the
 * current controller so a late response cannot replace newer selections.
 * Refresh waits until the preceding generation settles. Unmount cancels the
 * request and timer together, preventing a disposed page from scheduling work.
 */

/** props selects the protected administrator API and local settings controls. */
const props = withDefaults(defineProps<{ admin?: boolean }>(), { admin: false })
/** t resolves the active language for data labels and error messages. */
const { t } = useI18n()

// These selections own the current page scope and restart its cancellable load.
const range = ref<MonitorRange>('90m')
const platform = ref<string | null>('')
const group = ref<number | null>(0)
const model = ref<string | null>('')
const groupBy = ref<MonitorMatrixGroupBy>('platform_group')
const tab = ref('models')

// Published results belong to the latest successful request generation. Draft
// configuration is a private copy retained after failed or conflicting saves.
const snapshot = ref<MonitorSnapshot | null>(null)
const matrix = ref<MonitorMatrixResponse | null>(null)
const dimensions = ref<MonitorDimensions | null>(null)
const models = ref<MonitorModelRow[]>([])
const errors = ref<MonitorErrorRow[]>([])
const users = ref<MonitorUserRow[]>([])
const draft = ref<MonitorConfig | null>(null)
const loading = ref(false)
const saving = ref(false)
const error = ref('')

// Resource ownership remains local to this mounted page. A disposed page cannot
// publish a late result or install another refresh timer after cancellation.
let controller: AbortController | undefined
let timer: ReturnType<typeof setTimeout> | undefined
let disposed = false
// Settings requests share a mounted lifetime and are canceled on route disposal.
const settingsController = new AbortController()

// Fixed options match the backend range and grouping catalogs. Threshold keys
// edit the server's ratio, millisecond, and normalized-weight configuration.
const healthStates: HealthState[] = ['healthy', 'warning', 'critical', 'unknown']
const tabs = ['models', 'errors', 'users']
const thresholdFields = [
  'warning_error_rate',
  'critical_error_rate',
  'target_ttft_ms',
  'warning_ttft_ms',
  'critical_ttft_ms',
  'warning_cache_rate',
  'critical_cache_rate',
  'error_weight',
  'ttft_weight',
  'cache_weight',
] as const
/** rangeOptions labels the four fixed historical windows in the active language. */
const rangeOptions = computed(() =>
  (['90m', '24h', '7d', '30d'] as const).map((value) => ({
    value,
    label: t(`monitor.ranges.${value}`),
  })),
)
/** groupByOptions labels each supported platform, group, and model aggregation. */
const groupByOptions = computed(() =>
  (['platform', 'platform_group', 'platform_model', 'platform_group_model'] as const).map(
    (value) => ({ value, label: t(`monitor.groupings.${value}`) }),
  ),
)
/** platformOptions keeps a selectable all-platform scope beside observed dimensions. */
const platformOptions = computed(() => [
  { value: '', label: t('monitor.allPlatforms') },
  ...(dimensions.value?.platforms ?? []).map((item) => ({ value: item.value, label: item.label })),
])
/** groupOptions exposes server-authorized groups and an unrestricted selection label. */
const groupOptions = computed(() => [
  { value: 0, label: t('monitor.allGroups') },
  ...(dimensions.value?.groups ?? []).map((item) => ({ value: item.id, label: item.name })),
])
/** modelOptions exposes catalog models returned within the configured viewer scope. */
const modelOptions = computed(() => [
  { value: '', label: t('monitor.allModels') },
  ...(dimensions.value?.models ?? []).map((item) => ({ value: item.value, label: item.label })),
])

/** percent formats a server ratio using one displayed fractional percentage digit. */
function percent(value: number): string {
  return `${(value * 100).toFixed(1)}%`
}
/** milliseconds formats measured latency while retaining missing observations as unknown. */
function milliseconds(value: number | null): string {
  return value == null ? '—' : `${value.toLocaleString()} ms`
}
/** date formats observed server timestamps using the viewer's browser timezone. */
function date(value: string): string {
  return value && !value.startsWith('0001-') ? new Date(value).toLocaleString() : '—'
}
/** healthColor maps health categories to shared semantic status color classes. */
function healthColor(value: HealthState): string {
  return {
    healthy: 'bg-emerald-500',
    warning: 'bg-amber-400',
    critical: 'bg-red-500',
    unknown: 'bg-gray-200 dark:bg-dark-700',
  }[value]
}
/** splitModels normalizes comma-separated display model names and removes empty entries. */
function splitModels(value: string): string[] {
  return value
    .split(',')
    .map((item) => item.trim())
    .filter(Boolean)
}

/**
 * load refreshes the selected scope and schedules the next configured refresh.
 * Every invocation cancels its predecessor and publishes only while it still
 * owns the active controller. Failed loads retain existing observations and
 * report an error; canceled generations cannot overwrite that newer state.
 * All endpoint responses are awaited before publication so filters, metrics,
 * and ranking rows switch together. The finally block schedules another load
 * only for the surviving mounted generation, including after recoverable errors.
 */
async function load(): Promise<void> {
  // Cancel old network work and its refresh before taking ownership, ensuring
  // delayed results cannot change the newly selected filter generation.
  controller?.abort()
  clearTimeout(timer)
  const current = new AbortController()
  controller = current
  loading.value = true
  error.value = ''
  const filter = {
    range: range.value,
    platforms: platform.value ? [platform.value] : [],
    groupIds: group.value ? [group.value] : [],
    models: model.value ? [model.value] : [],
  }
  try {
    const [summary, choices, history, modelRows, errorRows, userRows] = await Promise.all([
      api.getSnapshot(filter, props.admin, current.signal),
      api.getDimensions(filter, props.admin, current.signal),
      api.getMatrix(filter, groupBy.value, props.admin, current.signal),
      api.getModels(filter, props.admin, current.signal),
      api.getErrors(filter, props.admin, current.signal),
      api.getUsers(filter, props.admin, current.signal),
    ])
    if (controller !== current || disposed) return
    snapshot.value = summary
    dimensions.value = choices
    matrix.value = history
    models.value = modelRows.items
    errors.value = errorRows.items
    users.value = userRows.items
  } catch (cause) {
    // Aborted generations belong to changed filters or disposal; preserve the
    // current generation's error banner instead of reporting obsolete failures.
    if (!current.signal.aborted && controller === current)
      error.value = extractApiErrorMessage(cause, t('monitor.loadFailed'))
  } finally {
    if (controller === current && !disposed) {
      loading.value = false
      timer = setTimeout(
        () => void load(),
        (snapshot.value?.config.refresh_interval_seconds ?? 300) * 1000,
      )
    }
  }
}

/** openSettings loads a fresh private configuration draft for an administrator.
 * Failed reads leave the editor closed and publish the localized error banner;
 * disabled monitoring remains editable through its separate configuration endpoint.
 */
async function openSettings(): Promise<void> {
  try {
    const value = await api.getConfig(settingsController.signal)
    if (!disposed) draft.value = value
  } catch (cause) {
    error.value = extractApiErrorMessage(cause, t('monitor.loadFailed'))
  }
}

/** saveSettings persists the versioned draft and refreshes observations afterward.
 * Failed writes retain the editor contents, including after HTTP 409 conflicts;
 * closing the editor does not imply that an in-flight save was canceled.
 */
async function saveSettings(): Promise<void> {
  if (!draft.value || saving.value) return
  saving.value = true
  try {
    await api.updateConfig(draft.value, settingsController.signal)
    if (disposed) return
    draft.value = null
    await load()
  } catch (cause) {
    error.value = extractApiErrorMessage(cause, t('monitor.saveFailed'))
  } finally {
    saving.value = false
  }
}

// One watcher owns scope refreshes. Disposal cancels its network generation and
// pending timer so route changes cannot leave a polling page alive in memory.
watch([range, platform, group, model, groupBy, () => props.admin], () => void load(), {
  immediate: true,
})
onBeforeUnmount(() => {
  disposed = true
  controller?.abort()
  settingsController.abort()
  clearTimeout(timer)
})
</script>
