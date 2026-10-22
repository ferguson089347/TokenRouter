import { defineComponent, nextTick } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import ModelMarketplaceView from '../ModelMarketplaceView.vue'
import Pagination from '@/components/common/Pagination.vue'
import { modelIconData } from '@/utils/modelIconData'
import type { MarketplaceGroup, MarketplaceModelPricing } from '@/api/marketplace.types'

// ModelMarketplaceView.spec.ts exercises public model cards, filters, group disclosures, and
// bounded pagination with catalog fixtures. It checks copied identifiers, capability
// metadata, discount explanations, and pricing selection through mounted components while
// replacing layout and network collaborators.

const getMarketplaceModels = vi.hoisted(() => vi.fn())
const checkAuth = vi.hoisted(() => vi.fn())
const fetchPublicSettings = vi.hoisted(() => vi.fn())
const copyToClipboard = vi.hoisted(() => vi.fn())

vi.mock('@/api/marketplace', () => ({
  getMarketplaceModels,
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copied: { value: false }, copyToClipboard }),
}))

vi.mock('@/stores', () => ({
  useAuthStore: () => ({
    isAuthenticated: true,
    isAdmin: false,
    checkAuth,
  }),
  useAppStore: () => ({
    siteName: 'TokenRouter',
    siteLogo: '',
    docUrl: '',
    cachedPublicSettings: null,
    publicSettingsLoaded: true,
    fetchPublicSettings,
  }),
}))

vi.mock('@/composables/useTheme', () => ({
  initTheme: vi.fn(),
  useTheme: () => ({ isDark: { value: true }, toggleTheme: vi.fn() }),
}))

vi.mock('@/composables/useBalanceDisplay', () => ({
  useBalanceDisplay: () => ({
    balanceUnitName: { value: '点' },
  }),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, string>) => {
        if (key === 'marketplace.rateMultiplierValue') {
          return `marketplace.rateMultiplierValue ${params?.multiplier || ''}`
        }
        if (key === 'marketplace.imageRateMultiplierValue') {
          return `marketplace.imageRateMultiplierValue ${params?.multiplier || ''}`
        }
        if (key === 'marketplace.maxDiscountOff') {
          return `marketplace.maxDiscountOff ${params?.percent || ''}`
        }

        return key
      },
    }),
  }
})

const SelectStub = defineComponent({
  name: 'TokenSelectStub',
  props: {
    modelValue: {
      type: [String, Number, Boolean, null],
      default: null,
    },
    options: {
      type: Array,
      default: () => [],
    },
  },
  emits: ['update:modelValue', 'change'],
  template: `
    <div class="select-stub">
      <button
        v-for="option in options"
        :key="String(option.value)"
        type="button"
        :data-testid="'select-option-' + String(option.value)"
        @click="$emit('update:modelValue', option.value); $emit('change', option.value, option)"
      >
        {{ option.label }}
      </button>
    </div>
  `,
})

const SearchInputStub = defineComponent({
  name: 'SearchInput',
  props: {
    modelValue: {
      type: String,
      default: '',
    },
  },
  emits: ['update:modelValue'],
  template: `
    <input
      data-testid="marketplace-search"
      :value="modelValue"
      @input="$emit('update:modelValue', $event.target.value)"
    />
  `,
})

const tokenPricing: MarketplaceModelPricing = {
  pricing_mode: 'token',
  price_status: 'priced',
  input_price_per_token: 0.000001,
  image_input_price_per_token: 0.000003,
  output_price_per_token: 0.000002,
  fast_image_input_price_per_token: 0.000006,
}

const imagePricing: MarketplaceModelPricing = {
  pricing_mode: 'image',
  price_status: 'priced',
  image_price_1k: 0.5,
}

const unpricedPricing: MarketplaceModelPricing = {
  pricing_mode: 'unknown',
  price_status: 'unpriced',
}

