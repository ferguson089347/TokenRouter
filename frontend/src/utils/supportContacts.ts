// supportContacts.ts reads support methods from localized site contact text and
// serializes edited entries for the existing settings field. Structured values
// keep uploaded image data separate from labels and addresses, while legacy
// single-line and multiline contact strings remain available to existing sites.
// Both profile and header consumers use these rules to recognize the same contact
// identities, so a label and URL cannot separate differently across page locations.
//
// The versioned JSON string carries full image data URLs, including their commas,
// before legacy delimiter parsing is considered. HTTP URLs are recognized before
// label separators because ports and path colons belong to the destination.
// Browser consumers validate links and images separately from editable text, keeping
// existing administrators able to correct malformed addresses in the same field.

/** SupportContact describes a labeled address and an optional support image. */
export interface SupportContact {
  label: string // administrator supplied contact label
  value: string // contact number or destination
  image: string // uploaded or linked image
}

/** safeContactUrl returns a permitted destination for support links or images.
 * Links accept HTTP(S) URLs. Image consumers additionally accept same-site paths
 * and image data URLs emitted by the image uploader; invalid values return empty.
 */
export function safeContactUrl(value: string, image = false): string {
  const text = value.trim()
  if (image && /^data:image\/(?:png|jpe?g|gif|webp|avif|bmp|svg\+xml|x-icon|vnd\.microsoft\.icon);base64,[a-z\d+/=\s]+$/i.test(text))
    return text
  if (image && text.startsWith('/') && !text.startsWith('//') && !text.includes('\\')) return text
  try {
    const url = new URL(text)
    return ['https:', 'http:'].includes(url.protocol) && !url.username && !url.password
      ? url.toString()
      : ''
  } catch {
    // Phone numbers and chat handles are valid contact text. Leave them available for copying rather than treating them as browser navigation targets.
    return ''
  }
}

/** parseSupportContacts reads structured entries or splits legacy contact text.
 * Old labels use the first colon outside an HTTP(S) scheme. Actual newlines,
 * escaped newlines, and standalone /n markers separate legacy entries alongside
 * the commas and semicolons accepted by the original support menu.
 *
 * Structured payloads retain empty rows for the administrator's editing session.
 * Structured members are narrowed to string fields, and malformed JSON-looking
 * text remains ordinary legacy content. Image URLs are checked when rendering so saved text
 * remains editable even when its image address needs correction.
 */
export function parseSupportContacts(raw: string): SupportContact[] {
  const text = raw.trim()
  if (!text) return []
  if (text.startsWith('{')) {
    try {
      const value: unknown = JSON.parse(text)
      if (
        value &&
        typeof value === 'object' &&
        'format' in value &&
        value.format === 'support-contacts-v1' &&
        'entries' in value &&
        Array.isArray(value.entries)
      ) {
        return value.entries.flatMap((entry: unknown) => {
          if (!entry || typeof entry !== 'object') return []
          return [
            {
              label: 'label' in entry && typeof entry.label === 'string' ? entry.label : '',
              value: 'value' in entry && typeof entry.value === 'string' ? entry.value : '',
              image: 'image' in entry && typeof entry.image === 'string' ? entry.image : ''
            }
          ]
        })
      }
    } catch {
      // A preexisting contact may contain braces as ordinary text. Keep parsing that text so editing cannot discard an older site's saved details.
    }
  }
  return text
    .replace(/\\n/g, '\n')
    .replace(/(?:^|\s)\/n(?=\s|$)/g, '\n')
    .split(/[，,;；\r\n]+/)
    .map((part) => part.trim())
    .filter(Boolean)
    .map((part) => {
      const separator = part.search(/：|:(?!\/\/)/)
      // A bare URL can contain a port or colon in its path. Recognize its scheme
      // first so an address such as https://host:8443/help retains its complete identity.
      if (/^https?:\/\//i.test(part) || separator <= 0) return { label: '', value: part, image: '' }
      return {
        label: part.slice(0, separator).trim(),
        value: part.slice(separator + 1).trim(),
        image: ''
      }
    })
    .filter((entry) => entry.value)
}

/** serializeSupportContacts encodes editable rows for the localized contact field. */
export function serializeSupportContacts(entries: SupportContact[]): string {
  return entries.length ? JSON.stringify({ format: 'support-contacts-v1', entries }) : ''
}
