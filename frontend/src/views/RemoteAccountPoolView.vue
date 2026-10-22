<template>
  <AppLayout>
    <TablePageLayout>
      <template #filters>
        <form class="flex flex-wrap items-center gap-2" @submit.prevent="applyFilters">
          <div class="input-icon-wrap min-w-0 grow basis-full sm:basis-48 sm:max-w-64">
            <Icon name="search" size="md" class="input-icon text-gray-400 dark:text-dark-400" />
            <input
              v-model="search"
              type="search"
              class="input input-has-icon"
              :placeholder="t('remotePool.search')"
              :aria-label="t('remotePool.search')"
              @input="debouncedSearch"
            />
          </div>
          <Select
            v-model="planType"
            class="min-w-0 grow basis-40 sm:grow-0 sm:w-44"
            :options="planOptions"
            :aria-label="t('remotePool.accountType')"
            searchable
            @change="applyFilters"
          >
            <template #selected="{ option }">
              <span
                v-if="option?.value"
                class="badge gap-1"
                :class="planBadge(String(option.value), String(option.label)).classes"
              >
                <Icon
                  :name="planBadge(String(option.value), String(option.label)).icon"
                  size="xs"
                  :animate-on-hover="false"
                />{{ option.label }}
              </span>
              <span v-else>{{ t('remotePool.allTypes') }}</span>
            </template>
            <template #option="{ option, selected }">
              <span
                v-if="option.value"
                class="badge gap-1"
                :class="planBadge(String(option.value), String(option.label)).classes"
              >
                <Icon
                  :name="planBadge(String(option.value), String(option.label)).icon"
                  size="xs"
                  :animate-on-hover="false"
                />{{ option.label }}
              </span>
              <span v-else>{{ option.label }}</span>
              <Icon
                v-if="selected"
                name="check"
                size="sm"
                class="ml-auto text-primary-500"
                :animate-on-hover="false"
              />
            </template>
          </Select>
          <Select
            v-model="status"
            class="min-w-0 grow basis-36 sm:grow-0 sm:w-40"
            :options="statusOptions"
            :aria-label="t('remotePool.columns.status')"
            @change="applyFilters"
          />
          <Select
            v-model="groupID"
            class="min-w-0 grow basis-44 sm:grow-0 sm:w-52"
            :options="groupOptions"
            :aria-label="t('remotePool.columns.groups')"
            searchable
            @change="applyFilters"
          />
          <button
            v-if="activeFilterCount"
            type="button"
            class="btn btn-secondary"
            @click="clearFilters"
          >
            <Icon name="x" size="sm" />{{ t('remotePool.clear') }}
          </button>
          <button
            type="button"
            class="btn btn-secondary btn-icon ml-auto"
            :title="t('common.refresh')"
            :aria-label="t('common.refresh')"
            :disabled="loading"
            @click="refresh"
          >
            <Icon name="refresh" size="sm" :class="{ 'animate-spin': loading }" />
          </button>
        </form>
        <p v-if="snapshotStatus.snapshot_at" class="mt-2 text-xs text-gray-500 dark:text-dark-400">
          {{ t('remotePool.snapshotAt') }}:
          {{ formatTime(snapshotStatus.snapshot_at) }}
        </p>
        <p
          v-if="error"
          role="alert"
          class="mt-4 rounded-surface border border-red-200 bg-red-50 p-4 text-sm text-red-700 dark:border-red-500/20 dark:bg-red-500/10 dark:text-red-300"
        >
          {{ error }}
        </p>
        <p
          v-if="snapshotStatus.stale || snapshotStatus.auth_invalid"
          role="status"
          class="mt-4 rounded-surface border border-amber-200 bg-amber-50 p-4 text-sm text-amber-800 dark:border-amber-500/20 dark:bg-amber-500/10 dark:text-amber-200"
        >
          {{ t('remotePool.staleHint') }}
        </p>
      </template>
      <template #table>
        <DataTable
          column-order-storage-key="remote-account-pool-column-order"
          :columns="columns"
          :data="items"
          :loading="loading"
          row-key="id"
        >
          <template #cell-display_name="{ row }">
            <div class="flex items-center gap-3">
              <span
                class="flex h-9 w-9 shrink-0 items-center justify-center rounded-control bg-primary-50 text-primary-600 dark:bg-primary-500/10 dark:text-primary-300"
              >
                <Icon name="server" size="md" :animate-on-hover="false" />
              </span>
              <div class="min-w-0 self-baseline">
                <div
                  class="whitespace-nowrap font-mono text-xs font-semibold tracking-wide text-gray-900 dark:text-dark-50"
                >
                  {{ row.id }}
                </div>
                <div class="mt-1 truncate text-xs text-gray-500 dark:text-dark-400">
                  {{ row.display_name || t('remotePool.accountNode') }}
                </div>
              </div>
            </div>
          </template>
          <template #cell-platform="{ row }">
            <div>{{ row.platform }} · {{ row.auth_type }}</div>
            <span
              class="badge mt-1 gap-1"
              :class="planBadge(row.plan_type, row.plan_label).classes"
            >
              <Icon
                :name="planBadge(row.plan_type, row.plan_label).icon"
                size="xs"
                :animate-on-hover="false"
              />{{ row.plan_label || row.plan_type || '—' }}
            </span>
          </template>
          <template #cell-capacity="{ row }">
            <div class="tabular-nums">
              {{ row.current_concurrency }} /
              {{ row.concurrency > 0 ? row.concurrency : '∞' }}
            </div>
            <div
              v-if="row.concurrency > 0"
              class="mt-1 h-1.5 w-24 overflow-hidden rounded-full bg-gray-100 dark:bg-dark-700"
            >
              <div
                class="h-full rounded-full"
                :class="
                  row.current_concurrency >= row.concurrency ? 'bg-red-500' : 'bg-primary-500'
                "
                :style="{
                  width: `${Math.max(0, Math.min(100, (row.current_concurrency / row.concurrency) * 100))}%`,
                }"
              />
            </div>
          </template>
          <template #cell-status="{ row }"
            ><span
              class="badge"
              :class="row.status === 'active' ? 'badge-success' : 'badge-gray'"
              >{{ statusLabel(row.status) }}</span
            ></template
          >
          <template #cell-groups="{ row }">
            <div class="flex max-w-xs flex-wrap gap-1">
              <span v-for="group in row.groups" :key="group.id" class="badge badge-gray">{{
                group.name
              }}</span>
            </div>
          </template>
          <template #cell-rate_multiplier="{ row }">
            <div class="flex flex-wrap gap-2">
              <div
                v-for="group in row.groups"
                :key="group.id"
                class="inline-flex items-center gap-2 rounded-control border border-primary-100 bg-primary-50/70 px-3 py-2 dark:border-primary-500/20 dark:bg-primary-500/10"
                :title="t('remotePool.groupRate', { group: group.name })"
              >
                <Icon
                  name="bolt"
                  size="sm"
                  class="shrink-0 text-primary-500 dark:text-primary-400"
                  :animate-on-hover="false"
                />
                <div class="min-w-0">
                  <div
                    class="whitespace-nowrap tabular-nums text-primary-700 dark:text-primary-300"
                  >
                    <span class="text-base font-semibold">{{
                      formatMultiplier(group.rate_multiplier)
                    }}</span
                    ><span class="ml-1 text-xs">×</span>
                  </div>
                  <div
                    class="max-w-32 truncate text-xs text-primary-700/70 dark:text-primary-300/70"
                  >
                    {{ group.name }}
                  </div>
                </div>
              </div>
            </div>
          </template>
          <template #cell-usage_windows="{ row }">
            <div
              v-if="row.usage_windows?.length"
              class="inline-flex flex-col gap-2 rounded-control border border-gray-100 bg-gray-50/70 px-3 py-2 dark:border-dark-600 dark:bg-dark-800/60"
            >
              <div v-for="(window, index) in row.usage_windows" :key="window.key">
                <div
                  v-if="
                    typeof window.utilization === 'number' && Number.isFinite(window.utilization)
                  "
                  role="progressbar"
                  :aria-label="window.label || window.key"
                  :aria-valuenow="Math.max(0, Math.min(100, window.utilization))"
                  :aria-valuetext="`${window.utilization}%`"
                  :aria-valuemin="0"
                  :aria-valuemax="100"
                >
                  <UsageProgressBar
                    :label="window.label || window.key"
                    :utilization="window.utilization"
                    :color="Number(index) % 2 === 0 ? 'indigo' : 'emerald'"
                    label-width="auto"
                  />
                </div>
                <div
                  v-else
                  class="flex items-center gap-2 text-xs text-gray-400 dark:text-dark-400"
                >
                  <span class="rounded-compact bg-gray-100 px-1 font-medium dark:bg-dark-700">{{
                    window.label || window.key
                  }}</span>
                  <span>{{ t('remotePool.usageUnknown') }}</span>
                </div>
              </div>
            </div>
            <span v-else class="text-xs text-gray-400 dark:text-dark-400">{{
              t('remotePool.usageUnknown')
            }}</span>
          </template>
          <template #cell-last_used_at="{ row }">{{ formatTime(row.last_used_at) }}</template>
        </DataTable>
      </template>
      <template #pagination>
        <Pagination
          v-if="total > 0"
          :page="page"
          :page-size="pageSize"
          :total="total"
          @update:page="changePage"
          @update:page-size="changePageSize"
        />
      </template>
    </TablePageLayout>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useDebounceFn } from '@vueuse/core'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/table/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'
