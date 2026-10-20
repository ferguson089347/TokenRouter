<template>
  <button
    type="button"
    class="inline-flex max-w-full items-center gap-1.5 rounded-control px-2 py-1 text-gray-500 transition-colors hover:bg-gray-100 hover:text-primary-600 dark:text-dark-400 dark:hover:bg-dark-800 dark:hover:text-primary-400"
    :aria-label="t(copied ? 'common.copied' : 'profile.copyUserId')"
    :title="t('profile.copyUserId')"
    data-testid="user-id-copy"
    @click.stop="copyToClipboard(String(userId))"
  >
    <span class="shrink-0 text-xs">{{ t('profile.userId') }}</span>
    <span class="min-w-0 truncate font-mono text-sm tabular-nums text-gray-900 dark:text-dark-100">{{ userId }}</span>
    <Icon :name="copied ? 'check' : 'copy'" size="xs" class="shrink-0" />
  </button>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { useClipboard } from '@/composables/useClipboard'

// UserIdBadge.vue displays the signed-in account identifier with a copy action.
// Profile summaries and the header account menu share this control so both copy
// the numeric identifier and use the application's clipboard feedback and browser
// compatibility handling.

defineProps<{
  userId: number // displayed account identifier
}>()

// Translations label the control, while shared clipboard state supplies copy
// feedback and the confirmation icon after the browser accepts the identifier.
const { t } = useI18n()
const { copied, copyToClipboard } = useClipboard()
</script>
