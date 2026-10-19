<template>
  <AppLayout>
    <TablePageLayout>
      <template #filters>
        <div class="flex flex-wrap items-center justify-between gap-2">
          <form class="flex min-w-0 flex-1 items-center gap-2" @submit.prevent="applyFilters">
            <div class="input-icon-wrap min-w-0 flex-1 sm:flex-none sm:w-64">
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
            <FilterDropdown :active-count="activeFilterCount" @reset="clearFilters">
              <FilterField :label="t('remotePool.columns.groups')">
                <Select
                  v-model="groupID"
                  :options="groupOptions"
                  searchable
                  @change="applyFilters"
                />
              </FilterField>
              <FilterField :label="t('remotePool.allPlans')">
                <Select v-model="planType" :options="planOptions" @change="applyFilters" />
              </FilterField>
              <FilterField :label="t('remotePool.columns.status')">
                <Select v-model="status" :options="statusOptions" @change="applyFilters" />
              </FilterField>
            </FilterDropdown>
          </form>
          <div class="flex items-center gap-2">
            <button
              type="button"
              class="btn btn-secondary btn-icon"
              :title="t('common.refresh')"
              :aria-label="t('common.refresh')"
              :disabled="loading"
              @click="refresh"
            >
              <Icon name="refresh" size="sm" :class="{ 'animate-spin': loading }" />
            </button>
          </div>
        </div>
        <p v-if="snapshotStatus.snapshot_at" class="mt-2 text-xs text-gray-500 dark:text-dark-400">
          {{ t('remotePool.snapshotAt') }}: {{ formatTime(snapshotStatus.snapshot_at) }}
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
            <div class="font-medium text-gray-900 dark:text-dark-50">{{ row.display_name }}</div>
            <div class="text-xs text-gray-500 dark:text-dark-400">#{{ row.id }}</div>
          </template>
          <template #cell-platform="{ row }">
            <div>{{ row.platform }} · {{ row.auth_type }}</div>
            <span class="badge badge-gray mt-1">{{ row.plan_label || row.plan_type || '—' }}</span>
          </template>
          <template #cell-capacity="{ row }">
            <div class="tabular-nums">
              {{ row.current_concurrency }} / {{ row.concurrency > 0 ? row.concurrency : '∞' }}
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
                  width: `${Math.max(0, Math.min(100, (row.current_concurrency / row.concurrency) * 100))}%`
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
            <div class="space-y-1 tabular-nums">
              <div v-for="group in row.groups" :key="group.id" :title="group.name">
                {{ group.rate_multiplier }}x
              </div>
            </div>
          </template>
          <template #cell-usage_windows="{ row }">
            <div class="space-y-1 text-xs">
              <div v-for="window in row.usage_windows" :key="window.key">
                {{ window.label || window.key }}:
                {{ window.utilization == null ? '—' : `${window.utilization}%` }}
              </div>
              <span v-if="!row.usage_windows?.length">—</span>
            </div>
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
import DataTable from '@/components/common/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import Select from '@/components/common/Select.vue'
import FilterDropdown from '@/components/common/FilterDropdown.vue'
import FilterField from '@/components/common/FilterField.vue'
import Icon from '@/components/icons/Icon.vue'
import { SEARCH_DEBOUNCE_MS } from '@/constants/ui'
import {
  remoteAccountPoolAPI,
  type RemotePoolAccount,
  type RemotePoolOptions,
  type RemotePoolSnapshotStatus
} from '@/api/admin/remoteAccountPool'

// RemoteAccountPoolView.vue presents the user's remote account directory using
// current local group names and multipliers. The page reads user endpoints for
// account rows and filter choices, and freshness notices explain when the server
// retains a complete snapshot after remote synchronization fails.
//
// Each directory load replaces the previous request controller. Only the current
// request may publish rows or loading state, so rapid filter changes cannot show
// results belonging to an older selection. The initial load obtains choices before
// rows from the shared snapshot. Leaving the page cancels reads and stops minute
// polling, while backend synchronization saves consumed refresh tokens independently.

// Directory state tracks visible rows, snapshot freshness, local filters, and request ownership for the authenticated user's pool page.
const { t } = useI18n()
const loading = ref(true)
const error = ref('')
const items = ref<RemotePoolAccount[]>([])
const options = ref<RemotePoolOptions>({
  groups: [],
  plan_types: [],
  stale: false,
  auth_invalid: false
})
const snapshotStatus = ref<RemotePoolSnapshotStatus>({ stale: false, auth_invalid: false })
const total = ref(0)
const page = ref(1)
const pageSize = ref(50)
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
    'last_used_at'
  ].map((key) => ({ key, label: t(`remotePool.columns.${key}`), sortable: false }))
)
const groupOptions = computed(() => [
  { value: '', label: t('remotePool.allGroups') },
  ...options.value.groups.map((group) => ({
    value: String(group.id),
    label: `${group.name} · ${group.rate_multiplier}x`
  }))
])
const planOptions = computed(() => [
  { value: '', label: t('remotePool.allPlans') },
  ...options.value.plan_types
])
const statusOptions = computed(() => [
  { value: '', label: t('remotePool.allStatuses') },
  ...statuses.map((value) => ({ value, label: statusLabel(value) }))
])
const activeFilterCount = computed(
  () => [groupID.value, planType.value, status.value].filter(Boolean).length
)
const debouncedSearch = useDebounceFn(applyFilters, SEARCH_DEBOUNCE_MS)

/** statusLabel translates known states and displays unknown source states verbatim. */
function statusLabel(value: string) {
  return statuses.includes(value) ? t(`remotePool.status.${value}`) : value
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
        status: status.value || undefined
      },
      request.signal
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
