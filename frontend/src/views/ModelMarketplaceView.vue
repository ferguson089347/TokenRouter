<template>
  <component
    :is="isAuthenticated ? AppLayout : 'div'"
    :class="isAuthenticated ? '' : 'ba-theme-shell relative min-h-screen overflow-hidden'"
  >
    <template v-if="isAuthenticated" #page-heading-actions>
      <div
        class="model-marketplace-toolbar flex w-[calc(100vw-2rem)] max-w-full min-w-0 items-center gap-2 sm:w-auto"
      >
        <div class="min-w-0 flex-1 sm:w-80 sm:flex-none lg:w-[min(24rem,32vw)]">
          <SearchInput
            v-model="search"
            :placeholder="t('marketplace.searchPlaceholder')"
            :debounce-ms="120"
          />
        </div>

        <FilterDropdown
          :active-count="activeFilterCount"
          :columns="3"
          keep-mounted
          @reset="resetPanelFilters"
        >
          <FilterField :label="t('marketplace.filterBrand')">
            <Select v-model="selectedBrand" :options="brandSelectOptions" />
          </FilterField>
          <FilterField :label="t('marketplace.filterPricingMode')">
            <Select v-model="selectedPricingMode" :options="pricingSelectOptions" />
          </FilterField>
          <FilterField :label="t('marketplace.filterGroup')">
            <Select v-model="selectedGroupId" :options="groupSelectOptions" searchable />
          </FilterField>
        </FilterDropdown>
      </div>
    </template>

    <template v-if="!isAuthenticated">
      <div class="ba-theme-backdrop pointer-events-none fixed inset-0"></div>

      <header class="site-header relative z-20 border-b border-primary-900/10 px-4 sm:px-6">
        <nav class="mx-auto flex h-[var(--header-h)] max-w-7xl items-center justify-between gap-4">
          <router-link to="/home" class="flex min-w-0 items-center gap-2.5">
            <span class="h-8 w-8 shrink-0 overflow-hidden rounded-control shadow-sm">
              <img :src="siteLogo || '/logo.svg'" alt="Logo" class="h-full w-full object-contain" />
            </span>
            <span class="truncate text-base font-semibold text-gray-950 dark:text-white">{{
              siteName
            }}</span>
          </router-link>

          <div class="flex items-center gap-2 sm:gap-3">
            <div
              class="hidden items-center gap-5 text-sm font-medium text-gray-600 dark:text-dark-300 md:flex"
            >
              <router-link
                to="/models"
                class="transition hover:text-gray-950 dark:hover:text-white"
              >
                {{ t('home.nav.models') }}
              </router-link>
              <a
                v-if="docUrl"
                :href="docUrl"
                target="_blank"
                rel="noopener noreferrer"
                class="transition hover:text-gray-950 dark:hover:text-white"
              >
                {{ t('home.docs') }}
              </a>
            </div>

            <LocaleSwitcher />

            <button
              type="button"
              @click="toggleTheme"
              class="flex rounded-control text-primary-900/90 transition-colors hover:bg-primary-100 hover:text-primary-900 dark:text-dark-100/80 dark:hover:bg-dark-800 dark:hover:text-white btn-icon"
              :title="isDark ? t('home.switchToLight') : t('home.switchToDark')"
            >
              <Icon v-if="isDark" name="sun" size="md" />
              <Icon v-else name="moon" size="md" />
            </button>

            <router-link
              to="/login"
              class="inline-flex items-center rounded-full bg-gray-950 px-4 py-2 text-xs font-semibold text-white transition hover:bg-gray-800 dark:bg-white dark:text-dark-950 dark:hover:bg-dark-200"
            >
              {{ t('home.login') }}
            </router-link>
          </div>
        </nav>
      </header>
    </template>

    <section
      v-content-reveal="!isAuthenticated && motionRoute?.path"
      :class="isAuthenticated ? 'space-y-4' : 'relative z-10 px-4 pb-12 pt-6 sm:px-6 lg:px-8'"
    >
      <div :class="isAuthenticated ? 'space-y-4' : 'relative mx-auto max-w-[1400px] space-y-5'">
        <div v-if="!isAuthenticated" class="page-heading mb-4">
          <h1 class="page-title">{{ t('marketplace.title') }}</h1>
          <p class="page-description">{{ t('marketplace.subtitle') }}</p>
        </div>
        <div v-if="!isAuthenticated" class="flex min-w-0 items-center gap-2">
          <div class="min-w-0 flex-1 sm:w-80 sm:flex-none xl:w-96">
            <SearchInput
              v-model="search"
              :placeholder="t('marketplace.searchPlaceholder')"
              :debounce-ms="120"
            />
          </div>

          <FilterDropdown
            :active-count="activeFilterCount"
            :columns="3"
            keep-mounted
            @reset="resetPanelFilters"
          >
            <FilterField :label="t('marketplace.filterBrand')">
              <Select v-model="selectedBrand" :options="brandSelectOptions" />
            </FilterField>
            <FilterField :label="t('marketplace.filterPricingMode')">
              <Select v-model="selectedPricingMode" :options="pricingSelectOptions" />
            </FilterField>
            <FilterField :label="t('marketplace.filterGroup')">
              <Select v-model="selectedGroupId" :options="groupSelectOptions" searchable />
            </FilterField>
          </FilterDropdown>
        </div>

        <ModelMarketplaceSkeleton v-if="loading" />

        <div v-else-if="errorMessage" class="card border-red-200 p-6 dark:border-red-500/30">
          <div class="flex flex-col gap-4 md:flex-row md:items-end md:justify-between">
            <div>
              <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{
                t('common.error')
              }}</h2>
              <p class="mt-2 text-sm leading-6 text-gray-600 dark:text-dark-300">{{
                errorMessage
              }}</p>
            </div>
            <button class="btn btn-primary" type="button" @click="fetchMarketplace">
              {{ t('common.refresh') }}
            </button>
          </div>
        </div>

        <div v-else-if="!hasMarketplaceResults" class="card px-6 py-14">
          <div
            class="mx-auto flex h-16 w-16 items-center justify-center rounded-surface bg-primary-50 text-primary-600 dark:bg-primary-500/10 dark:text-primary-300"
          >
            <Icon name="inbox" size="xl" />
          </div>
          <h2 class="mt-6 text-center text-2xl font-semibold text-gray-950 dark:text-white">{{
            t('marketplace.emptyTitle')
          }}</h2>
          <p
            class="mx-auto mt-3 max-w-xl text-center text-sm leading-7 text-gray-600 dark:text-dark-300"
          >
            {{ t('marketplace.emptyDescription') }}
          </p>
          <div class="mt-6 text-center">
            <button class="btn btn-secondary" type="button" @click="resetFilters">
              {{ t('common.reset') }}
            </button>
          </div>
        </div>

        <div v-else class="grid min-w-0 items-start gap-4 lg:grid-cols-[16rem_minmax(0,1fr)]">
          <aside
            class="card min-w-0 overflow-hidden lg:sticky lg:top-[calc(var(--header-h)+1rem)]"
            data-testid="marketplace-group-navigation"
          >
            <!-- Sticky navigation leaves one spacing unit below the fixed application header. -->
            <div
              class="flex items-center justify-between border-b border-gray-100 px-4 py-3 text-xs text-gray-500 dark:border-dark-700 dark:text-dark-400"
            >
              <span>{{ t('marketplace.allGroups') }}</span>
              <span class="tabular-nums">{{ filteredGroups.length }}</span>
            </div>
            <nav
              :aria-label="t('marketplace.groupNavigation')"
              class="flex gap-2 overflow-x-auto p-2 lg:max-h-[calc(100dvh-var(--header-h)-6rem)] lg:flex-col lg:overflow-y-auto"
            >
              <button
                v-for="candidate in filteredGroups"
                :key="candidate.id"
                type="button"
                :data-group-id="candidate.id"
                data-testid="marketplace-group-select"
                :aria-current="activeGroup?.id === candidate.id ? 'true' : undefined"
                aria-controls="marketplace-selected-group"
                class="w-60 shrink-0 rounded-control border p-3 text-left transition-colors lg:w-full"
                :class="
                  activeGroup?.id === candidate.id
                    ? 'border-gray-200 bg-primary-500/8 text-primary-500 dark:border-dark-600'
                    : 'border-transparent text-gray-700 hover:bg-gray-50 hover:text-primary-500 dark:text-dark-200 dark:hover:bg-dark-800'
                "
                @click="activeGroupID = candidate.id"
              >
                <span class="flex min-w-0 items-start gap-2">
                  <ProviderIcon :brand="groupBrandSource(candidate)" size="18px" class="mt-0.5" />
                  <span class="min-w-0 break-words text-sm font-medium">{{ candidate.name }}</span>
                </span>
                <span
                  class="mt-2 flex items-center justify-between gap-2 pl-6 text-xs text-gray-500 dark:text-dark-400"
                >
                  <span>{{
                    t('marketplace.groupModelCount', { count: candidate.model_count })
                  }}</span>
                  <span v-if="!candidate.hide_rate_multiplier" class="tabular-nums">{{
                    formatMultiplier(candidate.rate_multiplier)
                  }}</span>
                </span>
              </button>
            </nav>
          </aside>
          <section
            v-for="group in activeGroup ? [activeGroup] : []"
            :key="group.id"
            id="marketplace-selected-group"
            :data-group-id="group.id"
            data-testid="marketplace-group-section"
            class="min-w-0 space-y-4"
            aria-labelledby="marketplace-selected-group-title"
          >
            <div class="card space-y-4 p-4 md:p-5">
              <div class="min-w-0 flex-1 space-y-3">
                <div class="flex items-start gap-3">
                  <span
                    class="mt-0.5 flex h-12 w-12 shrink-0 items-center justify-center rounded-surface border border-gray-200 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-950"
                  >
                    <ProviderIcon :brand="groupBrandSource(group)" size="28px" />
                  </span>
                  <div class="min-w-0">
                    <h2
                      id="marketplace-selected-group-title"
                      class="text-lg font-semibold text-gray-950 dark:text-white"
                      >{{ group.name }}</h2
                    >
                    <p
                      v-if="group.description"
                      class="mt-1 text-sm leading-6 text-gray-600 dark:text-dark-300"
                    >
                      {{ group.description }}
                    </p>
                    <div class="mt-3 flex flex-wrap items-center gap-2">
                      <span :class="brandBadgeClass(group)">
                        <ProviderIcon :brand="groupBrandSource(group)" size="14px" />
                        {{ groupBrandLabel(group) }}
                      </span>
                      <!-- A group can hide its discount tag; visible discounts require a valid ratio and offer click or hover explanations. -->
                      <HelpTooltip
                        v-if="
                          !group.hide_max_discount &&
                          formatMaxDiscountOff(group.official_price_ratio)
                        "
                        trigger="both"
                        width-class="w-72"
                        :closable="false"
                        :content="t('marketplace.maxDiscountHint')"
                      >
                        <template #trigger>
                          <span
                            data-testid="group-max-discount-tag"
                            class="rounded-full border border-emerald-200 bg-emerald-50 px-3 py-1 text-xs font-semibold text-emerald-700 dark:border-emerald-500/30 dark:bg-emerald-500/10 dark:text-emerald-200"
                          >
                            {{ formatMaxDiscountOff(group.official_price_ratio) }}
                          </span>
                        </template>
                      </HelpTooltip>
                      <HelpTooltip
                        v-if="!group.hide_rate_multiplier"
                        trigger="both"
                        width-class="w-72"
                        :closable="false"
                        :content="t('marketplace.rateMultiplierHint')"
                      >
                        <template #trigger>
                          <!-- Neutral multiplier surfaces remain distinct from the surrounding dark card. -->
                          <span
                            data-testid="group-rate-multiplier-tag"
                            class="rounded-full border border-gray-200/80 bg-gray-50 px-3 py-1 text-xs font-semibold text-gray-600 dark:border-dark-600 dark:bg-dark-700/60 dark:text-dark-200"
                          >
                            {{ formatRateMultiplierLabel(group.rate_multiplier) }}
                          </span>
                        </template>
                      </HelpTooltip>
                    </div>
                  </div>
                </div>
              </div>
              <div
                v-if="group.availability"
                class="flex w-full justify-end border-t border-gray-100 pt-4 dark:border-dark-700"
                data-testid="marketplace-group-availability"
              >
                <!-- The selected group keeps its complete availability summary above the model directory. -->
                <GroupAvailabilityBar
                  :availability="group.availability"
                  class="min-w-0 sm:max-w-lg"
                />
              </div>
            </div>

            <div
              id="marketplace-model-directory"
              class="flex items-center justify-between gap-2 text-xs text-gray-500 dark:text-dark-400 [scroll-margin-top:calc(var(--header-h)+1rem)]"
            >
              <h3>{{ t('marketplace.modelsAndPricing') }}</h3>
              <span>{{ t('marketplace.groupModelCount', { count: group.model_count }) }}</span>
            </div>
            <div class="grid min-w-0 grid-cols-1 items-start gap-4 md:grid-cols-2 2xl:grid-cols-3">
              <!-- Render one selected group and a bounded page so navigation does not mount every model in the directory. -->
              <article
                v-for="model in group.pageModels"
                :key="`${group.id}-${model.id}`"
                class="group min-w-0 max-w-full rounded-surface border border-gray-100 bg-gray-50/80 p-4 transition hover:-translate-y-0.5 hover:border-black/20 hover:shadow-sm dark:border-dark-700 dark:bg-dark-950/80 dark:hover:border-primary-500/50"
              >
                <div class="flex min-w-0 flex-wrap items-start justify-between gap-2">
                  <div class="flex min-w-0 flex-1 basis-32 items-center">
                    <h3
                      class="min-w-0 truncate text-base font-semibold text-gray-950 dark:text-white"
                      >{{ model.display_name }}</h3
                    >
                    <!-- The information button groups attributes and protocols beside the model name, leaving card space for prices. -->
                    <ModelAttributeDetails
                      v-if="model.attributes || marketplaceProtocols(model.protocols).length > 0"
                      :model="model"
                      class="shrink-0"
                    />
                  </div>
                  <ModelCapabilityTags :model="model" />
                </div>
                <!-- The model ID gets its own row so capability tags can share the title row. -->
                <ModelIdLabel :model-id="model.id" class="mt-1" />

                <!-- Price summaries use a borderless list of the quoted billing units. -->
                <div class="mt-4">
                  <template v-if="compactPricingRows(model.pricing).length > 0">
                    <dl class="space-y-2">
                      <div
                        v-for="row in compactPricingRows(model.pricing)"
                        :key="row.key"
                        class="flex items-baseline justify-between gap-3 text-sm"
                      >
                        <dt class="shrink-0 text-gray-500 dark:text-dark-400">{{ row.label }}</dt>
                        <dd
                          class="min-w-0 break-words text-right font-medium tabular-nums [overflow-wrap:anywhere] text-gray-900 dark:text-white"
                          >{{ row.value }}</dd
                        >
                      </div>
                    </dl>
                  </template>
                  <p v-else class="text-sm text-gray-400 dark:text-dark-500">
                    {{ t('marketplace.pricingUnavailable') }}
                  </p>

                  <!-- ModelPricingPanel manages detailed prices, context ranges, and fast-mode selection inside its card. -->
                  <ModelPricingPanel :model="model" />
                </div>
              </article>
            </div>
            <Pagination
              v-if="group.model_count > MODEL_PAGE_SIZE"
              :total="group.model_count"
              :page="group.page"
              :page-size="MODEL_PAGE_SIZE"
              :show-page-size-selector="false"
              @update:page="changeGroupPage(group.id, $event)"
            />
          </section>
        </div>
      </div>
    </section>
  </component>
