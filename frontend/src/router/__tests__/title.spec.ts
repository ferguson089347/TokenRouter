import { describe, expect, it } from 'vitest'
import { resolveDocumentTitle, resolveRouteDocumentTitle, resolveRouteDisplayKeys } from '@/router/title'

describe('resolveDocumentTitle', () => {
  it('路由存在标题时，使用“路由标题 - 站点名”格式', () => {
    expect(resolveDocumentTitle('Usage Records', 'My Site')).toBe('Usage Records - My Site')
  })

  it('路由无标题时，回退到站点名', () => {
    expect(resolveDocumentTitle(undefined, 'My Site')).toBe('My Site')
  })

  it('站点名为空时，回退默认站点名', () => {
    expect(resolveDocumentTitle('Dashboard', '')).toBe('Dashboard - TokenRouter')
    expect(resolveDocumentTitle(undefined, '   ')).toBe('TokenRouter')
  })

  it('站点名变更时仅影响后续路由标题计算', () => {
    const before = resolveDocumentTitle('Admin Dashboard', 'Alpha')
    const after = resolveDocumentTitle('Admin Dashboard', 'Beta')

    expect(before).toBe('Admin Dashboard - Alpha')
    expect(after).toBe('Admin Dashboard - Beta')
  })
})

describe('resolveRouteDocumentTitle', () => {
  it('自定义页面菜单加载后，使用菜单名称作为标题', () => {
    const route = {
      name: 'CustomPage',
      params: { id: 'scheduler' },
      meta: {
        title: 'Custom Page'
      }
    }

    expect(resolveRouteDocumentTitle(route, 'EzouAPI')).toBe('Custom Page - EzouAPI')
    expect(resolveRouteDocumentTitle(route, 'EzouAPI', [
      {
        id: 'scheduler',
        label: '提供商调度器',
        icon_svg: '',
        url: 'https://example.com',
        visibility: 'admin',
        sort_order: 0
      }
    ])).toBe('提供商调度器 - EzouAPI')
  })
})

// Purchase copy must follow navigation visibility across shared page and tab
// title callers. Other routes retain their metadata when subscription entries hide.
describe('purchase display keys', () => {
  const purchaseRoute = {
    name: 'PurchaseSubscription',
    meta: { titleKey: 'nav.buySubscription', descriptionKey: 'purchase.description' },
  }

  // A purchase route can be visited after an administrator hides subscription
  // entries. Its shared display keys must describe balance top-ups in that state.
  it('selects recharge wording when subscription entries are hidden', () => {
    expect(resolveRouteDisplayKeys(purchaseRoute, false)).toEqual({
      titleKey: 'payment.rechargeAccount',
      descriptionKey: 'purchase.rechargeDescription',
    })
  })

  // Older public settings can omit the subscription visibility switch entirely.
  // Omitted settings and enabled settings must both keep the combined purchase wording.
  it('keeps combined purchase wording for enabled and omitted settings', () => {
    expect(resolveRouteDisplayKeys(purchaseRoute)).toEqual(purchaseRoute.meta)
    expect(resolveRouteDisplayKeys(purchaseRoute, true)).toEqual(purchaseRoute.meta)
  })

  // Subscription visibility applies to the purchase route's display text.
  // A profile route keeps its configured heading and description when that setting is disabled.
  it('keeps another route unchanged when subscription entries are hidden', () => {
    const profileRoute = { name: 'Profile', meta: { titleKey: 'profile.title', descriptionKey: 'profile.description' } }
    expect(resolveRouteDisplayKeys(profileRoute, false)).toEqual(profileRoute.meta)
  })
})