import UsageProgressBar from '@/components/provider/UsageProgressBar.vue'
import type { IconName } from '@/components/icons/registry'
import { SEARCH_DEBOUNCE_MS } from '@/constants/ui'
import {
  remoteAccountPoolAPI,
  type RemotePoolAccount,
  type RemotePoolOptions,
  type RemotePoolSnapshotStatus,
} from '@/api/admin/remoteAccountPool'

// RemoteAccountPoolView.vue presents the user's remote account directory using
// current local group names and multipliers. Account rows use site-local references
// and tier labels; utilization bars share the provider page's warning thresholds. The page reads user endpoints for
// account rows and filter choices, and freshness notices explain when the server
// retains a complete snapshot after remote synchronization fails.
//
// Each directory load replaces the previous request controller. Only the current
// request may publish rows or loading state, so rapid filter changes cannot show
// results belonging to an older selection. The initial load obtains choices before
// rows from the shared snapshot. Leaving the page cancels reads and stops minute
// polling, while backend synchronization saves consumed refresh tokens independently.

/** PlanBadge defines a source account tier's visual treatment in rows and choices. */
interface PlanBadge {
  classes: string // light and dark palette
  icon: IconName // account tier identifying icon
}

// planBadges assigns distinct colors and symbols to source tiers while retaining the source's displayed labels and filter values.
const planBadges: Record<string, PlanBadge> = {
  promax: {
    classes: 'bg-amber-100 text-amber-800 dark:bg-amber-500/15 dark:text-amber-300',
    icon: 'sparkles',
  },
  pro: {
    classes: 'bg-violet-100 text-violet-700 dark:bg-violet-500/15 dark:text-violet-300',
    icon: 'sparkles',
  },
  plus: {
    classes: 'bg-sky-100 text-sky-700 dark:bg-sky-500/15 dark:text-sky-300',
    icon: 'plus',
  },
  team: {
    classes: 'bg-indigo-100 text-indigo-700 dark:bg-indigo-500/15 dark:text-indigo-300',
    icon: 'users',
  },
  free: {
    classes: 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-dark-300',
    icon: 'user',
  },
  go: {
    classes: 'bg-emerald-100 text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300',
    icon: 'bolt',
  },
  supergrok: {
    classes: 'bg-rose-100 text-rose-700 dark:bg-rose-500/15 dark:text-rose-300',
    icon: 'bolt',
  },
  max: {
    classes: 'bg-orange-100 text-orange-800 dark:bg-orange-500/15 dark:text-orange-300',
    icon: 'sparkles',
  },
  enterprise: {
    classes: 'bg-cyan-100 text-cyan-800 dark:bg-cyan-500/15 dark:text-cyan-300',
    icon: 'shield',
  },
}