function marketplaceFixture(): MarketplaceGroup[] {
  return [
    marketplaceGroup(1, 'Plus', [
      marketplaceModel('gpt-5.5', 'GPT 5.5', tokenPricing),
      marketplaceModel('legacy-unpriced', 'Legacy Unpriced', unpricedPricing),
    ]),
    marketplaceGroup(2, 'Pro', [marketplaceModel('gpt-5.5', 'GPT 5.5', tokenPricing)]),
    marketplaceGroup(3, 'Plus 2', [marketplaceModel('gpt-5.5', 'GPT 5.5', tokenPricing)]),
    marketplaceGroup(4, 'Pro 2', [marketplaceModel('gpt-5.5', 'GPT 5.5', tokenPricing)]),
  ]
}

function marketplaceGroup(
  id: number,
  name: string,
  models: MarketplaceGroup['models'],
): MarketplaceGroup {
  return {
    id,
    name,
    description: `${name} group`,
    platform: 'openai',
    display_brand: 'OpenAI',
    sort_order: id,
    rate_multiplier: id,

    official_price_ratio: id / 10,
    official_price_rmb_equivalent: id,
    capacity: {
      concurrency_used: id,
      concurrency_max: 10,
      sessions_used: id,
      sessions_max: 20,
      rpm_used: id,
      rpm_max: 60,
    },
    model_count: models.length,
    models,
  }
}

function marketplaceModel(
  id: string,
  displayName: string,
  pricing: MarketplaceModelPricing,
): MarketplaceGroup['models'][number] {
  return {
    id,
    display_name: displayName,
    pricing,
  }
}

// mountMarketplace opens fixture groups for model-content assertions unless disabled.
// Tests of the initial folded state pass false, while pricing and capability
// tests reach their cards through the visible group controls.
async function mountMarketplace(expandGroups = true) {
  const wrapper = mount(ModelMarketplaceView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot name="page-heading-actions" /><slot /></div>' },
        RouterLink: { template: '<a><slot /></a>' },
        Icon: { template: '<span />' },
        LoadingSpinner: { template: '<span />' },
        LocaleSwitcher: { template: '<span />' },
        GroupCapacityBadge: { template: '<span data-testid="group-capacity" />' },
        SearchInput: SearchInputStub,
        Select: SelectStub,
      },
    },
  })
  await flushPromises()
  if (expandGroups) {
    for (const toggle of wrapper.findAll('[data-testid="marketplace-group-toggle"]')) {
      await toggle.trigger('click')
    }
    await flushPromises()
  }
  return wrapper
}

// visibleTooltips selects body-mounted tooltip panels whose inline display permits interaction. Hidden teleported panels stay mounted, so assertions inspect their visibility before reading their content.
function visibleTooltips() {
  return Array.from(document.body.querySelectorAll<HTMLElement>('[role="tooltip"]')).filter(
    (el) => el.style.display !== 'none',
  )
}