</template>

<script setup lang="ts">
import { useLocaleRefresh } from '@/composables/useLocaleRefresh'
import { vContentReveal } from '@/directives/contentReveal'
import { useRoute as useMotionRoute } from 'vue-router'
const motionRoute = useMotionRoute()

import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import ModelMarketplaceSkeleton from '@/components/marketplace/ModelMarketplaceSkeleton.vue'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import GroupAvailabilityBar from '@/components/marketplace/GroupAvailabilityBar.vue'
import ModelCapabilityTags from '@/components/marketplace/ModelCapabilityTags.vue'
import ModelAttributeDetails from '@/components/marketplace/ModelAttributeDetails.vue'
import ModelPricingPanel from '@/components/marketplace/ModelPricingPanel.vue'
import ProviderIcon from '@/components/common/ProviderIcon.vue'
import HelpTooltip from '@/components/common/HelpTooltip.vue'
import SearchInput from '@/components/common/SearchInput.vue'
import Select from '@/components/common/Select.vue'
import FilterDropdown from '@/components/common/FilterDropdown.vue'
import FilterField from '@/components/common/FilterField.vue'
import ModelIdLabel from '@/components/common/ModelIdLabel.vue'
import Pagination from '@/components/common/Pagination.vue'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import { initTheme, useTheme } from '@/composables/useTheme'
import { getMarketplaceModels } from '@/api/marketplace'
import {
  providerBrandDisplayName,
  providerBrandFilterKey,
  resolveProviderBrand,
} from '@/utils/providerBrand'
import { formatCompactTokenRange } from '@/utils/formatters'
import { formatPriceNumber, pricingKind, videoPricingEntries } from '@/utils/marketplacePricing'
import { marketplaceProtocols } from '@/utils/marketplaceProtocols'
import { sanitizeUrl } from '@/utils/url'
import type {
  MarketplaceGroup,
  MarketplaceModelPricing,
  MarketplacePricingInterval,
} from '@/api/marketplace.types'

