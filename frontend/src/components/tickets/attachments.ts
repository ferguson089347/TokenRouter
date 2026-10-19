import type { TicketSettings } from '@/api/tickets'

// attachments.ts supplies immediate browser checks and display sizes for private
// ticket files. The extension allowlist and configured count and size limits
// guide file selection, while the server independently validates real file
// content and access permission.

export const attachmentAccept = '.jpg,.jpeg,.png,.gif,.webp,.pdf,.doc,.docx'

// validateTicketFiles returns the first selection problem or null for accepted
// browser metadata. Count, extension, nonempty size, and combined capacity
// checks provide feedback before the server inspects actual content.
export function validateTicketFiles(
  files: File[],
  settings: TicketSettings,
): 'count' | 'type' | 'size' | 'total' | null {
  if (files.length > settings.max_attachments) return 'count'
  if (
    files.some((file) => !/\.(jpe?g|png|gif|webp|pdf|docx?)$/i.test(file.name))
  )
    return 'type'
  if (
    files.some(
      (file) =>
        file.size === 0 ||
        file.size > settings.max_attachment_size_mb * 1024 * 1024,
    )
  )
    return 'size'
  if (files.reduce((sum, file) => sum + file.size, 0) > 50 * 1024 * 1024)
    return 'total'
  return null
}

// formatAttachmentSize displays byte counts as binary kilobytes or
// megabytes.
export function formatAttachmentSize(bytes: number): string {
  return bytes >= 1024 * 1024
    ? `${(bytes / (1024 * 1024)).toFixed(1)} MB`
    : `${Math.ceil(bytes / 1024)} KB`
}
