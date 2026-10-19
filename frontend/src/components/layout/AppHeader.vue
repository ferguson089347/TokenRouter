<template>
  <header class="site-header fixed inset-x-0 top-0 z-header border-b border-primary-900/10">
    <!-- 水平内边距与主内容区保持同一条链，两侧边缘在所有断点对齐。 -->
    <div class="flex h-[var(--header-h)] items-center justify-between gap-3 px-4 md:px-6 lg:px-8">
      <!-- 品牌展示在全局顶栏。 -->
      <div class="flex min-w-0 shrink-0 items-center gap-2 sm:gap-4">
        <button
          v-if="!publicPage && !isCreativeStudio"
          @click="appStore.toggleMobileSidebar()"
          class="btn-ghost btn-icon lg:hidden"
          :aria-label="t('common.toggleMenu')"
          :title="t('common.toggleMenu')"
        >
          <Icon name="menu" size="md" />
        </button>

        <!-- 版本标签与首页链接分别渲染为独立控件。 -->
        <div class="header-brand flex min-w-0 items-center gap-2.5 rounded-control px-1.5 py-1 transition-colors hover:bg-primary-100/70 dark:hover:bg-dark-700">
          <router-link
            :to="homePath"
            class="flex h-8 w-8 shrink-0 items-center justify-center overflow-hidden rounded-control bg-primary-100 dark:bg-dark-800"
            :aria-label="siteName"
          >
            <img v-if="settingsLoaded" :src="siteLogo || '/logo.svg'" :alt="siteName" class="h-full w-full object-contain" />
          </router-link>
          <span class="hidden min-w-0 sm:block">
            <router-link
              :to="homePath"
              class="block max-w-44 truncate text-base font-bold leading-tight text-gray-900 dark:text-white"
            >{{ siteName }}</router-link>
            <VersionBadge :version="siteVersion" />
          </span>
        </div>

        <!-- 操作台的返回入口紧邻品牌右侧，与首页链接分开。 -->
        <router-link
          v-if="isCreativeStudio"
          to="/dashboard"
          class="btn-ghost btn-icon shrink-0"
          :aria-label="t('creative.canvas.backToDashboard')"
          :title="t('creative.canvas.backToDashboard')"
        >
          <Icon name="home" size="md" />
        </router-link>
      </div>

      <!-- 右侧分为工具区和账户区：工具区是同尺寸图标按钮，账户区是余额按钮和头像。 -->
      <div class="header-status-actions">
        <div class="header-status-icon-group">
          <router-link
            v-if="publicPage"
            to="/models"
            class="header-status-icon-button hidden sm:flex"
            :aria-label="t('nav.modelMarketplace')"
            :title="t('nav.modelMarketplace')"
          >
            <Icon name="modelMarketplace" size="md" />
          </router-link>

          <div v-if="user" class="hidden sm:block">
            <AnnouncementBell variant="status" />
          </div>

          <a
            v-if="docUrl"
            :href="docUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="header-status-icon-button hidden sm:flex"
            :aria-label="t('nav.docs')"
            :title="t('nav.docs')"
          >
            <Icon name="book" size="md" />
          </a>

          <HeaderContactSupport />

          <LocaleSwitcher variant="status" />

          <!-- 登录后主题切换收进用户菜单，未登录时仍在顶栏保留入口。 -->
          <button
            v-if="!user"
            type="button"
            data-testid="theme-toggle"
            class="header-status-icon-button"
            :aria-label="isDark ? t('nav.lightMode') : t('nav.darkMode')"
            :title="isDark ? t('nav.lightMode') : t('nav.darkMode')"
            @click="toggleTheme"
          >
            <!-- 太阳和月亮保留各自的颜色，让主题切换一眼可辨。 -->
            <Icon
              :name="isDark ? 'sun' : 'moon'"
              size="md"
              :class="isDark ? 'text-amber-500' : 'text-blue-500'"
            />
          </button>
        </div>

        <template v-if="user">
          <div class="header-status-divider hidden sm:block"></div>

          <!-- 余额按钮右侧的状态点表示订阅用量，点击展开订阅详情；窄屏余额收进用户菜单。 -->
          <SubscriptionProgressMini variant="status" class="hidden sm:block">
            <span class="text-primary-900/60 dark:text-dark-400">{{ balanceUnitSymbol }}</span>
            <span class="font-semibold tabular-nums text-primary-900 dark:text-dark-100">
              {{ formatHeaderMoney(availableBalance, false) }}
            </span>
            <span
              v-if="frozenBalance > 0"
              class="ml-1 text-xs font-medium text-amber-600 dark:text-amber-300"
              :title="balanceFrozenLabel"
            >
              {{ balanceFrozenLabel }}
            </span>
          </SubscriptionProgressMini>
        </template>

        <!-- 用户下拉菜单入口只保留头像，尺寸与图标按钮等高。 -->
        <div v-if="user" class="relative" ref="dropdownRef">
          <button
            @click="toggleDropdown"
            class="header-status-user-button"
            :aria-label="t('common.userMenu')"
          >
            <UserAvatar
              :avatar-url="avatarUrl"
              :user-id="user.id"
              :alt="displayName"
              size-class="h-9 w-9"
            />
          </button>

          <!-- 用户菜单：账户卡、导航、联系方式和退出登录分区排列，菜单项内缩并使用圆角悬停底色。 -->
          <MotionTransition name="dropdown-fade">
            <div v-if="dropdownOpen" class="dropdown user-menu-panel right-0 z-50 mt-2 w-72 origin-top-right py-0">
              <div class="menu-section">
                <!-- 账户卡同时作为个人资料入口，右侧齿轮提示可进入设置。 -->
                <router-link to="/profile" @click="closeDropdown" class="user-menu-card">
                  <UserAvatar
                    :avatar-url="avatarUrl"
                    :user-id="user.id"
                    :alt="displayName"
                    size-class="h-9 w-9 shrink-0"
                  />
                  <span class="min-w-0 flex-1">
                    <span class="block truncate text-sm font-medium text-primary-900 dark:text-dark-50">
                      {{ displayName }}
                    </span>
                    <span class="block truncate text-xs text-primary-900/60 dark:text-dark-400">
                      {{ user.email }}
                    </span>
                  </span>
                  <Icon name="cog" size="sm" class="shrink-0 text-primary-900/45 dark:text-dark-400" />
                </router-link>

                <!-- 窄屏顶栏不显示余额，放在账户卡下方。 -->
                <div class="flex items-baseline justify-between gap-3 px-2.5 pb-1 pt-2 sm:hidden">
                  <span class="text-xs text-primary-900/60 dark:text-dark-400">{{ t('common.balance') }}</span>
                  <span class="text-right">
                    <span class="block text-sm font-semibold tabular-nums text-primary-900 dark:text-dark-50">
                      {{ formatHeaderMoney(availableBalance) }}
                    </span>
                    <span v-if="frozenBalance > 0" class="block text-xs text-amber-600 dark:text-amber-300">
                      {{ balanceFrozenLabel }}
                    </span>
                  </span>
                </div>
              </div>

              <!-- 导航分组与侧栏条目、图标和功能开关保持一致。 -->
              <div v-for="group in menuNavGroups" :key="group.key" class="menu-section">
                <router-link
                  v-for="item in group.items"
                  :key="item.path"
                  :to="item.path"
                  @click="closeDropdown"
                  class="menu-item"
                >
                  <Icon :name="item.icon" size="md" class="shrink-0" />
                  {{ item.label }}
                </router-link>
              </div>

              <!-- 管理员附加入口：项目仓库和新手引导。 -->
              <div v-if="authStore.isAdmin" class="menu-section">
                <a
                  href="https://github.com/TokenFlux/TokenRouter"
                  target="_blank"
                  rel="noopener noreferrer"
                  @click="closeDropdown"
                  class="menu-item"
                >
                  <svg class="h-5 w-5 shrink-0" fill="currentColor" viewBox="0 0 24 24">
                    <path
                      fill-rule="evenodd"
                      clip-rule="evenodd"
                      d="M12 2C6.477 2 2 6.477 2 12c0 4.42 2.865 8.17 6.839 9.49.5.092.682-.217.682-.482 0-.237-.008-.866-.013-1.7-2.782.604-3.369-1.34-3.369-1.34-.454-1.156-1.11-1.464-1.11-1.464-.908-.62.069-.608.069-.608 1.003.07 1.531 1.03 1.531 1.03.892 1.529 2.341 1.087 2.91.831.092-.646.35-1.086.636-1.336-2.22-.253-4.555-1.11-4.555-4.943 0-1.091.39-1.984 1.029-2.683-.103-.253-.446-1.27.098-2.647 0 0 .84-.269 2.75 1.025A9.578 9.578 0 0112 6.836c.85.004 1.705.114 2.504.336 1.909-1.294 2.747-1.025 2.747-1.025.546 1.377.203 2.394.1 2.647.64.699 1.028 1.592 1.028 2.683 0 3.842-2.339 4.687-4.566 4.935.359.309.678.919.678 1.852 0 1.336-.012 2.415-.012 2.743 0 .267.18.578.688.48C19.138 20.167 22 16.418 22 12c0-5.523-4.477-10-10-10z"
                    />
                  </svg>
                  {{ t('nav.github') }}
                </a>

                <button v-if="showOnboardingButton" type="button" @click="handleReplayGuide" class="menu-item">
                  <Icon name="questionCircle" size="md" class="shrink-0" />
                  {{ t('onboarding.restartTour') }}
                </button>
              </div>

              <div class="menu-section">
                <button type="button" @click="handleLogout" class="menu-item menu-item-danger">
                  <Icon name="logout" size="md" />
                  {{ t('nav.logout') }}
                </button>
              </div>

              <!-- 主题三段式切换放在菜单底部，使用共用的分段样式，与顶部账户卡同一种灰底。 -->
              <div class="menu-section">
                <div v-segmented class="segmented grid grid-cols-3 gap-1" role="radiogroup" :aria-label="t('nav.theme')">
                  <button
                    v-for="option in themeOptions"
                    :key="option.mode"
                    type="button"
                    role="radio"
                    :aria-checked="themeMode === option.mode"
                    :aria-label="option.label"
                    :title="option.label"
                    :data-testid="`theme-mode-${option.mode}`"
                    :class="['segmented-item flex items-center justify-center py-1.5', themeMode === option.mode && 'segmented-item-active']"
                    @click="setThemeMode(option.mode)"
                  >
                    <Icon :name="option.icon" size="sm" />
                  </button>
                </div>
              </div>
            </div>
          </MotionTransition>
        </div>

        <router-link v-else-if="publicPage" to="/login" class="btn btn-primary">
          {{ t('home.login') }}
        </router-link>
      </div>
    </div>
  </header>