import { useAppStore, useAuthStore } from '@/stores'

// ModelMarketplaceView.vue displays public model groups with searchable pricing
// and capability details. Authenticated visitors use the application layout,
// while guests receive the public page. Model tooltips combine attributes and
// protocols, with route differences shown after both sections. Video summaries
// show the backend's group-scaled per-second quotes and retain free dimensions.
//
// Search and brand, pricing, and group filters determine navigation entries and
// their matching models. One selected group supplies the summary and twelve-model
// page; the first matching group appears when selection is absent or filtered out.
// Selection stores group identity separately from the filter, keeping other groups
// reachable. Each group retains its page during navigation, while changed filters
// reset pages before a new catalog is presented. Model details unmount when their
// group leaves selection, and full-directory filtering includes off-page models.

// MODEL_PAGE_SIZE bounds rendering to twelve cards for the selected group, keeping
// large groups responsive when visitors select them on slower devices.
const MODEL_PAGE_SIZE = 12

// VisibleMarketplaceGroup contains matching models and the selected navigation page.
type VisibleMarketplaceGroup = MarketplaceGroup & {
  page: number // current model page
  pageModels: MarketplaceGroup['models'] // visible page records
}
// PricingFilter selects the public quote category shown in marketplace results.
type PricingFilter = 'all' | 'token' | 'image' | 'video' | 'unpriced'

