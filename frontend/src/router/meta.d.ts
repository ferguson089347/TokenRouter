/**
 * Type definitions for Vue Router meta fields
 * Extends the RouteMeta interface with custom properties
 */

import 'vue-router'

// meta.d.ts declares access requirements and page presentation metadata shared
// by route definitions and navigation guards. Optional feature switches let
// public settings control entry availability while authentication and role
// requirements remain independent checks for every routed page.

declare module 'vue-router' {
  interface RouteMeta {
    /**
     * Whether this route requires authentication
     * @default true
     */
    requiresAuth?: boolean

    /**
     * Whether this route requires admin role
     * @default false
     */
    requiresAdmin?: boolean

    /**
     * Page title for this route
     */
    title?: string

    /**
     * Optional breadcrumb items for navigation
     */
    breadcrumbs?: Array<{
      label: string
      to?: string
    }>

    /**
     * Icon name for this route (for sidebar navigation)
     */
    icon?: string

    /**
     * Whether to hide this route from navigation menu
     * @default false
     */
    hideInMenu?: boolean

    /**
     * Whether this route requires internal payment system to be enabled
     * @default false
     */
    requiresPayment?: boolean

    /**
     * 是否要求风控中心功能开关已启用
     * @default false
     */
    requiresRiskControl?: boolean

    /**
     * 是否要求团队功能开关已启用
     * @default false
     */
    requiresTeam?: boolean

    /**
     * 是否要求用量排行功能开关已启用
     * @default false
     */
    requiresUsageRanking?: boolean

    /** requiresTickets checks support availability before opening user or administrator pages. */
    requiresTickets?: boolean
    /** requiresIntelligence checks the confirmed feature switch before opening user results. */
    requiresIntelligence?: boolean

    /**
     * 是否要求创作台功能开关已启用
     * @default false
     */
    requiresCreative?: boolean

    /**
     * 是否要求邀请返利功能开关已启用
     * @default false
     */
    requiresAffiliate?: boolean

    /**
     * i18n key for the page title
     */
    titleKey?: string

    /**
     * i18n key for the page description
     */
    descriptionKey?: string

    /**
     * 页面自身已经提供标题区时，隐藏 AppLayout 的通用标题。
     */
    hidePageHeading?: boolean

    /**
     * 全屏工作区页面（如创作台）隐藏侧栏，内容区占满可用宽度。
     */
    hideSidebar?: boolean
  }
}
