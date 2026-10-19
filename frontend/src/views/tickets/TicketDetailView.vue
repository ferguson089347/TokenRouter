<template>
  <AppLayout fit-viewport="all">
    <!-- The layout bounds page height while conversation and reply regions own their scrolling. -->
    <div
      class="ticket-workspace mx-auto flex min-h-0 min-w-0 w-full max-w-5xl flex-1 flex-col gap-2 overflow-hidden"
    >
      <div
        class="ticket-toolbar flex min-w-0 shrink-0 items-center justify-between gap-2"
      >
        <RouterLink
          :to="base"
          class="min-w-0 truncate py-2 text-sm text-primary-600 hover:underline dark:text-primary-400"
          >← {{ t('tickets.back') }}</RouterLink
        ><button
          type="button"
          class="btn btn-secondary btn-sm shrink-0"
          :disabled="loading || busy"
          @click="load"
        >
          {{ t('common.refresh') }}
        </button>
      </div>
      <p
        v-if="disabled"
        role="status"
        class="shrink-0 rounded-surface bg-amber-50 p-3 text-sm text-amber-800 dark:bg-amber-900/20 dark:text-amber-300"
      >
        {{ t('tickets.disabled') }}
      </p>
      <p
        v-if="error"
        role="alert"
        class="max-h-24 shrink-0 overflow-y-auto break-words rounded-surface bg-red-50 p-3 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300"
      >
        {{ error }}
      </p>
      <div
        v-if="loading && !ticket"
        class="p-12 text-center text-gray-500"
        role="status"
      >
        {{ t('common.loading') }}
      </div>
      <template v-if="ticket">
        <section class="ticket-info card min-w-0 shrink-0 p-3 sm:p-4">
          <!-- Narrow screens give the subject and badges separate rows before switching to the desktop arrangement. -->
          <div
            class="ticket-title-row flex min-w-0 flex-col gap-2 sm:flex-row sm:items-start sm:justify-between"
          >
            <div class="ticket-title-block w-full min-w-0 flex-1 sm:w-auto">
              <p class="text-xs text-gray-500">
                #{{ ticket.id }} · {{ t(`tickets.types.${ticket.type}`) }}
              </p>
              <h1
                class="mt-1 truncate text-base font-semibold text-gray-900 dark:text-white sm:text-lg"
                :title="ticket.title"
              >
                {{ ticket.title }}
              </h1>
            </div>
            <div class="ticket-badges flex min-w-0 flex-wrap gap-2 sm:shrink-0">
              <TicketBadge
                kind="priority"
                :value="ticket.priority"
              /><TicketBadge
                kind="status"
                :value="ticket.status"
                :closed-by-role="ticket.closed_by_role"
              />
            </div>
          </div>
          <dl
            class="ticket-metadata mt-3 grid min-w-0 gap-2 text-xs sm:grid-cols-3 sm:gap-2"
          >
            <div v-if="admin" class="min-w-0">
              <dt class="text-gray-500">{{ t('tickets.user') }}</dt>
              <dd
                class="mt-1 min-w-0 truncate"
                :title="`${ticket.user_name} · ${ticket.user_email}`"
              >
                {{ ticket.user_name }} · {{ ticket.user_email }}
              </dd>
            </div>
            <div class="min-w-0">
              <dt class="text-gray-500">{{ t('tickets.createdAt') }}</dt>
              <dd
                class="mt-1 min-w-0 truncate"
                :title="formatDate(ticket.created_at)"
              >
                {{ formatDate(ticket.created_at) }}
              </dd>
            </div>
            <div v-if="ticket.order_id" class="min-w-0">
              <dt class="text-gray-500">{{ t('tickets.order') }}</dt>
              <dd
                class="ticket-order-summary mt-1 flex min-w-0 gap-x-2"
                :title="`${ticket.order?.out_trade_no || `#${ticket.order_id}`}${ticket.order ? ` · ${ticket.order.currency || 'CNY'} ${ticket.order.pay_amount.toFixed(2)}` : ''}`"
              >
                <span
                  class="min-w-0 truncate"
                  :title="ticket.order?.out_trade_no || `#${ticket.order_id}`"
                  >{{
                    ticket.order?.out_trade_no || `#${ticket.order_id}`
                  }}</span
                ><span v-if="ticket.order" class="shrink-0"
                  >{{ ticket.order.currency || 'CNY' }}
                  {{ ticket.order.pay_amount.toFixed(2) }}</span
                >
              </dd>
            </div>
          </dl>
          <div
            v-if="isOpen"
            class="ticket-actions mt-3 flex min-w-0 flex-wrap items-center gap-2 border-t border-gray-100 pt-3 dark:border-dark-700 sm:gap-2"
          >
            <div
              v-if="admin"
              class="ticket-priority-control flex min-w-0 items-center gap-2"
            >
              <label
                for="detail-priority"
                class="sr-only shrink-0 text-sm text-gray-500 sm:not-sr-only"
                >{{ t('tickets.priority') }}</label
              ><Select
                id="detail-priority"
                class="w-24 sm:w-28"
                :aria-label="t('tickets.priority')"
                :model-value="ticket.priority"
                :options="priorityOptions"
                :disabled="busy"
                @update:model-value="changePriority"
              />
            </div>
            <div
              class="ticket-close-actions flex min-w-0 flex-1 justify-end gap-2"
            >
              <button
                type="button"
                class="btn btn-secondary btn-sm"
                :disabled="busy || loading"
                @click="closeAction = 'cancel'"
              >
                {{ t('tickets.cancel') }}</button
              ><button
                type="button"
                class="btn btn-primary btn-sm"
                :disabled="busy || loading"
                @click="closeAction = 'complete'"
              >
                {{ t('tickets.complete') }}
              </button>
            </div>
          </div>
        </section>
        <section
          class="ticket-thread flex min-h-0 min-w-0 flex-1 flex-col gap-2"
          :aria-label="t('tickets.conversation')"
        >
          <div
            class="ticket-conversation-heading flex shrink-0 items-center justify-between gap-2"
          >
            <h2
              id="ticket-conversation-heading"
              class="text-sm font-semibold text-gray-900 dark:text-white"
            >
              {{ t('tickets.conversation') }}
            </h2>
            <button
              v-if="!followLatest"
              type="button"
              class="text-xs text-primary-600 hover:underline dark:text-primary-400"
              @click="scrollToLatest"
            >
              {{ t('tickets.latest') }}
            </button>
          </div>
          <div
            ref="conversationElement"
            data-testid="ticket-messages"
            role="region"
            tabindex="0"
            aria-labelledby="ticket-conversation-heading"
            class="min-h-0 min-w-0 flex-1 space-y-4 overflow-x-hidden overflow-y-auto overscroll-contain pr-1"
            @scroll="trackConversationScroll"
          >
            <article
              v-for="message in ticket.messages || []"
              :key="message.id"
              class="min-w-0 rounded-surface border p-3 sm:rounded-dialog sm:p-5"
              :class="
                message.is_staff
                  ? 'border-primary-100 bg-primary-50/50 dark:border-primary-900/50 dark:bg-primary-900/10'
                  : 'border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-900'
              "
            >
              <div
                class="flex min-w-0 flex-wrap items-center justify-between gap-2 text-xs"
              >
                <div class="flex min-w-0 max-w-full items-center gap-2">
                  <span
                    class="min-w-0 truncate font-semibold text-gray-900 dark:text-white"
                    :title="
                      message.sender_name ||
                      (message.is_staff
                        ? t('tickets.staff')
                        : ticket.user_name || t('tickets.user'))
                    "
                    >{{
                      message.sender_name ||
                      (message.is_staff
                        ? t('tickets.staff')
                        : ticket.user_name || t('tickets.user'))
                    }}</span
                  ><span
                    v-if="message.is_staff"
                    class="shrink-0 rounded-compact bg-primary-100 px-2 py-0.5 text-primary-700 dark:bg-primary-900/30 dark:text-primary-300"
                    >{{ t('tickets.staff') }}</span
                  >
                </div>
                <time :datetime="message.created_at" class="text-gray-500">{{
                  formatDate(message.created_at)
                }}</time>
              </div>
              <!-- Conversation text uses escaped interpolation so uploaded message content stays text within the application. -->
              <p
                class="mt-3 whitespace-pre-wrap break-words text-sm leading-6 text-gray-800 dark:text-dark-200 sm:mt-4 sm:leading-7"
              >
                {{ message.content }}
              </p>
              <ul
                v-if="message.attachments?.length"
                class="mt-3 flex min-w-0 flex-wrap gap-2 sm:mt-4"
              >
                <li
                  v-for="attachment in message.attachments"
                  :key="attachment.id"
                  class="min-w-0 max-w-full"
                >
                  <button
                    type="button"
                    class="flex min-w-0 max-w-full items-center gap-2 rounded-control border border-gray-200 bg-white px-3 py-2 text-xs text-primary-600 hover:bg-gray-50 dark:border-dark-600 dark:bg-dark-800 dark:text-primary-400"
                    :aria-label="
                      t('tickets.preview.openNamed', {
                        name: attachment.filename,
                      })
                    "
                    @click="previewAttachment = attachment"
                  >
                    <Icon name="document" size="sm" class="shrink-0" /><span
                      class="min-w-0 max-w-64 truncate"
                      :title="attachment.filename"
                      >{{ attachment.filename }}</span
                    ><span class="shrink-0 text-gray-500">{{
                      formatAttachmentSize(attachment.size)
                    }}</span>
                  </button>
                </li>
              </ul>
            </article>
          </div>
        </section>
        <form
          v-if="isOpen && settings"
          data-testid="ticket-composer"
          class="ticket-composer card min-h-0 min-w-0 max-h-[40dvh] shrink-0 space-y-2 overflow-x-hidden overflow-y-auto p-3 sm:p-4"
          @submit.prevent="reply"
        >
          <label class="ticket-reply-label input-label" for="ticket-reply">{{
            t('tickets.reply')
          }}</label
          ><textarea
            id="ticket-reply"
            v-model="content"
            rows="3"
            maxlength="20000"
            class="input block w-full resize-none"
            :disabled="busy"
            :placeholder="t('tickets.replyPlaceholder')"
          />
          <Disclosure
            class="ticket-reply-attachments"
            summary-class="cursor-pointer text-xs text-primary-600 dark:text-primary-400"
            ><template #summary
              >{{ t('tickets.attachments')
              }}{{ files.length ? ` (${files.length})` : '' }}</template
            ><TicketAttachments
              id="reply-attachments"
              v-model="files"
              class="mt-2"
              :settings="settings"
              :disabled="busy"
          /></Disclosure>
          <div
            class="ticket-reply-actions flex min-w-0 items-center justify-between gap-2"
          >
            <p
              v-if="!admin && settings.auto_expire_hours > 0"
              class="ticket-expiry-hint min-w-0 text-xs text-gray-500"
            >
              {{
                t('tickets.expiryHint', { hours: settings.auto_expire_hours })
              }}
            </p>
            <button
              type="submit"
              class="btn btn-primary ml-auto shrink-0"
              :disabled="busy || loading || (!content.trim() && !files.length)"
            >
              {{ busy ? t('common.processing') : t('tickets.sendReply') }}
            </button>
          </div>
        </form>
        <p
          v-else-if="!isOpen"
          class="shrink-0 rounded-surface bg-gray-100 p-3 text-sm text-gray-500 dark:bg-dark-800"
        >
          {{ t('tickets.closedHint') }}
        </p>
      </template>
    </div>
    <ConfirmDialog
      :show="!!closeAction"
      :title="
        t(closeAction === 'cancel' ? 'tickets.cancel' : 'tickets.complete')
      "
      :message="
        t(
          closeAction === 'cancel'
            ? 'tickets.cancelConfirm'
            : 'tickets.completeConfirm',
        )
      "
      :danger="closeAction === 'cancel'"
      :loading="busy"
      @cancel="!busy && (closeAction = null)"
      @confirm="closeTicket"
    />
    <!-- The preview dialog releases file resources when it closes while the conversation retains its draft and reading position. -->
    <BaseDialog
      :show="!!previewAttachment"
      :title="previewAttachment?.filename || t('tickets.preview.title')"
      width="extra-wide"
      @close="previewAttachment = undefined"
    >
      <TicketAttachmentPreview
        v-if="previewAttachment && ticket"
        :ticket-id="ticket.id"
        :attachment-id="previewAttachment.id"
        :admin="admin"
        :show-title="false"
        @disabled="markDisabled"
      />
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted, nextTick } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Select from '@/components/common/Select.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import TicketAttachmentPreview from '@/components/tickets/TicketAttachmentPreview.vue'
import Icon from '@/components/icons/Icon.vue'
import Disclosure from '@/components/common/Disclosure.vue'
import TicketBadge from '@/components/tickets/TicketBadge.vue'
import TicketAttachments from '@/components/tickets/TicketAttachments.vue'
import { formatAttachmentSize } from '@/components/tickets/attachments'
import {
  ticketAPI,
  ticketPriorities,
  type Ticket,
  type TicketSettings,
  type TicketPriority,
  type TicketAttachment,
} from '@/api/tickets'
import { extractApiErrorCode, extractI18nErrorMessage } from '@/utils/apiError'
import { useAppStore } from '@/stores/app'
import { ticketSubmission } from '@/components/tickets/submission'