interface PricingRow {
  key: string
  label: string
  value: string
}

const { t } = useI18n()
const { balanceUnitName } = useBalanceDisplay()

const appStore = useAppStore()
const authStore = useAuthStore()
const { isDark, toggleTheme } = useTheme()

const groups = ref<MarketplaceGroup[]>([])
const loading = ref(true)
const errorMessage = ref('')
const search = ref('')
const selectedBrand = ref<string | 'all'>('all')
const selectedPricingMode = ref<PricingFilter>('all')
const selectedGroupId = ref<number | 'all'>('all')
// activeGroupID retains the requested navigation identity independently of filters.
// activeGroup selects the first match when this identity is absent from results.
const activeGroupID = ref<number | null>(null)
// groupPages stores independent model pages; filter changes reset these positions
// so a new result set starts with its first matching models.
const groupPages = ref<Record<number, number>>({})

const isAuthenticated = computed(() => authStore.isAuthenticated)

const siteName = computed(() => appStore.siteName || 'TokenRouter')
const siteLogo = computed(() =>
  sanitizeUrl(appStore.cachedPublicSettings?.site_logo || appStore.siteLogo || '', {
    allowRelative: true,
    allowDataUrl: true,
  }),
)
const docUrl = computed(() =>
  sanitizeUrl(appStore.cachedPublicSettings?.doc_url || appStore.docUrl || ''),
)