// Directory state tracks visible rows, snapshot freshness, local filters, and request ownership for the authenticated user's pool page.
const { t, locale } = useI18n()
const loading = ref(true)
const error = ref('')
const items = ref<RemotePoolAccount[]>([])
const options = ref<RemotePoolOptions>({
  groups: [],
  plan_types: [],
  stale: false,
  auth_invalid: false,
})
const snapshotStatus = ref<RemotePoolSnapshotStatus>({
  stale: false,
  auth_invalid: false,
})
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)
const search = ref('')
const groupID = ref('')
const planType = ref('')
const status = ref('')
const statuses = ['active', 'rate_limited', 'temporarily_unavailable', 'paused', 'error']
let controller: AbortController | undefined
let disposed = false
let refreshTimer: ReturnType<typeof setInterval> | undefined
const columns = computed(() =>
  [
    'display_name',
    'platform',
    'capacity',
    'status',
    'groups',
    'rate_multiplier',
    'usage_windows',
    'last_used_at',
  ].map((key) => ({
    key,
    label: t(`remotePool.columns.${key}`),
    sortable: false,
  })),
)
const groupOptions = computed(() => [
  { value: '', label: t('remotePool.allGroups') },
  ...options.value.groups.map((group) => ({
    value: String(group.id),
    label: `${group.name} · ${group.rate_multiplier}x`,
  })),
])
const planOptions = computed(() => [
  { value: '', label: t('remotePool.allTypes') },
  ...options.value.plan_types,
])
const statusOptions = computed(() => [
  { value: '', label: t('remotePool.allStatuses') },
  ...statuses.map((value) => ({ value, label: statusLabel(value) })),
])
const activeFilterCount = computed(
  () => [groupID.value, planType.value, status.value].filter(Boolean).length,
)
const debouncedSearch = useDebounceFn(applyFilters, SEARCH_DEBOUNCE_MS)

