import type { ProtocolID } from '@/api/protocols.types'

// magpieImport.ts builds Magpie's web import links from gateway addresses and
// the text protocols available to an API key. Credentials stay in the fragment,
// and Magpie discovers the key's models through the gateway after confirmation.

/** MagpieImportInput supplies the provider identity, credentials, and permitted gateway protocols. */
export interface MagpieImportInput {
  baseUrl: string // configured gateway base URL
  providerName: string // displayed site provider name
  apiKey: string // selected API key value
  protocols: readonly ProtocolID[] // permitted client request protocols
}

// MAGPIE_ENDPOINTS maps each supported gateway protocol to Magpie's parameter and
// required URL suffix, keeping version normalization consistent across imports.
const MAGPIE_ENDPOINTS: ReadonlyArray<readonly [ProtocolID, string, string]> = [
  ['openai_chat_completions', 'chat', '/v1'],
  ['openai_responses', 'responses', '/v1'],
  ['anthropic_messages', 'anthropic', ''],
  ['gemini_generate_content', 'gemini', '/v1beta'],
]

/** buildMagpieImportUrl encodes a key and its supported endpoints for Magpie.
 * Empty keys, invalid base URLs, or protocols without a supported text endpoint
 * return an empty string. The base URL may include a deployment path and one
 * API version suffix. Magpie obtains the model list using the imported key.
 */
// @project-doc docs/interfaces/magpie_import.md#web_import
export function buildMagpieImportUrl(input: MagpieImportInput): string {
  let base: URL
  try {
    base = new URL(input.baseUrl)
  } catch {
    // An invalid saved gateway address cannot describe an import destination;
    // return the unavailable result for the Keys page to explain to the user.
    return ''
  }
  if (
    !['http:', 'https:'].includes(base.protocol) ||
    base.username ||
    base.password ||
    base.search ||
    base.hash ||
    !input.apiKey
  )
    return ''

  const root = base.href.replace(/\/+$/, '').replace(/\/v1(?:beta)?$/, '')
  const params = new URLSearchParams()
  // Magpie limits provider names to eighty characters in its import protocol.
  params.set(
    'name',
    Array.from(input.providerName.trim() || 'TokenRouter')
      .slice(0, 80)
      .join(''),
  )
  for (const [protocol, parameter, suffix] of MAGPIE_ENDPOINTS) {
    if (input.protocols.includes(protocol)) params.set(parameter, root + suffix)
  }
  // A name alone cannot identify a supported provider endpoint.
  if (params.size === 1) return ''
  params.set('key', input.apiKey)
  return `https://usemagpie.ai/import#${params.toString()}`
}
