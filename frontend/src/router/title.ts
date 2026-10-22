import { i18n } from '@/i18n'
import type { RouteLocationNormalizedLoaded } from 'vue-router'
import type { CustomMenuItem } from '@/api/settings.types'

// title.ts resolves route display keys and browser document titles from route
// metadata, configured menu names, and subscription entry visibility. The shared
// layout and browser title callers use the same purchase-page wording when the
// site exposes balance top-ups with subscription navigation hidden.
//
// Title callers run during navigation, when public settings change, and after a
// language switch. Each passes the subscription entry choice from public
// settings, with omitted values using visible subscriptions. This keeps the
// route's title keys consistent across these updates while allowing custom-page
// menu labels to name configured destinations.

// resolveDocumentTitle formats a translated route title with the site name.
// Unavailable translations use a supplied static title, and missing route titles
// produce the normalized site name alone.
export function resolveDocumentTitle(
  routeTitle: unknown,
  siteName?: string,
  titleKey?: string,
): string {
  const normalizedSiteName =
    typeof siteName === 'string' && siteName.trim() ? siteName.trim() : 'TokenRouter'

  if (typeof titleKey === 'string' && titleKey.trim()) {
    const translated = i18n.global.t(titleKey)
    if (translated && translated !== titleKey) {
      return `${translated} - ${normalizedSiteName}`
    }
  }

  // A missing or untranslated title key leaves the literal route title eligible, so a
  // translation lookup never replaces it with the key text.
  if (typeof routeTitle === 'string' && routeTitle.trim()) {
    return `${routeTitle.trim()} - ${normalizedSiteName}`
  }

  return normalizedSiteName
}

// resolveRouteDisplayKeys selects title and description keys for the route.
// Subscription entry visibility defaults to enabled when public settings omit
// the switch, and a hidden purchase entry selects balance top-up wording.
export function resolveRouteDisplayKeys(
  route: Pick<RouteLocationNormalizedLoaded, 'name' | 'meta'>,
  subscriptionEntryEnabled = true,
) {
  if (route.name === 'PurchaseSubscription' && !subscriptionEntryEnabled) {
    return { titleKey: 'payment.rechargeAccount', descriptionKey: 'purchase.rechargeDescription' }
  }
  return {
    titleKey: route.meta.titleKey as string | undefined,
    descriptionKey: route.meta.descriptionKey as string | undefined,
  }
}

// resolveRouteDocumentTitle builds the browser title from a route and its visible
// menu settings. A matching custom-page menu label takes precedence over translated
// route metadata, so visitors see their configured page name. For the purchase
// route, subscriptionEntryEnabled selects the same translation key used by the
// content heading. The resulting label and normalized site name use
// resolveDocumentTitle's formatting and translation handling.
export function resolveRouteDocumentTitle(
  route: Pick<RouteLocationNormalizedLoaded, 'name' | 'params' | 'meta'>,
  siteName: string | undefined,
  customMenuItems: CustomMenuItem[] = [],
  subscriptionEntryEnabled = true,
): string {
  const id = typeof route.params.id === 'string' ? route.params.id : ''
  const menuItem =
    route.name === 'CustomPage' && id ? customMenuItems.find((item) => item.id === id) : undefined
  const menuTitle = menuItem?.label.trim()
  const { titleKey } = resolveRouteDisplayKeys(route, subscriptionEntryEnabled)

  return resolveDocumentTitle(
    menuTitle || route.meta.title,
    siteName,
    menuTitle ? undefined : titleKey,
  )
}
