<template>
  <div class="relative" ref="dropdownRef">
    <button
      type="button"
      @click="toggleDropdown"
      :disabled="switching"
      :class="triggerClass"
      :aria-label="currentLocale?.name"
      :aria-expanded="isOpen"
      :title="currentLocale?.name"
    >
      <!-- The header uses a language icon so its trigger also renders on systems lacking flag emoji. -->
      <Icon v-if="variant === 'status'" name="languages" size="md" />
      <span v-else class="text-base leading-none">{{ currentLocale?.flag }}</span>
      <span :class="variant === 'status' ? 'header-status-action-label' : 'hidden sm:inline'">
        {{ currentLocaleLabel }}
      </span>
      <Icon
        name="chevronDown"
        size="xs"
        class="text-gray-400 transition-transform duration-normal"
        :class="[variant === 'status' && 'hidden md:block', { 'rotate-180': isOpen }]"
        :animate-on-hover="false"
      />
    </button>

    <!-- 语言列表用顶栏的内缩菜单样式，当前语言文字转为品牌色并在行尾打勾。 -->
    <MotionTransition name="dropdown-fade">
      <div
        v-if="isOpen"
        class="dropdown right-0 z-50 mt-2 w-44 origin-top-right py-0"
      >
        <div class="menu-section">
          <button
            v-for="locale in availableLocales"
            :key="locale.code"
            type="button"
            :disabled="switching"
            @click="selectLocale(locale.code)"
            class="menu-item whitespace-nowrap"
            :class="{ 'text-primary-700 dark:text-primary-500': locale.code === currentLocaleCode }"
          >
            <span class="text-base leading-none">{{ locale.flag }}</span>
            <span class="min-w-0 truncate">{{ locale.name }}</span>
            <Icon
              v-if="locale.code === currentLocaleCode"
              name="check"
              size="sm"
              class="ml-auto shrink-0"
              :animate-on-hover="false"
            />
          </button>
        </div>
      </div>
    </MotionTransition>
  </div>
</template>

<script setup lang="ts">
import MotionTransition from '@/components/common/MotionTransition.vue'
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { setLocale, availableLocales } from '@/i18n'

// LocaleSwitcher.vue displays the current language and opens the available
// translations. Its header variant reveals the language code on wider screens,
// while language changes use the shared locale loader and disable the trigger
// until loading finishes.

const props = withDefaults(defineProps<{
  variant?: 'default' | 'status'
}>(), {
  variant: 'default'
})

const { locale } = useI18n()

const isOpen = ref(false)
const dropdownRef = ref<HTMLElement | null>(null)
const switching = ref(false)

const currentLocaleCode = computed(() => locale.value)
const currentLocale = computed(() => availableLocales.find((l) => l.code === locale.value))
const variant = computed(() => props.variant)

// currentLocaleLabel shows the primary language tag in the header, where space
// is shared with navigation; the menu presents each language's full name.
const currentLocaleLabel = computed(() => {
  const code = currentLocale.value?.code
  return (variant.value === 'status' ? code?.split('-')[0] : code)?.toUpperCase()
})

// triggerClass applies shared header action sizing or the standalone selector's
// compact appearance according to the requested variant.
const triggerClass = computed(() => {
  if (variant.value === 'status') {
    return 'header-status-action disabled:cursor-not-allowed disabled:opacity-60'
  }
  return 'flex items-center gap-1.5 rounded-control px-2 py-1.5 text-sm font-medium text-gray-600 transition-colors hover:bg-gray-100 disabled:cursor-not-allowed disabled:opacity-60 dark:text-gray-300 dark:hover:bg-dark-700'
})

function toggleDropdown() {
  isOpen.value = !isOpen.value
}

async function selectLocale(code: string) {
  if (switching.value || code === currentLocaleCode.value) {
    isOpen.value = false
    return
  }
  switching.value = true
  try {
    await setLocale(code)
    isOpen.value = false
  } finally {
    switching.value = false
  }
}

function handleClickOutside(event: MouseEvent) {
  if (dropdownRef.value && !dropdownRef.value.contains(event.target as Node)) {
    isOpen.value = false
  }
}

onMounted(() => {
  document.addEventListener('click', handleClickOutside)
})

onBeforeUnmount(() => {
  document.removeEventListener('click', handleClickOutside)
})
</script>