const normalizedSearch = computed(() => search.value.trim().toLowerCase())
watch([normalizedSearch, selectedBrand, selectedPricingMode, selectedGroupId], () => {
  groupPages.value = {}
})

const activeFilterCount = computed(
  () =>
    [
      selectedBrand.value !== 'all' ? selectedBrand.value : '',
      selectedPricingMode.value !== 'all' ? selectedPricingMode.value : '',
      selectedGroupId.value !== 'all' ? selectedGroupId.value : '',
    ].filter(Boolean).length,
)

const sortedGroups = computed(() =>
  [...groups.value].sort((left, right) => {
    const sortDiff = (left.sort_order ?? 0) - (right.sort_order ?? 0)
    if (sortDiff !== 0) {
      return sortDiff
    }
    return left.id - right.id
  }),
)

const availableBrands = computed(() => {
  const seen = new Set<string>()
  const brands: string[] = []
  for (const group of sortedGroups.value) {
    const brand = groupBrandLabel(group)
    const key = brandKey(brand)
    if (seen.has(key)) {
      continue
    }
    seen.add(key)
    brands.push(brand)
  }
  return brands
})

const brandSelectOptions = computed(() => [
  { value: 'all', label: t('marketplace.allBrands') },
  ...availableBrands.value.map((brand) => ({
    value: brand,
    label: brand,
  })),
])

// pricingSelectOptions lists the billing units available in marketplace filters.
const pricingSelectOptions = computed(() => [
  { value: 'all', label: t('marketplace.allTypes') },
  { value: 'token', label: t('marketplace.tokenPricing') },
  { value: 'image', label: t('marketplace.imagePricing') },
  { value: 'video', label: t('marketplace.videoPricing') },
  { value: 'unpriced', label: t('marketplace.unpriced') },
])

const groupSelectOptions = computed(() => [
  { value: 'all', label: t('marketplace.allGroups') },
  ...sortedGroups.value.map((group) => ({
    value: group.id,
    label: group.name,
    search_terms: group.search_terms,
  })),
])

