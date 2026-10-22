import { describe, expect, it } from 'vitest'
import { parseSupportContacts, safeContactUrl, serializeSupportContacts } from '../supportContacts'

// supportContacts.spec.ts checks saved support content across legacy text and
// structured image entries. Cases protect URL identity, newline compatibility,
// and image data round trips so contact editing retains numbers and QR codes
// while browser navigation accepts the intended protocols.

describe('support contact content', () => {
  // Existing sites can store multiple methods in free text. Both escaped newline
  // forms split entries while URL ports and /news paths retain their full address.
  it('reads multiple legacy contacts and preserves URL identity', () => {
    const entries = parseSupportContacts(
      '客服QQ：123456\\nQQ群: 987654 /n TG群：https://t.me/news;https://support.example:8443/help',
    )
    expect(entries).toEqual([
      { label: '客服QQ', value: '123456', image: '' },
      { label: 'QQ群', value: '987654', image: '' },
      { label: 'TG群', value: 'https://t.me/news', image: '' },
      { label: '', value: 'https://support.example:8443/help', image: '' },
    ])
  })

  // An uploaded QR code contains a comma inside its data URL. Structured values
  // retain it with the contact name and address across serialization and parsing.
  it('round trips uploaded images and empty editable rows', () => {
    const entries = [
      { label: 'QQ group', value: '123456', image: 'data:image/png;base64,aGVsbG8=' },
      { label: '', value: '', image: '' },
    ]
    expect(parseSupportContacts(serializeSupportContacts(entries))).toEqual(entries)
    expect(serializeSupportContacts([])).toBe('')
  })

  // Contact values can be plain numbers and uploaded images. The browser receives
  // navigation targets exclusively for permitted links and image representations.
  it('validates contact navigation and image addresses', () => {
    expect(safeContactUrl('123456')).toBe('')
    expect(safeContactUrl('javascript:alert(1)')).toBe('')
    expect(safeContactUrl('https://user:pass@example.test')).toBe('')
    expect(safeContactUrl('//outside.test/image.png', true)).toBe('')
    expect(safeContactUrl('data:image/png;base64,aGVsbG8=', true)).toBe(
      'data:image/png;base64,aGVsbG8=',
    )
    expect(safeContactUrl('data:image/png;base64,aGVsbG8=')).toBe('')
    expect(safeContactUrl('/images/support.png', true)).toBe('/images/support.png')
    expect(safeContactUrl('https://t.me/example')).toBe('https://t.me/example')
  })

  // Unexpected structured entries may be stored by an older administrator client.
  // Invalid fields are narrowed to editable strings and ordinary braces retain text.
  it('tolerates malformed values without discarding ordinary contact text', () => {
    expect(parseSupportContacts('{Support QQ} 123456')[0].value).toBe('{Support QQ} 123456')
    expect(
      parseSupportContacts(
        '{"format":"support-contacts-v1","entries":[null,{"label":3,"value":"123","image":false}]}',
      ),
    ).toEqual([{ label: '', value: '123', image: '' }])
  })
})
