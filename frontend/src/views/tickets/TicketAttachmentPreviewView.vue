<template>
  <AppLayout>
    <div class="mx-auto max-w-6xl space-y-4">
      <RouterLink
        :to="`${base}/${route.params.id}`"
        class="text-sm text-primary-600 hover:underline dark:text-primary-400"
        >← {{ t('tickets.preview.back') }}</RouterLink
      >
      <TicketAttachmentPreview
        :ticket-id="Number(route.params.id)"
        :attachment-id="Number(route.params.attachmentId)"
        :admin="admin"
      />
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import TicketAttachmentPreview from '@/components/tickets/TicketAttachmentPreview.vue'

// Direct preview routes share the renderer used by the conversation dialog,
// keeping both access paths under the same authentication and feature checks.
// TicketAttachmentPreviewView.vue supplies a direct authenticated route to the
// shared attachment preview component. Personal and administrator routes pass
// their access scope and file identifiers to the same renderer and link back to
// the corresponding conversation.

const props = withDefaults(defineProps<{ admin?: boolean }>(), { admin: false })
const route = useRoute()
const { t } = useI18n()
const base = computed(() => (props.admin ? '/admin/tickets' : '/tickets'))
</script>