// filteredGroups searches the full model directory and selects one page per
// matching group. Counts describe all matching models, while pageModels bounds
// rendering. Filter changes reset page positions, and a shorter refreshed
// directory clamps a group's page to its last available page.
const filteredGroups = computed<VisibleMarketplaceGroup[]>(() => {
  const keyword = normalizedSearch.value

  return sortedGroups.value.flatMap((group) => {
    if (
      selectedBrand.value !== 'all' &&
      brandKey(groupBrandLabel(group)) !== brandKey(selectedBrand.value)
    ) {
      return []
    }

    if (selectedGroupId.value !== 'all' && group.id !== selectedGroupId.value) {
      return []
    }

    const groupMatchesKeyword =
      !keyword ||
      [
        group.name,
        group.description,
        groupBrandSource(group),
        groupBrandLabel(group),
        ...(group.search_terms || []),
      ]
        .filter(Boolean)
        .some((value) => value.toLowerCase().includes(keyword))

    const models = group.models.filter((model) => {
      if (
        selectedPricingMode.value !== 'all' &&
        pricingKind(model.pricing) !== selectedPricingMode.value
      ) {
        return false
      }

      if (!keyword || groupMatchesKeyword) {
        return true
      }

      return [model.id, model.display_name, ...(model.attributes?.search_terms || [])].some(
        (value) => value.toLowerCase().includes(keyword),
      )
    })

    if (models.length === 0) {
      return []
    }

    const page = Math.min(
      groupPages.value[group.id] ?? 1,
      Math.ceil(models.length / MODEL_PAGE_SIZE),
    )
    const start = (page - 1) * MODEL_PAGE_SIZE
    return [
      {
        ...group,
        model_count: models.length,
        models,
        page,
        pageModels: models.slice(start, start + MODEL_PAGE_SIZE),
      },
    ]
  })
})

const hasMarketplaceResults = computed(() => filteredGroups.value.length > 0)

// activeGroup resolves one matching group for the detail pane. A filtered-out
// selection uses the first matching group, while clearing filters can restore
// the requested group identity and its independently retained model page.
const activeGroup = computed(
  () =>
    filteredGroups.value.find((group) => group.id === activeGroupID.value) ??
    filteredGroups.value[0],
)

// changeGroupPage selects a model page and brings its directory heading into view.
// Each group's page survives navigation, and the heading's scroll margin leaves
// room for the fixed application header before the first visible model card.
function changeGroupPage(groupId: number, page: number): void {
  groupPages.value[groupId] = page
  document.getElementById('marketplace-model-directory')?.scrollIntoView({ block: 'start' })
}

function hasPositiveValue(value?: number | null): value is number {
  return typeof value === 'number' && value > 0
}

function hasContextIntervalPricing(pricing: MarketplaceModelPricing): boolean {
  // The backend omits unpriced ranges; returned zero-price intervals still need their range labels for interpretation.
  return (pricing.context_intervals?.length ?? 0) > 0
}

// Panel reset clears dropdown filters, while empty-state reset also clears the search term so catalog results can return.
function resetPanelFilters() {
  selectedBrand.value = 'all'
  selectedPricingMode.value = 'all'
  selectedGroupId.value = 'all'
}

function resetFilters() {
  search.value = ''
  resetPanelFilters()
}

function formatMultiplier(multiplier: number): string {
  return `x${multiplier.toFixed(multiplier % 1 === 0 ? 0 : 2)}`
}

// Group multiplier labels use localized spacing rules alongside their formatted numeric values.
function formatRateMultiplierLabel(multiplier: number): string {
  return t('marketplace.rateMultiplierValue', { multiplier: formatMultiplier(multiplier) })
}

// groupDiscountText formats the greatest official-price discount using the home-page ratio calculation. Missing, invalid, or undiscounted ratios return null so callers omit the badge.
function formatMaxDiscountOff(ratio?: number): string | null {
  if (typeof ratio !== 'number' || !Number.isFinite(ratio) || ratio <= 0 || ratio >= 1) {
    return null
  }
  const percent = new Intl.NumberFormat(undefined, {
    maximumFractionDigits: 1,
  }).format((1 - ratio) * 100)
  return t('marketplace.maxDiscountOff', { percent })
}

function formatPrice(value: number): string {
  return `${formatPriceNumber(value)} ${balanceUnitName.value}`
}

function formatPerMillion(value: number): string {
  return `${formatPrice(value * 1_000_000)} ${t('usage.perMillionTokens')}`
}

function formatCompactPerMillion(value: number): string {
  return formatPriceNumber(value * 1_000_000)
}

function formatPerImage(value: number): string {
  return `${formatPrice(value)} ${t('marketplace.perImage')}`
}

function groupBrandSource(group: Pick<MarketplaceGroup, 'display_brand' | 'name'>): string {
  return group.display_brand?.trim() || group.name
}

function groupBrandLabel(group: Pick<MarketplaceGroup, 'display_brand' | 'name'>): string {
  return providerBrandDisplayName(groupBrandSource(group))
}

