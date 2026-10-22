<template>
  <AppLayout fit-viewport>
    <p
      v-if="disabled"
      role="status"
      class="rounded-surface bg-amber-50 p-4 text-sm text-amber-800 dark:bg-amber-900/20 dark:text-amber-300"
    >
      {{ t('tickets.disabled') }}
    </p>
    <button
      v-if="disabled"
      type="button"
      class="btn btn-secondary mt-3"
      :disabled="loading || closing"
      @click="refresh"
    >
      {{ t('common.refresh') }}
    </button>
    <!-- The shared table shows field cards on narrow screens and a scrollable table on desktop. -->
    <TablePageLayout v-else class="ticket-list">
      <template #filters>
        <div class="flex flex-col-reverse gap-2 lg:flex-row lg:items-start lg:justify-between">
          <form class="flex min-w-0 flex-1 items-center gap-2" @submit.prevent="search">
            <div class="min-w-0 flex-1">
              <label class="sr-only" for="filter-ticket-query">{{ t('tickets.search') }}</label>
              <input
                id="filter-ticket-query"
                v-model="filters.q"
                type="search"
                maxlength="200"
                class="input w-full"
                :placeholder="t('tickets.searchPlaceholder')"
              />
            </div>
            <button type="submit" class="btn btn-secondary shrink-0">
              {{ t('common.search') }}
            </button>
            <FilterDropdown
              keep-mounted
              :active-count="
                [filters.type, filters.status, filters.priority].filter(Boolean).length
              "
              @reset="resetFilters"
            >
              <FilterField :label="t('tickets.type')"
                ><Select v-model="filters.type" :options="typeOptions" @change="search"
              /></FilterField>
              <FilterField :label="t('tickets.status')"
                ><Select v-model="filters.status" :options="statusOptions" @change="search"
              /></FilterField>
              <FilterField :label="t('tickets.priority')"
                ><Select v-model="filters.priority" :options="priorityOptions" @change="search"
              /></FilterField>
            </FilterDropdown>
          </form>
          <div class="flex shrink-0 justify-end gap-2">
            <button
              type="button"
              class="btn btn-secondary"
              :disabled="loading || closing"
              @click="refresh"
            >
              {{ t('common.refresh') }}
            </button>
            <button
              v-if="!admin"
              type="button"
              class="btn btn-primary"
              :disabled="!settings?.enabled"
              @click="showCreate = true"
            >
              {{ t('tickets.create') }}
            </button>
          </div>
        </div>
        <p v-if="error" role="alert" class="mt-3 break-words text-sm text-red-600">
          {{ error }}
        </p>
      </template>
      <template #table>
        <DataTable
          :columns="columns"
          :data="items"
          :loading="loading"
          row-key="id"
          :expandable-actions="false"
          :sticky-first-column="false"
        >
          <template #empty>
            <p class="font-medium text-gray-900 dark:text-white">
              {{ t('tickets.empty') }}
            </p>
            <p class="mt-2 text-sm text-gray-500">
              {{ t(admin ? 'tickets.emptyAdminHint' : 'tickets.emptyHint') }}
            </p>
          </template>
          <template #cell-title="{ row: ticket }">
            <RouterLink
              :to="`${base}/${ticket.id}`"
              :title="ticket.title"
              class="block max-w-full truncate font-medium text-primary-600 hover:underline dark:text-primary-400 lg:max-w-sm"
              >{{ ticket.title }}</RouterLink
            >
            <span class="mt-1 block text-xs text-gray-400">#{{ ticket.id }}</span>
          </template>
          <template #cell-user="{ row: ticket }">
            <p class="truncate lg:max-w-56" :title="ticket.user_name">
              {{ ticket.user_name }}
            </p>
            <p
              class="break-all text-xs text-gray-500 lg:max-w-56 lg:truncate"
              :title="ticket.user_email"
            >
              {{ ticket.user_email }}
            </p>
          </template>
          <template #cell-type="{ row: ticket }">{{ t(`tickets.types.${ticket.type}`) }}</template>
          <template #cell-priority="{ row: ticket }"
            ><TicketBadge kind="priority" :value="ticket.priority"
          /></template>
          <template #cell-status="{ row: ticket }"
            ><TicketBadge
              kind="status"
              :value="ticket.status"
              :closed-by-role="ticket.closed_by_role"
          /></template>
          <template #cell-updated_at="{ row: ticket }"
            ><span class="text-xs text-gray-500">{{
              new Date(ticket.updated_at).toLocaleString()
            }}</span></template
          >
          <template #cell-actions="{ row: ticket }">
            <div v-if="isOpen(ticket)" class="flex justify-end gap-2 lg:justify-start">
              <button
                type="button"
                class="btn btn-secondary min- flex-1 lg:flex-none"
                :disabled="closing"
                @click="requestClose(ticket, 'complete')"
              >
                {{ t('tickets.complete') }}
              </button>
              <button
                type="button"
                class="btn btn-secondary min- flex-1 lg:flex-none"
                :disabled="closing"
                @click="requestClose(ticket, 'cancel')"
              >
                {{ t('tickets.cancel') }}
              </button>
            </div>
            <span v-else class="text-gray-400">-</span>
          </template>
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
    <CreateTicketDialog
      v-if="settings && !admin && !disabled"
      :show="showCreate"
      :settings="settings"
      @close="showCreate = false"
      @created="created"
      @disabled="markDisabled"
    />
    <ConfirmDialog
      :show="!!closeTarget"
      :title="t(closeTarget?.action === 'cancel' ? 'tickets.cancel' : 'tickets.complete')"
      :message="
        t(closeTarget?.action === 'cancel' ? 'tickets.cancelConfirm' : 'tickets.completeConfirm')
      "
      :danger="closeTarget?.action === 'cancel'"
      :loading="closing"
      @cancel="!closing && (closeTarget = null)"
      @confirm="closeTicket"
      ><p class="break-words text-sm text-gray-500">
        {{ closeTarget?.ticket.title }}
      </p></ConfirmDialog
    >
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, watch } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/table/DataTable.vue'
import FilterDropdown from '@/components/common/FilterDropdown.vue'
import FilterField from '@/components/common/FilterField.vue'
import type { Column } from '@/components/common/table/types'
import Select from '@/components/common/Select.vue'
import Pagination from '@/components/common/Pagination.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import TicketBadge from '@/components/tickets/TicketBadge.vue'
import CreateTicketDialog from '@/components/tickets/CreateTicketDialog.vue'
import {
  ticketAPI,
  ticketTypes,
  ticketPriorities,
  ticketStatuses,
  type Ticket,
  type TicketSettings,
} from '@/api/tickets'
import { extractApiErrorCode, extractI18nErrorMessage } from '@/utils/apiError'
import { useAppStore } from '@/stores/app'

