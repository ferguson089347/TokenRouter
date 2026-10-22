import { describe, expect, it } from 'vitest'
import { buildMagpieImportUrl, type MagpieImportInput } from '../magpieImport'

// magpieImport.spec.ts checks Magpie's documented web handoff fields and endpoint
// normalization. The cases protect credential encoding in fragments, protocol
// filtering, and deployment paths before a browser opens the generated URL.

// baseInput supplies a Responses provider whose credentials and endpoints are
// replaced by individual cases to exercise the documented import format.
const baseInput: MagpieImportInput = {
  baseUrl: 'https://api.example.test',
  providerName: 'MoonUp',
  apiKey: 'sk-test',
  protocols: ['openai_responses'],
}

describe('Magpie web import', () => {
  // Magpie accepts provider fields in the fragment of its HTTPS handoff page.
  // Special characters in names and credentials round trip through URLSearchParams.
  it('keeps credentials in the fragment and encodes them independently', () => {
    const key = 'sk-&=+# /中文'
    const url = new URL(
      buildMagpieImportUrl({ ...baseInput, apiKey: key, providerName: 'MoonUp & 中文' }),
    )
    const params = new URLSearchParams(url.hash.slice(1))

    expect(url.origin + url.pathname).toBe('https://usemagpie.ai/import')
    expect(url.search).toBe('')
    expect(params.get('key')).toBe(key)
    expect(params.get('name')).toBe('MoonUp & 中文')
    expect(params.has('models')).toBe(false)
  })

  // Gateway addresses can already include an API version or deployment prefix.
  // Each Magpie API receives its documented suffix while the deployment prefix survives.
  it.each(['', '/', '/v1', '/v1/', '/v1beta', '/v1beta/'])(
    'normalizes version suffix %s for all four APIs',
    (suffix) => {
      const url = buildMagpieImportUrl({
        ...baseInput,
        baseUrl: `https://api.example.test/gateway${suffix}`,
        protocols: [
          'openai_chat_completions',
          'openai_responses',
          'anthropic_messages',
          'gemini_generate_content',
        ],
      })
      const params = new URLSearchParams(new URL(url).hash.slice(1))

      expect(params.get('chat')).toBe('https://api.example.test/gateway/v1')
      expect(params.get('responses')).toBe('https://api.example.test/gateway/v1')
      expect(params.get('anthropic')).toBe('https://api.example.test/gateway')
      expect(params.get('gemini')).toBe('https://api.example.test/gateway/v1beta')
    },
  )

  // A provider should advertise the key's configured text endpoints. Media and
  // WebSocket permissions do not imply an additional HTTP text API.
  it('filters endpoints by their exact text protocol', () => {
    const params = new URLSearchParams(
      new URL(
        buildMagpieImportUrl({
          ...baseInput,
          protocols: ['gemini_generate_content', 'openai_responses_websocket', 'image_batches'],
        }),
      ).hash.slice(1),
    )

    expect(params.get('gemini')).toBe('https://api.example.test/v1beta')
    expect(params.has('responses')).toBe(false)
    expect(params.has('chat')).toBe(false)
    expect(params.has('anthropic')).toBe(false)
    expect(buildMagpieImportUrl({ ...baseInput, protocols: ['image_batches'] })).toBe('')
    expect(buildMagpieImportUrl({ ...baseInput, protocols: [] })).toBe('')
  })

  // Magpie's importer rejects addresses with credentials, query strings, or fragments.
  // Invalid destinations produce the unavailable result before any credential handoff.
  it.each([
    'not a URL',
    'javascript:alert(1)',
    'file:///tmp/key',
    'https://user:pass@api.example.test',
    'https://api.example.test?route=one',
    'https://api.example.test#route',
  ])('rejects invalid base address %s', (baseUrl) => {
    expect(buildMagpieImportUrl({ ...baseInput, baseUrl })).toBe('')
  })

  // Local installations can use an HTTP loopback gateway with a deployment path.
  // The imported endpoint preserves the configured port and accepts no empty credential.
  it('supports loopback gateways and rejects empty keys', () => {
    const params = new URLSearchParams(
      new URL(
        buildMagpieImportUrl({
          ...baseInput,
          baseUrl: 'http://127.0.0.1:8080/gateway/v1/',
        }),
      ).hash.slice(1),
    )

    expect(params.get('responses')).toBe('http://127.0.0.1:8080/gateway/v1')
    expect(buildMagpieImportUrl({ ...baseInput, apiKey: '' })).toBe('')
  })

  // Magpie limits imported provider names to eighty characters. Blank site names
  // receive the product name and supplementary Unicode characters count once each.
  it('bounds provider names and supplies the product default', () => {
    const long = new URLSearchParams(
      new URL(
        buildMagpieImportUrl({
          ...baseInput,
          providerName: '🐦'.repeat(81),
        }),
      ).hash.slice(1),
    )
    const blank = new URLSearchParams(
      new URL(
        buildMagpieImportUrl({
          ...baseInput,
          providerName: '  ',
        }),
      ).hash.slice(1),
    )

    expect(long.get('name')).toBe('🐦'.repeat(80))
    expect(blank.get('name')).toBe('TokenRouter')
  })
})