/** planBadge resolves a source tier's row and filter styling consistently.
 * Source values or labels may vary in casing, spacing, or separators. Unknown
 * tiers use the neutral account treatment while retaining their supplied text.
 */
function planBadge(value: string, label = ''): PlanBadge {
  const key = (value || '').toLowerCase().replace(/[\s_-]/g, '')
  const labelKey = label.toLowerCase().replace(/[\s_-]/g, '')
  // Source-specific identifiers can accompany a recognized display label. Use that label's visual treatment before assigning the neutral unknown-tier style.
  return planBadges[key] || planBadges[labelKey] || { classes: 'badge-gray', icon: 'userCircle' }
}

/** statusLabel translates known states and displays unknown source states verbatim. */
function statusLabel(value: string) {
  return statuses.includes(value) ? t(`remotePool.status.${value}`) : value
}

/** formatMultiplier formats the local group's saved rate for the active locale.
 * Rates retain up to four fractional digits, matching group pricing precision.
 * Missing or invalid measurements use the empty marker rather than a fabricated price.
 */
function formatMultiplier(value: number): string {
  // Group rates use four decimal places in persisted pricing configuration.
  return Number.isFinite(value)
    ? new Intl.NumberFormat(locale.value, { maximumFractionDigits: 4 }).format(value)
    : '—'
}

/** formatTime displays a source timestamp using the browser's local date conventions.
 * Missing values produce the empty-cell marker so unknown source activity and
 * snapshot dates remain distinguishable from actual timestamps in the table.
 */
function formatTime(value?: string | null) {
  return value ? new Date(value).toLocaleString() : '—'
}

/** message extracts a request error or supplies the localized directory failure message. */
function message(cause: unknown) {
  // Nonstandard request failures lack a message. Supply the page's localized explanation so an empty alert cannot hide the failure.
  return cause &&
    typeof cause === 'object' &&
    'message' in cause &&
    typeof cause.message === 'string'
    ? cause.message
    : t('remotePool.loadFailed')
}

/** loadData publishes a directory page for the current filters and optionally refreshes choices.
 * Each call aborts the previous read, and only its controller can publish rows or
 * release loading state. Leaving the page aborts outstanding requests and stops polling.
 *
 * Choices load before rows so both use the backend's shared snapshot. Failed reads
 * retain existing rows and display an error. Disposed pages cannot publish asynchronous
 * results, and only the newest controller can release the table loading indicator.
 */
async function loadData(includeOptions = false) {
  if (disposed) return
  controller?.abort()
  const request = new AbortController()
  controller = request
  loading.value = true
  error.value = ''
  try {
    if (includeOptions) {
      const result = await remoteAccountPoolAPI.options(request.signal)
      if (request.signal.aborted) return
      options.value = result
    }
    const result = await remoteAccountPoolAPI.list(
      {
        page: page.value,
        page_size: pageSize.value,
        search: search.value.trim() || undefined,
        group_id: groupID.value ? Number(groupID.value) : undefined,
        plan_type: planType.value || undefined,
        status: status.value || undefined,
      },
      request.signal,
    )
    if (request.signal.aborted) return
    items.value = result.items
    total.value = result.total
    snapshotStatus.value = result
  } catch (cause) {
    if (!request.signal.aborted) error.value = message(cause)
  } finally {
    if (controller === request) loading.value = false
  }
}

/** refresh reloads filter choices and account rows from the shared backend snapshot. */
function refresh() {
  return loadData(true)
}

/** applyFilters returns to page one before requesting the selected directory filters. */
function applyFilters() {
  page.value = 1
  void loadData()
}

/** changePage requests a different page while retaining the selected directory filters. */
function changePage(value: number) {
  page.value = value
  void loadData()
}

/** changePageSize caps directory pages at the backend limit before reloading page one. */
function changePageSize(value: number) {
  pageSize.value = Math.min(value, 500)
  applyFilters()
}

/** clearFilters removes all structured filters and returns the directory to page one. */
function clearFilters() {
  groupID.value = ''
  planType.value = ''
  status.value = ''
  applyFilters()
}

onMounted(async () => {
  await refresh()
  // Poll while visible and idle because the backend shares one-minute synchronization attempts among all authenticated directory readers.
  if (!disposed)
    refreshTimer = setInterval(() => {
      if (!loading.value && !document.hidden) void refresh()
    }, 60000)
})
onUnmounted(() => {
  disposed = true
  // Cancel active reads and polling when leaving the directory so asynchronous completions cannot update rows belonging to an unmounted page.
  if (refreshTimer) clearInterval(refreshTimer)
  controller?.abort()
})
</script>
