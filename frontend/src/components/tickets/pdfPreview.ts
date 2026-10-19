import { getDocument, GlobalWorkerOptions, AnnotationMode } from 'pdfjs-dist'
import workerUrl from 'pdfjs-dist/build/pdf.worker.min.mjs?url'
import jpegFallbackUrl from 'pdfjs-dist/wasm/openjpeg_nowasm_fallback.js?url'
import jbigFallbackUrl from 'pdfjs-dist/wasm/jbig2_nowasm_fallback.js?url'

// pdfPreview.ts configures the bundled PDF renderer for private support
// attachments. Workers, character maps, fonts, and image decoders resolve to
// application assets. Document-provided resource locations are excluded from the
// resource factory.
//
// The factory maps requested filenames to build-time assets, decoding inline
// data URLs locally or fetching same-origin resources with the preview abort
// signal. PDF loading disables XFA, WASM, font injection, and annotations in the
// caller. The caller owns the returned task and destroys it when a preview
// closes or changes; page rendering separately applies side-length and pixel
// limits before allocating a canvas.

// The resource map is created from bundled assets, so a document cannot redirect

// character-map or font requests to an external destination.
const resources = import.meta.glob<string>(
  [
    '/node_modules/pdfjs-dist/cmaps/*.bcmap',
    '/node_modules/pdfjs-dist/standard_fonts/*.{pfb,ttf}',
  ],
  { eager: true, query: '?url', import: 'default' },
)

GlobalWorkerOptions.workerSrc = workerUrl
export const pdfAnnotationMode = AnnotationMode.DISABLE
export const pdfMaxCanvasPixels = 8_000_000

// loadTicketPDF opens validated PDF bytes with local resource resolution and a
// cancellable fetch signal. The caller owns the returned loading task and
// destroys it when the preview closes or changes.
export function loadTicketPDF(data: Uint8Array, signal: AbortSignal) {
  // Bundled JavaScript decoders share one application directory; the worker loads
  // their fixed filenames when WASM decoding is disabled for previews.
  const jpegDirectory = new URL(
    '.',
    new URL(jpegFallbackUrl, window.location.href),
  ).href
  const jbigDirectory = new URL(
    '.',
    new URL(jbigFallbackUrl, window.location.href),
  ).href
  if (jpegDirectory !== jbigDirectory)
    throw new Error('PDF decoder resources unavailable')
  // LocalBinaryDataFactory resolves document resource requests through the bundled asset map.
  class LocalBinaryDataFactory {
    // fetch returns bundled character-map or font bytes for a recognized resource
    // request. Unknown resource kinds and filenames fail before network access.
    // The preview abort signal cancels same-origin fetches when the caller leaves.
    //
    // Small build assets may be encoded directly in data URLs, while larger
    // resources have content-hashed URLs. Both representations come from the same
    // build-time map, preserving resource identity through bundler output choices.
    // Data URLs are decoded locally and successful network responses return their
    // bytes to the PDF parser without adopting document-provided addresses.
    async fetch({
      kind,
      filename,
    }: {
      kind: string
      filename: string
    }): Promise<Uint8Array> {
      const folder =
        kind === 'cMapUrl'
          ? 'cmaps'
          : kind === 'standardFontDataUrl'
            ? 'standard_fonts'
            : ''
      const url =
        folder && resources[`/node_modules/pdfjs-dist/${folder}/${filename}`]
      if (!url) throw new Error('Unsupported PDF resource')
      // Small bundled resources may be data URLs, so local decoding avoids a network
      // request and retains the existing connection policy.
      if (url.startsWith('data:')) {
        return Uint8Array.from(atob(url.slice(url.indexOf(',') + 1)), (char) =>
          char.charCodeAt(0),
        )
      }
      const response = await fetch(url, { signal, credentials: 'same-origin' })
      if (!response.ok) throw new Error('PDF resource unavailable')
      return new Uint8Array(await response.arrayBuffer())
    }
  }
  // The renderer produces page pixels with XFA and font injection disabled; the
  // caller also disables annotations before drawing the canvas.
  return getDocument({
    data,
    useWorkerFetch: false,
    BinaryDataFactory: LocalBinaryDataFactory,
    wasmUrl: jpegDirectory,
    useWasm: false,
    enableXfa: false,
    disableFontFace: true,
    useSystemFonts: true,
    maxImageSize: 40_000_000,
    canvasMaxAreaInBytes: pdfMaxCanvasPixels * 4,
    stopAtErrors: true,
  })
}
