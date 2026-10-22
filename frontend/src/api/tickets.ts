import { apiClient } from './client'
import type { BasePaginationResponse } from '@/api/common.types'

// tickets.ts defines the ticket protocol and authenticated user and support API
// requests. Multipart submissions send structured payload fields beside private
// file objects, and retry identities travel in the Idempotency-Key header. The
// selected access scope determines the endpoint family for reads and support
// actions.
//
// Attachment previews use authenticated Blob requests and bounded JSON error
// parsing. Unauthorized responses continue through the shared client token-
// refresh handling, while other preview failures become stable reason values for
// localized messages. File contents and credentials stay out of preview URLs.
// Original downloads and validated inline previews remain separate operations so
// the browser opens files only after an explicit action.

// Ticket categories use the same canonical values as server validation;

// historical categories are normalized by the persistence migration and service.
export const ticketTypes = ['consultation', 'financial', 'technical'] as const
export const ticketPriorities = ['low', 'normal', 'high'] as const
// The subject counter uses Unicode code points so supplementary characters
// receive the same allowance as server rune-based validation.
export const ticketTitleMaxLength = 50
export const ticketStatuses = [
  'pending',
  'waiting_user',
  'completed',
  'cancelled',
  'expired',
] as const
// TicketType represents canonical support category shared with server input validation.
export type TicketType = (typeof ticketTypes)[number]
// TicketPriority represents canonical priority used in administrator queue ordering and filters.
export type TicketPriority = (typeof ticketPriorities)[number]
// TicketStatus represents persisted conversation state used to enable supported ticket actions.
export type TicketStatus = (typeof ticketStatuses)[number]
// TicketClosedByRole represents authenticated closing role snapshot with an empty historical unknown value.
export type TicketClosedByRole = 'user' | 'admin' | 'system' | ''

// TicketSettings represents current module switch and submission limits used by support forms.
export interface TicketSettings {
  enabled: boolean // support availability switch
  max_open_tickets: number // per-owner open ticket limit
  max_attachments: number // per-submission file count limit
  max_attachment_size_mb: number // per-file limit in MiB
  notify_on_staff_reply: boolean // support event email switch
  auto_expire_hours: number // waiting-user expiration age in hours
}

// TicketAttachment represents private attachment metadata returned with an authorized conversation message.
export interface TicketAttachment {
  id: number // stored record identifier
  message_id: number // owning message identifier
  filename: string // normalized display filename
  content_type: string // validated MIME type
  size: number // stored byte count
  created_at: string // record creation timestamp
}

// TicketMessage represents one conversation entry with sender information and private file metadata.
export interface TicketMessage {
  id: number // stored record identifier
  ticket_id: number // owning ticket identifier
  user_id: number // ticket owner identifier
  sender_name: string // sender display name
  is_staff: boolean // support entry identity
  content: string // conversation message text
  created_at: string // record creation timestamp
  attachments: TicketAttachment[] // private file metadata
}

// Ticket represents an owned support conversation with lifecycle and payment summary metadata.
export interface Ticket {
  id: number // stored record identifier
  user_id: number // ticket owner identifier
  user_email: string // ticket owner email
  user_name: string // ticket owner name
  title: string // trimmed ticket subject
  content: string // conversation message text
  type: TicketType // canonical ticket category
  priority: TicketPriority // canonical handling priority
  status: TicketStatus // ticket lifecycle state
  order_id?: number | null // optional owned payment reference
  created_at: string // record creation timestamp
  updated_at: string // latest activity timestamp
  last_staff_reply_at?: string | null // latest support reply timestamp
  closed_at?: string | null // first terminal action timestamp
  closed_by?: number | null // recorded closing account identifier
  closed_by_role?: TicketClosedByRole | null // recorded closing role snapshot
  messages?: TicketMessage[] // ordered conversation message records
  order?: {
    id: number
    out_trade_no: string
    amount: number
    pay_amount: number
    currency?: string
    status: string
  } | null
}

// CreateTicketRequest represents personal ticket fields submitted beside the ordered private file list.
export interface CreateTicketRequest {
  type: TicketType // canonical ticket category
  title: string // trimmed ticket subject
  content: string // conversation message text
  priority: TicketPriority // canonical handling priority
  order_id?: number // optional owned payment reference
}

// TicketFilters represents selected list conditions and pagination sent to the ticket API.
export interface TicketFilters {
  type?: string // canonical ticket category
  status?: string // ticket lifecycle state
  priority?: string // canonical handling priority
  q?: string // literal subject search text
  page?: number // requested page number
  page_size?: number // requested row count
}