function brandKey(label: string): string {
  return providerBrandFilterKey(label)
}

function brandBadgeClass(group: MarketplaceGroup): string {
  const base =
    'inline-flex items-center gap-1.5 rounded-full px-3 py-1 text-xs font-semibold ring-1 ring-inset'
  return `${base} ${resolveProviderBrand(groupBrandSource(group)).badgeClass}`
}

function tokenPricingRowsFromValues(
  pricing: MarketplaceModelPricing | MarketplacePricingInterval,
): PricingRow[] {
  const rows: PricingRow[] = []

  if (hasPositiveValue(pricing.input_price_per_token)) {
    rows.push({
      key: 'input',
      label: t('marketplace.input'),
      value: formatPerMillion(pricing.input_price_per_token),
    })
  }
  if (hasPositiveValue(pricing.image_input_price_per_token)) {
    rows.push({
      key: 'image_input',
      label: t('marketplace.imageInput'),
      value: formatPerMillion(pricing.image_input_price_per_token),
    })
  }
  if (hasPositiveValue(pricing.output_price_per_token)) {
    rows.push({
      key: 'output',
      label: t('marketplace.output'),
      value: formatPerMillion(pricing.output_price_per_token),
    })
  }
  if (hasPositiveValue(pricing.cache_write_price_per_token)) {
    rows.push({
      key: 'cache_write',
      label: t('marketplace.cacheWrite'),
      value: formatPerMillion(pricing.cache_write_price_per_token),
    })
  }
  if (hasPositiveValue(pricing.cache_write_1h_price_per_token)) {
    rows.push({
      key: 'cache_write_1h',
      label: t('marketplace.cacheWrite1h'),
      value: formatPerMillion(pricing.cache_write_1h_price_per_token),
    })
  }
  if (hasPositiveValue(pricing.cache_read_price_per_token)) {
    rows.push({
      key: 'cache_read',
      label: t('marketplace.cacheRead'),
      value: formatPerMillion(pricing.cache_read_price_per_token),
    })
  }
  if (hasPositiveValue(pricing.image_output_price_per_token)) {
    rows.push({
      key: 'image_output',
      label: t('marketplace.imageOutput'),
      value: formatPerMillion(pricing.image_output_price_per_token),
    })
  }
  if (hasPositiveValue(pricing.fast_input_price_per_token)) {
    rows.push({
      key: 'fast_input',
      label: t('marketplace.fastInput'),
      value: formatPerMillion(pricing.fast_input_price_per_token),
    })
  }
  if (hasPositiveValue(pricing.fast_image_input_price_per_token)) {
    rows.push({
      key: 'fast_image_input',
      label: t('marketplace.fastImageInput'),
      value: formatPerMillion(pricing.fast_image_input_price_per_token),
    })
  }
  if (hasPositiveValue(pricing.fast_output_price_per_token)) {
    rows.push({
      key: 'fast_output',
      label: t('marketplace.fastOutput'),
      value: formatPerMillion(pricing.fast_output_price_per_token),
    })
  }
  if (hasPositiveValue(pricing.fast_cache_write_price_per_token)) {
    rows.push({
      key: 'fast_cache_write',
      label: t('marketplace.fastCacheWrite'),
      value: formatPerMillion(pricing.fast_cache_write_price_per_token),
    })
  }
  if (hasPositiveValue(pricing.fast_cache_write_1h_price_per_token)) {
    rows.push({
      key: 'fast_cache_write_1h',
      label: t('marketplace.fastCacheWrite1h'),
      value: formatPerMillion(pricing.fast_cache_write_1h_price_per_token),
    })
  }
  if (hasPositiveValue(pricing.fast_cache_read_price_per_token)) {
    rows.push({
      key: 'fast_cache_read',
      label: t('marketplace.fastCacheRead'),
      value: formatPerMillion(pricing.fast_cache_read_price_per_token),
    })
  }
  if (hasPositiveValue(pricing.fast_image_output_price_per_token)) {
    rows.push({
      key: 'fast_image_output',
      label: t('marketplace.fastImageOutput'),
      value: formatPerMillion(pricing.fast_image_output_price_per_token),
    })
  }

  return rows
}

function compactTokenPricingRows(
  pricing: MarketplaceModelPricing | MarketplacePricingInterval,
): PricingRow[] {
  const primaryRows: PricingRow[] = []
  if (hasPositiveValue(pricing.input_price_per_token)) {
    primaryRows.push({
      key: 'input',
      label: t('marketplace.input'),
      value: formatPerMillion(pricing.input_price_per_token),
    })
  }
  if (hasPositiveValue(pricing.output_price_per_token)) {
    primaryRows.push({
      key: 'output',
      label: t('marketplace.output'),
      value: formatPerMillion(pricing.output_price_per_token),
    })
  }
  if (primaryRows.length > 0) {
    return primaryRows
  }

  const rows = tokenPricingRowsFromValues(pricing)
  if (rows.length === 0) {
    return zeroTokenPricingRows()
  }

  return rows.filter((row) => !row.key.startsWith('fast_')).slice(0, 2)
}

