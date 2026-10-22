import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useAdminSettingsStore, useAppStore, useAuthStore } from '@/stores'
import { resolveRouteDisplayKeys } from '@/router/title'

// usePageMeta.ts resolves the translated heading and description shown by the
// shared application layout. Custom menu labels supply configured page names,
// while route display keys account for subscription visibility so purchase copy
// matches the available user controls.

// usePageMeta returns reactive page headings for the shared layout.
// Route metadata, custom menu settings, and subscription entry visibility are
// read reactively, allowing confirmed settings changes to update displayed copy.
export function usePageMeta() {
  const route = useRoute()
  const { t } = useI18n()
  const appStore = useAppStore()
  const authStore = useAuthStore()
  const adminSettingsStore = useAdminSettingsStore()
  const displayKeys = computed(() =>
    resolveRouteDisplayKeys(route, appStore.cachedPublicSettings?.subscription_entry_enabled),
  )

  // pageTitle and pageDescription recompute from route metadata and translations; only
  // title also consults custom menu configuration and heading suppression.
  const pageTitle = computed(() => {
    if (route.meta.hidePageHeading) return ''

    if (route.name === 'CustomPage') {
      const id = route.params.id as string
      const publicItems = appStore.cachedPublicSettings?.custom_menu_items ?? []
      // Public menu entries win duplicate identifiers; administrator menu entries are
      // considered afterward only when the current user has administrator access.
      const menuItem =
        publicItems.find((item) => item.id === id) ??
        (authStore.isAdmin
          ? adminSettingsStore.customMenuItems.find((item) => item.id === id)
          : undefined)
      if (menuItem?.label) return menuItem.label
    }

    const titleKey = displayKeys.value.titleKey
    if (titleKey) return t(titleKey)
    // Routes without a translation key use their literal heading, and absent metadata
    // leaves the layout with an empty title.
    return (route.meta.title as string) || ''
  })

  const pageDescription = computed(() => {
    const descriptionKey = displayKeys.value.descriptionKey
    if (descriptionKey) return t(descriptionKey)
    // A description translation key wins when supplied; otherwise literal metadata
    // provides display text and missing descriptions stay empty.
    return (route.meta.description as string) || ''
  })

  return { pageTitle, pageDescription }
}