// multipart packages a ticket payload and ordered file objects for authenticated
// submission. The JSON field excludes binary data, and repeated file fields
// retain the same order used by server request hashing.
function multipart(payload: CreateTicketRequest | { content: string }, files: File[]): FormData {
  const data = new FormData()
  data.append('payload', JSON.stringify(payload))
  files.forEach((file) => data.append('files', file))
  return data
}

// ticketAPI selects personal or administrator endpoints for one caller scope.
// Creation always uses the personal entry, while administrator mutations and the
// server independently enforce support-role and ticket-state restrictions.
export function ticketAPI(admin = false) {
  const base = admin ? '/admin/tickets' : '/tickets'
  return {
    // list returns a visible ticket page for the selected access scope. Callers
    // pass current filters and pagination; the server validates their bounds and
    // computes totals using the same owner restriction.
    async list(params: TicketFilters) {
      return (await apiClient.get<BasePaginationResponse<Ticket>>(base, { params })).data
    },
    // get retrieves one authorized conversation and its private attachment
    // metadata. The optional abort signal belongs to the caller, allowing
    // preview navigation to cancel a conversation lookup before fetching file
    // bytes.
    async get(id: number, signal?: AbortSignal) {
      return (await apiClient.get<Ticket>(`${base}/${id}`, { signal })).data
    },
    // create submits a personal ticket and ordered files with the supplied retry
    // key. Callers retain the same key for an unchanged draft so uncertain
    // network responses can reuse the committed result.
    async create(payload: CreateTicketRequest, files: File[], idempotencyKey: string) {
      return (
        await apiClient.post<Ticket>('/tickets', multipart(payload, files), {
          headers: {
            'Content-Type': 'multipart/form-data',
            'Idempotency-Key': idempotencyKey,
          },
        })
      ).data
    },
    // reply appends draft text and ordered files with a stable retry identity.
    // The selected access scope determines staff handling, while the server
    // independently checks ownership, role, and open conversation state.
    async reply(id: number, content: string, files: File[], idempotencyKey: string) {
      return (
        await apiClient.post<Ticket>(`${base}/${id}/replies`, multipart({ content }, files), {
          headers: {
            'Content-Type': 'multipart/form-data',
            'Idempotency-Key': idempotencyKey,
          },
        })
      ).data
    },
    // close submits one supported terminal action for the selected ticket scope.
    // Repeating the same action returns its saved result; the server rejects
    // changes between different closed states.
    async close(id: number, action: 'cancel' | 'complete') {
      return (await apiClient.post<Ticket>(`${base}/${id}/${action}`)).data
    },
    // update changes ticket priority through the administrator endpoint after
    // server permission checks.
    async update(id: number, payload: { priority: TicketPriority }) {
      return (await apiClient.patch<Ticket>(`/admin/tickets/${id}`, payload)).data
    },
    // download retrieves original private attachment bytes through the
    // authenticated API client.
    async download(id: number, attachmentId: number) {
      return (
        await apiClient.get<Blob>(`${base}/${id}/attachments/${attachmentId}`, {
          responseType: 'blob',
        })
      ).data
    },
    // Preview requests retain shared unauthorized-response refresh handling, while
    // other Blob errors are parsed within a fixed response-size budget.
    async preview(id: number, attachmentId: number, signal?: AbortSignal) {
      const response = await apiClient.get<Blob>(
        `${base}/${id}/attachments/${attachmentId}/preview`,
        {
          responseType: 'blob',
          signal,
          validateStatus: (status) => status !== 401,
        },
      )
      if (response.status >= 200 && response.status < 300) return response.data
      let details: Record<string, unknown> = {}
      if (response.data.size <= 64 * 1024 && response.data.type.includes('json')) {
        try {
          const parsed: unknown = JSON.parse(await response.data.text())
          if (parsed && typeof parsed === 'object' && !Array.isArray(parsed))
            details = parsed as Record<string, unknown>
        } catch {
          /* Malformed error data uses the generic failure message; HTML remains unrendered so upstream error pages cannot become preview content. */
        }
      }
      throw {
        status: response.status,
        reason: typeof details.reason === 'string' ? details.reason : undefined,
        code: details.code,
      }
    },
    // config reads support availability and limits even while the module is
    // disabled. The endpoint follows the selected access scope, allowing
    // administrators to restore availability through its separate settings
    // writer.
    async config() {
      return (await apiClient.get<TicketSettings>(admin ? `${base}/settings` : `${base}/config`))
        .data
    },
  }
}

// saveTicketSettings persists the administrator support configuration and
// returns its confirmed values.
export async function saveTicketSettings(settings: TicketSettings): Promise<TicketSettings> {
  return (await apiClient.put<TicketSettings>('/admin/tickets/settings', settings)).data
}