function zeroTokenPricingRows(): PricingRow[] {
  return [
    { key: 'input', label: t('marketplace.input'), value: formatPerMillion(0) },
    { key: 'output', label: t('marketplace.output'), value: formatPerMillion(0) },
  ]
}

function compactContextIntervalRows(pricing: MarketplaceModelPricing): PricingRow[] {
  return (
    pricing.context_intervals?.flatMap((interval, index) => {
      const pricedRows = compactIntervalTokenPricingRows(interval)
      const rows = pricedRows.length > 0 ? pricedRows : zeroTokenPricingRows()
      return [
        {
          key: `compact-${interval.min_tokens}-${interval.max_tokens ?? 'up'}-${index}`,
          label: formatCompactTokenRange(interval.min_tokens, interval.max_tokens),
          value: rows.map((row) => `${row.label} ${row.value}`).join(' / '),
        },
      ]
    }) ?? []
  )
}

function compactIntervalTokenPricingRows(pricing: MarketplacePricingInterval): PricingRow[] {
  const rows: PricingRow[] = []
  if (hasPositiveValue(pricing.input_price_per_token)) {
    rows.push({
      key: 'input',
      label: t('marketplace.input'),
      value: formatCompactPerMillion(pricing.input_price_per_token),
    })
  }
  if (hasPositiveValue(pricing.output_price_per_token)) {
    rows.push({
      key: 'output',
      label: t('marketplace.output'),
      value: formatCompactPerMillion(pricing.output_price_per_token),
    })
  }
  if (rows.length > 0) {
    return rows
  }

  return tokenPricingRowsFromValues(pricing)
    .filter((row) => !row.key.startsWith('fast_'))
    .slice(0, 2)
}

/**
 * compactPricingRows selects summary rows with units matching the public quote.
 * Unpriced models return no rows. Token context ranges use compact summaries,
 * while image and video models expose their available unit prices.
 */
function compactPricingRows(pricing: MarketplaceModelPricing): PricingRow[] {
  const kind = pricingKind(pricing)
  if (kind === 'token' && hasContextIntervalPricing(pricing)) {
    return compactContextIntervalRows(pricing)
  }
  if (kind === 'token') {
    return compactTokenPricingRows(pricing)
  }
  if (kind === 'image') {
    return imagePricingRows(pricing)
  }
  if (kind === 'video') {
    return videoPricingRows(pricing)
  }
  return []
}

/**
 * videoPricingRows formats video dimensions using the displayed balance unit.
 * Named dimensions use their supplied labels; the explicit default uses a
 * translated label, and every row retains the per-second billing unit.
 */
function videoPricingRows(pricing: MarketplaceModelPricing): PricingRow[] {
  return videoPricingEntries(pricing).map((item) => ({
    key: item.key,
    label: item.resolution ?? t('marketplace.defaultVideoPrice'),
    value: `${formatPrice(item.price)} ${t('marketplace.perSecond')}`,
  }))
}

function imagePricingRows(pricing: MarketplaceModelPricing): PricingRow[] {
  const values = [
    { key: '1k', label: '1K', price: pricing.image_price_1k },
    { key: '2k', label: '2K', price: pricing.image_price_2k },
    { key: '4k', label: '4K', price: pricing.image_price_4k },
  ]

  return values.flatMap((item) => {
    if (typeof item.price !== 'number' || !Number.isFinite(item.price) || item.price < 0) {
      return []
    }

    return [
      {
        key: item.key,
        label: item.label,
        value: formatPerImage(item.price),
      },
    ]
  })
}

async function fetchMarketplace() {
  loading.value = true
  errorMessage.value = ''

  try {
    groups.value = await getMarketplaceModels()
  } catch (error) {
    console.error('Failed to load marketplace models:', error)
    errorMessage.value =
      typeof error === 'object' && error !== null && 'message' in error
        ? String(error.message)
        : t('common.unknownError')
  } finally {
    loading.value = false
  }
}

onMounted(async () => {
  initTheme()
  authStore.checkAuth()
  if (!appStore.publicSettingsLoaded) {
    await appStore.fetchPublicSettings()
  }
  await fetchMarketplace()
})

useLocaleRefresh(fetchMarketplace)
</script>