</template>

<script setup lang="ts">
import { vSegmented } from '@/directives/segmented'
import MotionTransition from '@/components/common/MotionTransition.vue'
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useAppStore, useAuthStore, useOnboardingStore } from '@/stores'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import SubscriptionProgressMini from '@/components/common/SubscriptionProgressMini.vue'
import AnnouncementBell from '@/components/common/AnnouncementBell.vue'
import UserAvatar from '@/components/common/UserAvatar.vue'
import HeaderContactSupport from '@/components/layout/HeaderContactSupport.vue'
import Icon from '@/components/icons/Icon.vue'
import type { IconName } from '@/components/icons'
import VersionBadge from '@/components/common/VersionBadge.vue'
import { sanitizeUrl } from '@/utils/url'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import { useTheme, type ThemeMode } from '@/composables/useTheme'

// AppHeader.vue renders the shared brand, account menu, and user controls across
// public and authenticated pages. Site settings determine navigation destinations,
// support access, and optional account actions, while the current user supplies
// identity and balance details displayed by the header.

// 公开页面复用品牌和账户区，只省略侧栏开关并补充访客入口。
withDefaults(defineProps<{ publicPage?: boolean }>(), {
  publicPage: false
})

const router = useRouter()
const route = useRoute()
const { t } = useI18n()
const appStore = useAppStore()
const authStore = useAuthStore()
const onboardingStore = useOnboardingStore()
const { formatBalanceAmount, balanceUnitSymbol } = useBalanceDisplay()
const { isDark, themeMode, setThemeMode, toggleTheme } = useTheme()