// TicketsView.vue displays personal tickets or the administrator support queue
// with the shared table layout. Both entry scopes use the same pagination and
// filters, while administrator rows include the owner identity. Current
// configuration determines whether creation and historical reads are available.
//
// Asynchronous loads use a request generation so earlier filter results cannot
// replace a newer selection. Closing a ticket invalidates pending list reads
// before refreshing its committed state. Module-disable errors clear rows and
// pending dialogs and update public navigation immediately. Creation opens the
// conversation returned by the server, and terminal actions require a
// confirmation dialog.

const props = withDefaults(defineProps<{ admin?: boolean }>(), { admin: false })
const { t } = useI18n()
const appStore = useAppStore()
const router = useRouter()
const base = computed(() => (props.admin ? '/admin/tickets' : '/tickets'))
// Both scopes share columns and cells, with owner identity added for
// administrators while the server retains its scope-specific ordering.
const columns = computed<Column[]>(() => [
  { key: 'title', label: t('tickets.subject') },
  ...(props.admin ? [{ key: 'user', label: t('tickets.user') }] : []),
  { key: 'type', label: t('tickets.type') },
  { key: 'priority', label: t('tickets.priority') },
  { key: 'status', label: t('tickets.status') },
  { key: 'updated_at', label: t('tickets.updatedAt') },
  { key: 'actions', label: t('common.actions') },
])
const filters = reactive({ type: '', status: '', priority: '', q: '' })
// resetFilters clears category, state, and priority selections before reloading
// the first page.
function resetFilters() {
  filters.type = ''
  filters.status = ''
  filters.priority = ''
  search()
}
const items = ref<Ticket[]>([])
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)
const loading = ref(false)
const error = ref('')
const disabled = ref(false)
const settings = ref<TicketSettings>()
const showCreate = ref(false)
const closeTarget = ref<{
  ticket: Ticket
  action: 'cancel' | 'complete'
} | null>(null)
const closing = ref(false)
let requestID = 0
const typeOptions = computed(() => [
  { value: '', label: t('tickets.allTypes') },
  ...ticketTypes.map((value) => ({
    value,
    label: t(`tickets.types.${value}`),
  })),
])
const statusOptions = computed(() => [
  { value: '', label: t('tickets.allStatuses') },
  ...ticketStatuses.map((value) => ({
    value,
    label: t(`tickets.statuses.${value}`),
  })),
])
const priorityOptions = computed(() => [
  { value: '', label: t('tickets.allPriorities') },
  ...ticketPriorities.map((value) => ({
    value,
    label: t(`tickets.priorities.${value}`),
  })),
])