// TicketDetailView.vue presents an owned support conversation and the actions
// allowed by its current state. The shared viewport layout keeps the message
// region scrollable and the reply composer reachable. Administrator mode adds
// priority editing and owner information while retaining the same message and
// attachment components.
//
// Configuration reads precede conversation reads, and request generations
// prevent a result from another ticket or entry scope replacing the current
// conversation. Mutations wait for refresh completion before writing. Draft
// identity survives failed sends for idempotent retry, while route changes clear
// drafts and attachment previews. Refresh preserves historical reading position
// unless the reader is already following the latest message.

const props = withDefaults(defineProps<{ admin?: boolean }>(), { admin: false })
const { t } = useI18n()
const appStore = useAppStore()
const route = useRoute()
const base = computed(() => (props.admin ? '/admin/tickets' : '/tickets'))
const ticket = ref<Ticket>()
const settings = ref<TicketSettings>()
const loading = ref(false)
const busy = ref(false)
const error = ref('')
const disabled = ref(false)
const content = ref('')
const files = ref<File[]>([])
const closeAction = ref<'cancel' | 'complete' | null>(null)
const previewAttachment = ref<TicketAttachment>()
const conversationElement = ref<HTMLElement>()
const followLatest = ref(true)
const submission = ticketSubmission()
const isOpen = computed(
  () =>
    ticket.value && ['pending', 'waiting_user'].includes(ticket.value.status),
)
const priorityOptions = computed(() =>
  ticketPriorities.map((value) => ({
    value,
    label: t(`tickets.priorities.${value}`),
  })),
)
const formatDate = (date: string) => new Date(date).toLocaleString()
let requestID = 0