const user = computed(() => authStore.user)
const isCreativeStudio = computed(() => route.path === '/creative')
const homePath = '/home'
const siteName = computed(() => appStore.siteName)
const siteLogo = computed(() => sanitizeUrl(appStore.siteLogo || '', { allowRelative: true, allowDataUrl: true }))
const siteVersion = computed(() => appStore.siteVersion)
const settingsLoaded = computed(() => appStore.publicSettingsLoaded)
const dropdownOpen = ref(false)

interface MenuNavItem {
  path: string
  label: string
  icon: IconName
}

// menuNavGroups exposes workspace, support, and billing destinations permitted by the current public site settings and account feature flags.
const menuNavGroups = computed(() => {
  const settings = appStore.cachedPublicSettings
  const paymentEnabled = settings?.payment_enabled === true
  const groups: Array<{ key: string; items: Array<MenuNavItem | false> }> = [
    {
      key: 'workspace',
      items: [
        { path: '/profile', label: t('nav.profile'), icon: 'userCircle' },
        !!appStore.contactInfo?.trim() && { path: '/profile#contact-support', label: t('common.contactSupport'), icon: 'chat' },
        { path: '/dashboard', label: t('nav.dashboard'), icon: 'dashboard' },
        { path: '/usage', label: t('nav.usage'), icon: 'chart' },
        { path: '/keys', label: t('nav.apiKeys'), icon: 'key' },
        settings?.team_enabled !== false && { path: '/team', label: t('nav.team'), icon: 'users' }
      ]
    },
    {
      key: 'billing',
      items: [
        paymentEnabled && { path: '/purchase', label: t('nav.buySubscription'), icon: 'recharge' },
        { path: '/subscriptions', label: t('nav.mySubscriptions'), icon: 'creditCard' },
        paymentEnabled && { path: '/orders', label: t('nav.myOrders'), icon: 'orderList' },
        { path: '/redeem', label: t('nav.redeem'), icon: 'gift' },
        settings?.affiliate_enabled === true && { path: '/affiliate', label: t('nav.affiliate'), icon: 'affiliate' }
      ]
    }
  ]
  return groups.map(group => ({
    key: group.key,
    items: group.items.filter((item): item is MenuNavItem => Boolean(item))
  }))
})

