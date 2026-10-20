import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

const componentPath = resolve(dirname(fileURLToPath(import.meta.url)), '../AppHeader.vue')
const componentSource = readFileSync(componentPath, 'utf8')

describe('AppHeader theme toggle', () => {
  it('keeps the header theme button only for guests', () => {
    const themeIndex = componentSource.indexOf('data-testid="theme-toggle"')
    const themeStart = componentSource.lastIndexOf('<button', themeIndex)
    const themeEnd = componentSource.indexOf('</button>', themeIndex)
    const themeButton = componentSource.slice(themeStart, themeEnd)

    expect(themeIndex).toBeGreaterThanOrEqual(0)
    expect(themeButton).toContain('v-if="!user"')
    expect(themeButton).toContain('class="header-status-icon-button"')
  })

  it('offers light, dark and system modes at the bottom of the user menu', () => {
    const logoutIndex = componentSource.indexOf("{{ t('nav.logout') }}")
    const modeIndex = componentSource.indexOf(':data-testid="`theme-mode-${option.mode}`"')

    expect(modeIndex).toBeGreaterThan(logoutIndex)
    expect(componentSource).toContain('@click="setThemeMode(option.mode)"')
    expect(componentSource).toContain("{ mode: 'system', icon: 'monitor', label: t('nav.systemTheme') }")
  })

  // Mobile headers reserve space for branding and account access. Secondary links
  // remain hidden below sm while utility spacing stays compact.
  it('hides lower-priority header actions on mobile', () => {
    expect(componentSource).toContain('<div v-if="user" class="hidden sm:block">')
    expect(componentSource).toContain('class="header-status-action hidden sm:flex"')
    expect(componentSource).toContain('@apply flex items-center gap-1 sm:gap-2;')
  })
})

describe('AppHeader positioning', () => {
  it('keeps the global header outside document scrolling', () => {
    expect(componentSource).toContain('class="site-header fixed inset-x-0 top-0 z-header')
  })
})

describe('AppHeader user menu', () => {
  it('filters optional entries with the same feature flags as the sidebar', () => {
    expect(componentSource).toContain("settings?.team_enabled !== false && { path: '/team'")
    expect(componentSource).toContain("paymentEnabled && { path: '/purchase'")
    expect(componentSource).toContain("paymentEnabled && { path: '/orders'")
    expect(componentSource).toContain("settings?.affiliate_enabled === true && { path: '/affiliate'")
  })

  it('moves contact support into its own header icon', () => {
    expect(componentSource).toContain('<HeaderContactSupport />')
    expect(componentSource).not.toContain('contactEntries')
  })
})
