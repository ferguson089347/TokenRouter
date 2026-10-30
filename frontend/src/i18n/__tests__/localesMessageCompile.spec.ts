import { describe, expect, it } from 'vitest'
import { baseCompile } from '@intlify/message-compiler'
import { createI18n } from 'vue-i18n'

import en from '../locales/en'
import zh from '../locales/zh'

// localesMessageCompile.spec.ts compiles every string in the English and Chinese
// message catalogs with the Vue I18n compiler. Recursive traversal records
// message paths so malformed placeholders fail this test before a rendered
// component encounters the invalid message.
// A runtime translation also checks that escaped parameters cannot supply an
// active iframe document when a translated message is inserted into HTML.

// collectCompileErrors walks nested catalog values and appends compiler failures
// with their message paths. Arrays retain numeric path segments, non-string
// scalar values are skipped, and the supplied output array accumulates all
// discovered errors.
function collectCompileErrors(node: unknown, path: string, out: string[]): void {
  if (typeof node === 'string') {
    // Collect invalid placeholders with their catalog paths so traversal can
    // report failures from subsequent messages during the same catalog check.
    baseCompile(node, {
      onError: (err) => {
        out.push(`${path}: ${err.message}`)
      },
    })
    return
  }
  if (Array.isArray(node)) {
    node.forEach((item, index) => collectCompileErrors(item, `${path}[${index}]`, out))
    return
  }
  if (node && typeof node === 'object') {
    for (const [key, value] of Object.entries(node as Record<string, unknown>)) {
      collectCompileErrors(value, path ? `${path}.${key}` : key, out)
    }
  }
}

describe('locale messages compile', () => {
  // A translation interpolates HTML into an iframe document with parameter escaping enabled.
  // Rendering the translated text must leave that frame without a usable srcdoc attribute, protecting against GHSA-83rm-6c8r-g728.
  it('neutralizes iframe srcdoc in escaped HTML translations', () => {
    const messages = {
      message: baseCompile('<iframe srcdoc="{content}"></iframe>', { jit: true }).ast,
    }
    const i18n = createI18n({
      legacy: false,
      locale: 'en',
      escapeParameter: true,
      warnHtmlMessage: false,
      messages: { en: messages },
    })
    const template = document.createElement('template')
    template.innerHTML = i18n.global.t('message', { content: '<script>alert(1)</script>' })
    const frame = template.content.querySelector('iframe')
    expect(frame).not.toBeNull()
    expect(frame?.hasAttribute('srcdoc')).toBe(false)
  })

  // Each catalog supplies nested strings that Vue I18n compiles when used.
  // Compiling every string collects no placeholder errors, protecting messages
  // containing literal braces and interpolation syntax.
  it.each([
    ['zh', zh],
    ['en', en],
  ] as const)('%s messages all compile without placeholder errors', (locale, messages) => {
    const errors: string[] = []
    collectCompileErrors(messages, locale, errors)
    expect(errors).toEqual([])
  })
})