const themeOptions = computed<Array<{ mode: ThemeMode; icon: IconName; label: string }>>(() => [
  { mode: 'light', icon: 'sun', label: t('nav.lightMode') },
  { mode: 'dark', icon: 'moon', label: t('nav.darkMode') },
  { mode: 'system', icon: 'monitor', label: t('nav.systemTheme') }
])
const dropdownRef = ref<HTMLElement | null>(null)
const docUrl = computed(() => sanitizeUrl(appStore.docUrl))
const avatarUrl = computed(() => user.value?.avatar_url?.trim() || '')
const availableBalance = computed(() => Number(user.value?.balance || 0))
const frozenBalance = computed(() => Number(user.value?.frozen_balance || 0))
const balanceFrozenText = computed(() => t('common.frozenBalance') === 'common.frozenBalance' ? '冻结金额' : t('common.frozenBalance'))
const balanceFrozenLabel = computed(() => `${balanceFrozenText.value} ${formatHeaderMoney(frozenBalance.value)}`)

// 只向管理员显示新手引导按钮
const showOnboardingButton = computed(() => {
  return user.value?.role === 'admin'
})

const displayName = computed(() => {
  if (!user.value) return ''
  return user.value.username || user.value.email?.split('@')[0] || ''
})

function toggleDropdown() {
  dropdownOpen.value = !dropdownOpen.value
}

function closeDropdown() {
  dropdownOpen.value = false
}

async function handleLogout() {
  closeDropdown()
  try {
    await authStore.logout()
  } catch (error) {
    // Ignore logout errors - still redirect to login
    console.error('Logout error:', error)
  }
  await router.push('/login')
}

function handleReplayGuide() {
  closeDropdown()
  onboardingStore.replay()
}

// withSymbol 为 false 时只返回数字，供顶栏余额按钮单独排版货币符号。
function formatHeaderMoney(value: number, withSymbol = true) {
  return formatBalanceAmount(Number.isFinite(value) ? value : 0, { fractionDigits: 2, withSymbol })
}

function handleClickOutside(event: MouseEvent) {
  if (dropdownRef.value && !dropdownRef.value.contains(event.target as Node)) {
    closeDropdown()
  }
}

onMounted(() => {
  document.addEventListener('click', handleClickOutside)
})

onBeforeUnmount(() => {
  document.removeEventListener('click', handleClickOutside)
})
</script>

<style scoped>
.header-status-actions {
  @apply ml-auto flex min-w-0 shrink-0 items-center gap-2 sm:gap-5;
}

.header-brand {
  max-width: min(18rem, 42vw);
}

.header-status-icon-group {
  @apply flex items-center gap-1 sm:gap-2;
}

.header-status-divider {
  @apply h-5 w-px shrink-0 bg-primary-900/10 dark:bg-dark-600;
}

.header-status-user-button {
  /* 头像边线使用中性灰，悬停时与通用控件保持一致。 */
  @apply flex h-9 w-9 items-center justify-center rounded-full ring-1 ring-gray-200 transition-shadow hover:ring-black/20 dark:ring-dark-600 dark:hover:ring-dark-500;
}

/* 菜单较长时不超出视口，超出部分在面板内滚动。 */
.user-menu-panel {
  max-height: calc(100dvh - var(--header-h) - 1rem);
  @apply overflow-y-auto;
}

/* 账户卡常驻浅灰底，与下方普通菜单项区分层级，底色与底部主题切换的轨道一致。 */
.user-menu-card {
  @apply flex items-center gap-3 rounded-control bg-gray-50 px-2.5 py-2 transition-colors hover:bg-gray-100;
  @apply dark:bg-dark-800 dark:hover:bg-dark-700;
}
</style>
