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
  const displayKeys = computed(() => resolveRouteDisplayKeys(
    route,
    appStore.cachedPublicSettings?.subscription_entry_enabled,
  ))

  const pageTitle = computed(() => {
    if (route.meta.hidePageHeading) return ''

    // 自定义页面优先显示菜单配置的名称。
    if (route.name === 'CustomPage') {
      const id = route.params.id as string
      const publicItems = appStore.cachedPublicSettings?.custom_menu_items ?? []
      const menuItem = publicItems.find((item) => item.id === id)
        ?? (authStore.isAdmin ? adminSettingsStore.customMenuItems.find((item) => item.id === id) : undefined)
      if (menuItem?.label) return menuItem.label
    }

    const titleKey = displayKeys.value.titleKey
    if (titleKey) return t(titleKey)
    return (route.meta.title as string) || ''
  })

  const pageDescription = computed(() => {
    const descriptionKey = displayKeys.value.descriptionKey
    if (descriptionKey) return t(descriptionKey)
    return (route.meta.description as string) || ''
  })

  return { pageTitle, pageDescription }
}