// trackConversationScroll records whether the reader remains near the newest
// message.
function trackConversationScroll() {
  const element = conversationElement.value
  if (element)
    followLatest.value =
      element.scrollHeight - element.scrollTop - element.clientHeight < 80
}
// scrollToLatest follows the conversation after Vue finishes applying its latest
// message update. It waits for the next render before measuring and setting the
// scroll position of the message region.
async function scrollToLatest() {
  followLatest.value = true
  await nextTick()
  const element = conversationElement.value
  if (element) element.scrollTop = element.scrollHeight
}

// markDisabled clears the conversation, terminal confirmation, and attachment
// preview after support closes. Pending reads are invalidated before publishing
// the switch to navigation, preventing unavailable data from reappearing.
function markDisabled() {
  ++requestID
  loading.value = false
  disabled.value = true
  void appStore.syncTicketModuleEnabled(false)
  ticket.value = undefined
  closeAction.value = null
  previewAttachment.value = undefined
  error.value = ''
}
// handleError interprets disabled support separately from ordinary localized
// action failures. A disabled response removes historical content and active
// dialogs; other failures preserve the current conversation and draft for retry.
function handleError(err: unknown, fallback: string) {
  if (extractApiErrorCode(err) === 'TICKET_DISABLED') markDisabled()
  else
    error.value = extractI18nErrorMessage(err, t, 'tickets.errors', t(fallback))
}

