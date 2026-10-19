<template>
  <div class="space-y-4">
    <article v-for="(entry, index) in entries" :key="index" class="space-y-2">
      <p v-if="entry.label" class="text-xs font-medium text-gray-500 dark:text-dark-300">
        {{ entry.label }}
      </p>
      <a
        v-if="entry.url"
        :href="entry.url"
        target="_blank"
        rel="noopener noreferrer"
        class="flex min-w-0 items-center gap-2 text-sm text-primary-700 hover:text-primary-600 dark:text-primary-300"
        @click="emit('activate')"
        ><span class="min-w-0 break-all">{{ entry.value }}</span
        ><Icon name="externalLink" size="sm" class="shrink-0"
      /></a>
      <button
        v-else-if="entry.value"
        type="button"
        class="flex w-full min-w-0 items-center gap-2 text-left text-sm text-gray-800 hover:text-primary-600 dark:text-dark-100"
        :title="t('common.copy')"
        @click="copyValue(entry.value)"
      >
        <span class="min-w-0 flex-1 break-all">{{ entry.value }}</span
        ><Icon name="copy" size="sm" class="shrink-0" />
      </button>
      <button
        v-if="entry.image"
        type="button"
        class="block rounded-control border border-gray-200 bg-white p-2 dark:border-dark-600"
        :aria-label="
          t('common.supportContacts.previewLabel', {
            label: entry.label || t('common.contactSupport')
          })
        "
        @click="showImage(entry)"
      >
        <img
          :src="entry.image"
          :alt="entry.label || t('common.supportContacts.image')"
          loading="lazy"
          referrerpolicy="no-referrer"
          class="h-36 w-36 object-contain"
        /><span class="mt-2 block text-xs text-primary-700">{{
          t('common.supportContacts.preview')
        }}</span>
      </button>
    </article>
    <BaseDialog
      :show="!!preview"
      :title="preview?.label || t('common.contactSupport')"
      width="normal"
      @close="preview = null"
    >
      <img
        v-if="preview"
        :src="preview.image"
        :alt="preview.label || t('common.supportContacts.image')"
        class="mx-auto max-h-[70dvh] max-w-full rounded-control bg-white p-4 object-contain"
        referrerpolicy="no-referrer"
      />
    </BaseDialog>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import { useClipboard } from '@/composables/useClipboard'
import { parseSupportContacts, safeContactUrl, type SupportContact } from '@/utils/supportContacts'

// SupportContactList.vue displays support numbers, browser links, and QR images
// from localized site configuration. The header and profile share these rendering
// rules, and image previews use the common dialog so keyboard access, focus return,
// and mobile sizing follow the rest of the application.

// Display inputs and image selection keep serialized site data separate from validated navigation targets and the current preview dialog.
const props = defineProps<{ value: string }>()
const emit = defineEmits<{ activate: [] }>()
const { t } = useI18n()
const { copyToClipboard } = useClipboard()
const preview = ref<SupportContact | null>(null)
const entries = computed(() =>
  parseSupportContacts(props.value)
    .map((entry) => ({
      ...entry,
      url: safeContactUrl(entry.value),
      image: safeContactUrl(entry.image, true)
    }))
    .filter((entry) => entry.value || entry.image)
)

/** copyValue copies a contact number and closes the owning navigation menu.
 * Failed clipboard writes retain the current menu so the user can select the
 * visible contact text or retry with the browser's available clipboard mechanism.
 */
async function copyValue(value: string) {
  if (await copyToClipboard(value)) emit('activate')
}

/** showImage opens a validated support image in the shared preview dialog. */
function showImage(entry: SupportContact) {
  preview.value = entry
}
</script>