// markDisabled invalidates pending reads and clears unavailable ticket actions
// and rows. It also publishes the confirmed module switch to navigation so
// another open page cannot keep advertising support access.
function markDisabled() {
  ++requestID
  loading.value = false
  disabled.value = true
  void appStore.syncTicketModuleEnabled(false)
  items.value = []
  total.value = 0
  showCreate.value = false
  closeTarget.value = null
  error.value = ''
}
// handleError clears disabled ticket data or displays a localized request
// failure. Unknown reasons use the supplied feature message, keeping
// unrecognized server responses out of the visible conversation state.
function handleError(err: unknown, fallback: string) {
  if (extractApiErrorCode(err) === 'TICKET_DISABLED') markDisabled()
  else error.value = extractI18nErrorMessage(err, t, 'tickets.errors', t(fallback))
}

// load reads current ticket configuration and the requested visible list page.
// Each invocation captures a request generation, and only the latest generation
// may update rows, totals, errors, or loading state.
//
// A disabled module clears data and dialogs before returning. An enabled result
// synchronizes public navigation, then fetches tickets for the selected entry
// scope and filters. Earlier responses are ignored after filter changes or
// terminal actions, keeping the displayed rows consistent with the current list
// request.
async function load() {
  const current = ++requestID
  loading.value = true
  error.value = ''
  try {
    const api = ticketAPI(props.admin)
    const config = await api.config()
    if (current !== requestID) return
    settings.value = config
    if (!config.enabled) {
      markDisabled()
      return
    }
    void appStore.syncTicketModuleEnabled(true)
    const result = await api.list({
      ...filters,
      q: filters.q.trim(),
      page: page.value,
      page_size: pageSize.value,
    })
    // Apply the latest request generation after filters change, preventing an older
    // list response from replacing the currently selected search results.
    if (current !== requestID) return
    disabled.value = false
    items.value = result.items
    total.value = result.total
  } catch (err) {
    if (current === requestID) {
      items.value = []
      total.value = 0
      handleError(err, 'tickets.loadFailed')
    }
  } finally {
    if (current === requestID) loading.value = false
  }
}
// search resets pagination and reloads tickets using the current filter values.
function search() {
  page.value = 1
  void load()
}
// refresh reloads the current page using the selected ticket filters.
function refresh() {
  void load()
}
// changePage selects a page and requests its visible ticket records.
function changePage(value: number) {
  page.value = value
  void load()
}
// changePageSize updates row capacity and restarts filtering from the first
// page.
function changePageSize(value: number) {
  pageSize.value = value
  search()
}
// created closes the creation dialog and opens the committed ticket
// conversation.
function created(ticket: Ticket) {
  showCreate.value = false
  void router.push(`${base.value}/${ticket.id}`)
}
const isOpen = (ticket: Ticket) => ['pending', 'waiting_user'].includes(ticket.status)
// requestClose opens confirmation for an open ticket when support is available.
// Busy closure requests and terminal tickets leave the dialog unchanged,
// matching the server lifecycle restrictions.
function requestClose(ticket: Ticket, action: 'cancel' | 'complete') {
  if (!closing.value && !disabled.value && isOpen(ticket)) closeTarget.value = { ticket, action }
}
// closeTicket submits the confirmed terminal action and reloads the support
// list. It invalidates pending list reads before mutation so a late response
// cannot overwrite the committed ticket state.
//
// The entry scope is captured for the request. Switching between administrator
// and personal lists prevents that response from updating the new view. Failed
// closure retains the confirmation target for retry, and disabled-module errors
// clear the target with the rows. The busy flag covers both mutation and the
// resulting refresh.
async function closeTicket() {
  if (closing.value || disabled.value || !closeTarget.value) return
  const { ticket, action } = closeTarget.value
  const admin = props.admin
  closing.value = true
  error.value = ''
  // Invalidate active list reads before closing a ticket so an earlier response
  // cannot overwrite the refreshed terminal state after mutation.
  ++requestID
  loading.value = false
  try {
    await ticketAPI(admin).close(ticket.id, action)
    if (props.admin !== admin) return
    closeTarget.value = null
    await load()
  } catch (err) {
    if (props.admin === admin) handleError(err, 'tickets.actionFailed')
  } finally {
    closing.value = false
  }
}
watch(
  () => props.admin,
  () => {
    page.value = 1
    settings.value = undefined
    disabled.value = false
    closeTarget.value = null
    void load()
  },
)
onMounted(load)
</script>