// load reads current settings and conversation detail for the selected route. A
// request generation selects the response allowed to update the page. Disabled
// support clears detail and synchronizes navigation before returning.
//
// Successful refreshes replace the conversation but respect a reader browsing
// historical messages. First loads and readers near the bottom follow the newest
// message after rendering. Older responses cannot replace another ticket after
// route changes, and errors update the page only while their generation remains
// current.
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
    const result = await api.get(Number(route.params.id))
    if (current === requestID) {
      disabled.value = false
      // Readers browsing historical messages keep their position on refresh; first
      // loads and readers already near the bottom follow the latest reply.
      const shouldFollow = !ticket.value || followLatest.value
      ticket.value = result
      settings.value = config
      if (shouldFollow) await scrollToLatest()
    }
  } catch (err) {
    if (current === requestID) handleError(err, 'tickets.loadFailed')
  } finally {
    if (current === requestID) loading.value = false
  }
}

// mutate serializes a conversation write against refreshes and other mutations.
// Busy, loading, or disabled pages reject the action before sending it. The
// request generation captured before the action determines whether its result
// still belongs to this page.
//
// Successful results replace detail only for the same conversation generation.
// Failures show a localized error while callers retain their drafts or
// confirmation state. The method returns whether the result was applied,
// allowing reply and closure callers to reset local input only after a committed
// response is accepted.
async function mutate(action: () => Promise<Ticket>): Promise<boolean> {
  // Wait for active reads before mutation so a refresh response cannot replace the
  // conversation state returned by a newly committed reply.
  if (busy.value || loading.value || disabled.value) return false
  busy.value = true
  error.value = ''
  const current = requestID
  try {
    const result = await action()
    if (current !== requestID) return false
    ticket.value = result
    return true
  } catch (err) {
    if (current === requestID) handleError(err, 'tickets.actionFailed')
    return false
  } finally {
    busy.value = false
  }
}
// reply sends the current draft with a stable submission key for retry. Draft
// text and files are cleared only when the committed result applies to this
// conversation, then the reader follows the newest message.
async function reply() {
  if (
    !ticket.value ||
    (!content.value.trim() && !files.value.length) ||
    !isOpen.value
  )
    return
  const id = ticket.value.id
  const body = content.value.trim()
  const key = submission.key({ id, content: body }, files.value)
  if (
    await mutate(() => ticketAPI(props.admin).reply(id, body, files.value, key))
  ) {
    content.value = ''
    files.value = []
    submission.reset()
    await scrollToLatest()
  }
}
// changePriority accepts a supported changed priority and submits it through
// serialized mutation. Unsupported or unchanged values leave the page intact,
// while the administrator API independently verifies support permission.
async function changePriority(value: string | number | boolean | null) {
  if (
    !ticket.value ||
    !ticketPriorities.includes(value as TicketPriority) ||
    value === ticket.value.priority
  )
    return
  await mutate(() =>
    ticketAPI(true).update(ticket.value!.id, {
      priority: value as TicketPriority,
    }),
  )
}
// closeTicket submits the currently confirmed terminal action through serialized
// mutation. The confirmation closes after its response applies; a failure
// retains the action so the user can review or retry it.
async function closeTicket() {
  if (!ticket.value || !closeAction.value) return
  const action = closeAction.value
  if (
    await mutate(() => ticketAPI(props.admin).close(ticket.value!.id, action))
  )
    closeAction.value = null
}
watch(
  () => [route.params.id, props.admin],
  () => {
    // Clear drafts, selections, and preview state when switching tickets so a late
    // request cannot mix another conversation into the new route.
    ticket.value = undefined
    settings.value = undefined
    content.value = ''
    files.value = []
    closeAction.value = null
    previewAttachment.value = undefined
    followLatest.value = true
    disabled.value = false
    submission.reset()
    void load()
  },
)
onMounted(load)
</script>

<style scoped>
/* The layout provides viewport height; only message and composer regions scroll. */
.ticket-thread article p {
  overflow-wrap: anywhere;
}
.ticket-reply-attachments {
  min-width: 0;
}
@media (max-height: 700px) {
  .ticket-info {
    min-height: 0;
    max-height: 38%;
    overflow: auto;
    flex-shrink: 1;
  }
  .ticket-composer {
    max-height: 38%;
    padding: 0.5rem;
  }
  #ticket-reply {
    height: 3rem;
    min-height: 3rem;
  }
  .ticket-metadata {
    margin-top: 0.5rem;
  }
  .ticket-expiry-hint {
    display: none;
  }
}
</style>