describe('ModelMarketplaceView', () => {
  beforeEach(() => {
    localStorage.clear()
    getMarketplaceModels.mockResolvedValue(marketplaceFixture())
    checkAuth.mockClear()
    fetchPublicSettings.mockClear()
    copyToClipboard.mockClear()
  })

  it('取数期间保留模型卡片占位，不提前显示空状态', async () => {
    let resolveModels!: (value: MarketplaceGroup[]) => void
    getMarketplaceModels.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolveModels = resolve
        }),
    )
    const wrapper = await mountMarketplace()
    expect(wrapper.find('[data-testid="marketplace-skeleton"]').exists()).toBe(true)
    expect(wrapper.text()).not.toContain('marketplace.emptyTitle')

    resolveModels(marketplaceFixture())
    await flushPromises()
    expect(wrapper.find('[data-testid="marketplace-skeleton"]').exists()).toBe(false)
    expect(wrapper.findAll('[data-testid="marketplace-group-section"]')).toHaveLength(4)
    wrapper.unmount()
  })

  // A fresh visit shows group summaries with every model list folded.
  // Opening one group exposes its cards while neighboring groups remain folded.
  it('starts groups folded and opens each model list independently', async () => {
    const wrapper = await mountMarketplace(false)
    const toggles = wrapper.findAll('[data-testid="marketplace-group-toggle"]')
    const sections = wrapper.findAll('[data-testid="marketplace-group-section"]')
    expect(toggles).toHaveLength(4)
    expect(toggles.every((toggle) => toggle.attributes('aria-expanded') === 'false')).toBe(true)
    expect(wrapper.findAll('article')).toHaveLength(0)
    expect(toggles[0].text()).toContain('marketplace.expandModels')
    await toggles[0].trigger('click')
    await flushPromises()
    expect(toggles[0].attributes('aria-expanded')).toBe('true')
    expect(sections[0].findAll('article')).toHaveLength(2)
    expect(sections[1].findAll('article')).toHaveLength(0)
    expect(toggles[1].attributes('aria-expanded')).toBe('false')
    expect(toggles[0].text()).toContain('marketplace.collapseModels')
    await toggles[0].trigger('click')
    expect(toggles[0].attributes('aria-expanded')).toBe('false')
    expect(sections[0].get('.motion-collapse').attributes('aria-hidden')).toBe('true')
    wrapper.unmount()
  })

  // Large groups render a bounded page, and folding keeps those card instances.
  // Searching still inspects models beyond that page and returns to its first result.
  it('pages large groups, retains folded cards, and searches all models', async () => {
    const models = Array.from({ length: 25 }, (_, index) =>
      marketplaceModel(`model-${index}`, `Model ${index}`, tokenPricing),
    )
    getMarketplaceModels.mockResolvedValueOnce([marketplaceGroup(1, 'Large', models)])
    const wrapper = await mountMarketplace(false)
    const toggle = wrapper.get('[data-testid="marketplace-group-toggle"]')
    await toggle.trigger('click')
    await flushPromises()
    expect(wrapper.findAll('article')).toHaveLength(12)
    expect(wrapper.findAll('article')[0].text()).toContain('Model 0')
    wrapper.getComponent(Pagination).vm.$emit('update:page', 3)
    await flushPromises()
    expect(wrapper.findAll('article')).toHaveLength(1)
    const lastCard = wrapper.get('article').element
    expect(wrapper.get('article').text()).toContain('Model 24')
    await toggle.trigger('click')
    await toggle.trigger('click')
    expect(wrapper.get('article').element).toBe(lastCard)
    await wrapper.get('[data-testid="marketplace-search"]').setValue('model-24')
    await flushPromises()
    expect(wrapper.findAll('article')).toHaveLength(1)
    expect(wrapper.get('article').text()).toContain('Model 24')
    expect(wrapper.findComponent(Pagination).exists()).toBe(false)
    await wrapper.get('[data-testid="marketplace-search"]').setValue('')
    await flushPromises()
    expect(wrapper.getComponent(Pagination).props('page')).toBe(1)
    expect(wrapper.findAll('article')).toHaveLength(12)
    wrapper.unmount()
  })

  it('renders the catalog as separate group sections', async () => {
    const wrapper = await mountMarketplace()

    expect(wrapper.findAll('[data-testid="marketplace-group-section"]')).toHaveLength(4)
  })

  it('用户侧不展示分组容量，并将可用率状态条靠右放置', async () => {
    const fixture = marketplaceFixture()
    fixture[0] = {
      ...fixture[0],
      availability: {
        window_days: 7,
        bucket_minutes: 1440,
        success_count: 6,
        total_count: 7,
        availability_rate: 6 / 7,
        days: [],
      },
    }
    getMarketplaceModels.mockResolvedValue(fixture)

    const wrapper = await mountMarketplace()

    expect(wrapper.findAll('[data-testid="group-capacity"]')).toHaveLength(0)
    expect(wrapper.get('[data-testid="marketplace-group-availability"]').classes()).toContain(
      'xl:w-[560px]',
    )
  })

  it('分组头部用最高优惠标签替换模型数标签，无有效折扣的分组不渲染', async () => {
    const fixture = marketplaceFixture()
    // Ratios at or above one represent ordinary pricing; missing ratios mean the backend supplied no official-price comparison.
    fixture[1] = { ...fixture[1], official_price_ratio: 1.2 }
    fixture[2] = { ...fixture[2], official_price_ratio: undefined }
    getMarketplaceModels.mockResolvedValue(fixture)

    const wrapper = await mountMarketplace()
    const sections = wrapper.findAll('[data-testid="marketplace-group-section"]')

    // Group one has an official-price ratio of one tenth, so its displayed maximum discount is ninety percent.
    expect(sections[0].text()).toContain('marketplace.maxDiscountOff 90')
    expect(sections[0].text()).not.toContain('marketplace.modelsStat')
    expect(sections[1].text()).not.toContain('marketplace.maxDiscountOff')
    expect(sections[2].text()).not.toContain('marketplace.maxDiscountOff')
    expect(sections[3].text()).toContain('marketplace.maxDiscountOff 60')

    // A mobile click opens the discount explanation through the same tooltip used for pointer hover.
    await sections[0].get('[data-testid="group-max-discount-tag"]').trigger('click')
    await nextTick()
    expect(
      visibleTooltips().some((el) => el.textContent?.includes('marketplace.maxDiscountHint')),
    ).toBe(true)
    await sections[0].get('[data-testid="group-rate-multiplier-tag"]').trigger('click')
    await nextTick()
    expect(
      visibleTooltips().some((el) => el.textContent?.includes('marketplace.rateMultiplierHint')),
    ).toBe(true)
  })

  it('模型卡片摘要保留免费默认区间', async () => {
    const fixture = marketplaceFixture()
    fixture[0].models[0].pricing = {
      pricing_mode: 'token',
      price_status: 'priced',
      context_intervals: [
        { min_tokens: 0, max_tokens: 100 },
        { min_tokens: 100, max_tokens: null, input_price_per_token: 0.000002 },
      ],
    }
    getMarketplaceModels.mockResolvedValue(fixture)
    const wrapper = await mountMarketplace()
    const section = wrapper.findAll('[data-testid="marketplace-group-section"]')[0]
    expect(section.text()).toContain('0.00')
    expect(section.text()).toContain('0-100')
    expect(section.text()).toContain('100+')
  })

  it('模型卡片中的模型 ID 支持一键复制', async () => {
    const wrapper = await mountMarketplace()

    // The card identifier action sends its model ID directly to the clipboard helper.
    const cardCopyButtons = wrapper.findAll('[data-testid="model-id-copy"]')
    expect(cardCopyButtons.length).toBeGreaterThan(0)
    await cardCopyButtons[0].trigger('click')
    expect(copyToClipboard).toHaveBeenCalledWith('gpt-5.5')
  })

  it('模型卡片右上角展示输入输出能力标签', async () => {
    const wrapper = await mountMarketplace()

    const sections = wrapper.findAll('[data-testid="marketplace-group-section"]')
    const gptCards = sections[0].findAll('article')
    // The fixture includes image input pricing, so its capabilities include text and image input with text output.
    expect(
      gptCards[0]
        .get('[data-testid="model-capability-tags"]')
        .findAll('[data-modality]')
        .map((tag) => tag.attributes('data-modality')),
    ).toEqual(['input-text', 'input-image', 'output-text'])
    // A model without pricing observations uses the default text input and output capability labels.
    expect(
      gptCards[1]
        .get('[data-testid="model-capability-tags"]')
        .findAll('[data-modality]')
        .map((tag) => tag.attributes('data-modality')),
    ).toEqual(['input-text', 'output-text'])
  })

  it('能力标签优先使用接口下发的模态元数据', async () => {
    const fixture = marketplaceFixture()
    fixture[0] = {
      ...fixture[0],
      models: [
        // Server capability metadata takes precedence over model-name inference, which would otherwise classify this fixture as image input.
        {
          ...marketplaceModel('gpt-image-2', 'GPT Image 2', imagePricing),
          input_modalities: ['text'],
          output_modalities: ['image'],
        },
      ],
    }
    getMarketplaceModels.mockResolvedValue(fixture)

    const wrapper = await mountMarketplace()
    const sections = wrapper.findAll('[data-testid="marketplace-group-section"]')

    expect(
      sections[0]
        .get('[data-testid="model-capability-tags"]')
        .findAll('[data-modality]')
        .map((tag) => tag.attributes('data-modality')),
    ).toEqual(['input-text', 'output-image'])
  })

  it('Gemini Flash 档位展示接口下发的完整音视频输入能力', async () => {
    const fixture = marketplaceFixture()
    // Tier identifiers remain stable while capability icons follow the metadata supplied by the backend.
    fixture[0].models = ['gemini-3.7-flash-tiered', 'gemini-3.8-flash-tiered'].map((id) => ({
      ...marketplaceModel(id, id, imagePricing),
      input_modalities: ['text', 'image', 'audio', 'video'],
      output_modalities: ['text'],
    }))
    getMarketplaceModels.mockResolvedValue(fixture)
    const wrapper = await mountMarketplace()
    const cards = wrapper.findAll('[data-testid="marketplace-group-section"]')[0].findAll('article')
    expect(cards).toHaveLength(2)
    for (const card of cards) {
      expect(
        card
          .get('[data-testid="model-capability-tags"]')
          .findAll('[data-modality]')
          .map((tag) => tag.attributes('data-modality')),
      ).toEqual(['input-text', 'input-image', 'input-audio', 'input-video', 'output-text'])
    }
  })

  it('模型信息浮层按目录顺序列出可用协议并标出原生协议，没有属性时也显示入口', async () => {
    getMarketplaceModels.mockResolvedValueOnce([
      marketplaceGroup(1, 'Plus', [
        {
          ...marketplaceModel('gpt-5.5', 'GPT 5.5', tokenPricing),
          protocols: ['openai_chat_completions', 'anthropic_messages'],
          native_protocols: ['openai_chat_completions'],
        },
        marketplaceModel('legacy-unpriced', 'Legacy Unpriced', unpricedPricing),
      ]),
    ])
    const wrapper = await mountMarketplace()
    const triggers = wrapper.findAll('[data-testid="model-attributes-trigger"]')
    expect(triggers).toHaveLength(1)

    await triggers[0].trigger('click')
    await nextTick()
    const tooltip = visibleTooltips().find((el) =>
      el.querySelector('[data-testid="model-protocols"]'),
    )
    expect(tooltip).toBeDefined()
    const chips = Array.from(tooltip!.querySelectorAll<HTMLElement>('[data-protocol]'))
    expect(chips.map((chip) => chip.dataset.protocol)).toEqual([
      'anthropic_messages',
      'openai_chat_completions',
    ])
    expect(chips.map((chip) => chip.title)).toEqual([
      'POST /v1/messages · marketplace.protocolConvertedHint',
      'POST /v1/chat/completions · marketplace.protocolNativeHint',
    ])
    expect(chips.map((chip) => chip.dataset.native)).toEqual(['false', 'true'])
    expect(tooltip!.querySelector('[data-testid="model-protocols-native-legend"]')).not.toBeNull()
    expect(chips[0].textContent).toContain('marketplace.protocolNames.anthropic_messages')
    wrapper.unmount()
  })

  it('xAI 分组展示品牌图形', async () => {
    const fixture = marketplaceFixture()
    fixture[0] = {
      ...fixture[0],
      name: 'Grok',
      display_brand: 'xAI',
    }
    getMarketplaceModels.mockResolvedValue(fixture)

    const wrapper = await mountMarketplace()

    // The xAI brand resolves to the shared Grok icon rather than a generated initial.
    const grokGroup = wrapper
      .findAll('[data-testid="marketplace-group-section"]')
      .find((section) => section.get('h2').text() === 'Grok')
    expect(grokGroup?.find('.provider-icon[width="28px"]').exists()).toBe(true)
    expect(grokGroup?.find('.provider-icon-fallback').exists()).toBe(false)
  })

  // Group names and display brands both resolve Zhipu through the shared mapping used for both icon sizes.
  it.each([
    ['GLM', ''],
    ['zai', ''],
    ['Z.AI', ''],
    ['zai Pro', ''],
    ['自定义分组', '智谱'],
    ['自定义分组', 'zai'],
    ['自定义分组', 'z.ai'],
  ])('分组 %s、展示品牌 %s 显示智谱图标', async (name, displayBrand) => {
    getMarketplaceModels.mockResolvedValue([
      {
        ...marketplaceGroup(1, name, [marketplaceModel('glm-5', 'GLM 5', tokenPricing)]),
        display_brand: displayBrand,
      },
    ])

    const wrapper = await mountMarketplace()
    const group = wrapper.get('[data-testid="marketplace-group-section"]')

    for (const size of ['14px', '28px']) {
      const icon = group.get(`svg.provider-icon[width="${size}"]`)
      expect(icon.findAll('path').map((path) => path.attributes('d'))).toEqual(
        modelIconData.zhipu.paths,
      )
    }
    expect(group.find('.provider-icon-fallback').exists()).toBe(false)
    expect(group.text()).toContain('智谱')
    wrapper.unmount()
  })

  it('只展示统一分组倍率', async () => {
    const fixture = marketplaceFixture()
    fixture[0] = {
      ...fixture[0],

      models: [marketplaceModel('gpt-image-1', 'GPT Image', imagePricing)],
    }
    getMarketplaceModels.mockResolvedValue(fixture)

    const wrapper = await mountMarketplace()

    expect(
      wrapper
        .findAll('[data-testid="marketplace-group-section"]')
        .map((section) => section.text())
        .join('\n'),
    ).not.toContain('marketplace.imageRateMultiplierValue')
  })

  it('模型卡片可展开抽屉式定价面板并切换 fast mode', async () => {
    const wrapper = await mountMarketplace()

    const toggle = wrapper.get('[data-testid="model-pricing-toggle"]')
    expect(toggle.attributes('aria-expanded')).toBe('false')

    await toggle.trigger('click')
    await nextTick()

    // Expanding the model panel reveals its ordinary pricing rows and the speed selection control inside the same card.
    const sections = wrapper.findAll('[data-testid="marketplace-group-section"]')
    const firstCard = sections[0].findAll('article')[0]
    expect(firstCard.text()).toContain('marketplace.input')
    expect(firstCard.text()).toContain('marketplace.imageInput')
    expect(firstCard.text()).toContain('marketplace.output')

    const fastSwitch = firstCard.get('[data-testid="pricing-fast-switch"]')
    await fastSwitch.findAll('button')[1].trigger('click')
    await nextTick()

    // Fast selection reveals the corresponding surcharge rows and hides the ordinary pricing rows.
    expect(firstCard.text()).toContain('marketplace.fastImageInput')
    expect(firstCard.text()).not.toContain('marketplace.imageInput')

    await wrapper.get('[data-testid="model-pricing-toggle"]').trigger('click')
    await nextTick()
    expect(wrapper.get('[data-testid="model-pricing-toggle"]').attributes('aria-expanded')).toBe(
      'false',
    )
  })
})

// Public model cards display the quoted video unit and expose its filter.
// Selecting video pricing removes token models while retaining free video tiers.
it('shows and filters custom video prices', async () => {
  getMarketplaceModels.mockResolvedValueOnce([
    marketplaceGroup(1, 'Videos', [
      marketplaceModel('custom-video', 'Custom Video', {
        pricing_mode: 'video',
        price_status: 'priced',
        video_prices: { '768p': 0.12, '2k': 0 },
      }),
      marketplaceModel('text-model', 'Text Model', tokenPricing),
    ]),
  ])
  const wrapper = await mountMarketplace()
  expect(wrapper.text()).toContain('768p')
  expect(wrapper.text()).toContain('marketplace.perSecond')
  await wrapper.get('[data-testid="select-option-video"]').trigger('click')
  expect(wrapper.text()).toContain('Custom Video')
  expect(wrapper.text()).not.toContain('Text Model')
  wrapper.unmount()
})
