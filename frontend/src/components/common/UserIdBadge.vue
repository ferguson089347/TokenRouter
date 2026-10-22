<template>
  <button
    type="button"
    class="inline-flex max-w-full items-center gap-1.5 rounded-control px-2 py-1 text-gray-500 transition-colors hover:bg-gray-100 hover:text-primary-600 dark:text-dark-400 dark:hover:bg-dark-800 dark:hover:text-primary-400"
    :aria-label="t(copied ? 'common.copied' : 'profile.copyUserId')"
    :title="t('profile.copyUserId')"
    data-testid="user-id-copy"
    @click.stop="copyToClipboard(displayedUserId)"
  >
    <span class="shrink-0 text-xs">{{ t('profile.userId') }}</span>
    <span
      class="min-w-0 truncate font-mono text-sm tabular-nums text-gray-900 dark:text-dark-100"
      >{{ displayedUserId }}</span
    >
    <Icon :name="copied ? 'check' : 'copy'" size="xs" class="shrink-0" />
  </button>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { computed } from 'vue'
import Icon from '@/components/icons/Icon.vue'
import { useClipboard } from '@/composables/useClipboard'

// UserIdBadge.vue displays the signed-in account ID with a copy action.
// Profile summaries and the header account menu share the same prefixed value,
// so clicking either control copies the complete identifier, including its #
// prefix, through the application's clipboard feedback.

const props = defineProps<{
  userId: number // stored account identifier
}>()

// Translations label the control, while shared clipboard state supplies copy
// feedback and the confirmation icon after the browser accepts the identifier.
const { t } = useI18n()
const { copied, copyToClipboard } = useClipboard()

// displayedUserId formats the stored user ID with its visible prefix.
const displayedUserId = computed(() => `#${props.userId}`)
</script>
