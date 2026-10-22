<template>
  <BaseDialog :show="show" :title="t('admin.users.userApiKeys')" width="wide" @close="handleClose">
    <div v-if="user" class="space-y-4">
      <div class="flex items-center gap-3 rounded-surface bg-gray-50 p-4 dark:bg-dark-700">
        <UserAvatar
          :avatar-url="user.avatar_url || ''"
          :user-id="user.id"
          :alt="user.email"
          size-class="h-10 w-10"
        />
        <div>
          <p class="font-medium text-gray-900 dark:text-white">{{ user.email }}</p>
          <p class="text-sm text-gray-500 dark:text-dark-400">{{ user.username }}</p>
        </div>
      </div>
      <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
      <ContentSkeleton v-if="loading" variant="list" :rows="3" class="py-4" />
      <div v-else-if="apiKeys.length === 0" class="py-8 text-center">
        <p class="text-sm text-gray-500">{{ t('admin.users.noApiKeys') }}</p>
      </div>
      <div v-else class="max-h-96 space-y-3 overflow-y-auto">
        <div
          v-for="key in apiKeys"
          :key="key.id"
          class="rounded-surface border border-gray-200 bg-white p-4 dark:border-dark-600 dark:bg-dark-800"
        >
          <div class="flex items-start justify-between">
            <div class="min-w-0 flex-1">
              <div class="mb-1 flex items-center gap-2">
                <span class="font-medium text-gray-900 dark:text-white">{{ key.name }}</span
                ><span
                  :class="[
                    'badge text-xs',
                    key.status === 'active' ? 'badge-success' : 'badge-danger',
                  ]"
                  >{{ key.status }}</span
                >
              </div>
              <!-- Keep twenty leading and eight trailing characters in the credential preview. -->
              <p class="truncate font-mono text-sm text-gray-500">
                {{ key.key.substring(0, 20) }}...{{ key.key.substring(key.key.length - 8) }}
              </p>
            </div>
          </div>
          <div class="mt-3 flex flex-wrap items-start gap-4 text-xs text-gray-500">
            <!-- Every Key shows its resolved channel order, including inherited administrator defaults. -->
            <div
              data-testid="composite-group-mappings"
              class="flex w-full min-w-0 items-start gap-1 sm:w-auto sm:min-w-80 sm:flex-1"
            >
              <span class="shrink-0 py-1">{{ t('admin.users.group') }}:</span>
              <div v-if="key.groups?.length || key.group" class="flex min-w-0 flex-wrap gap-1.5">
                <span
                  v-for="binding in key.routing_mode === 'single' && key.group_id
                    ? [{ group_id: key.group_id, group: key.group }]
                    : key.groups"
                  :key="`${key.id}-${binding.group_id}`"
                  class="inline-flex max-w-full min-w-0 items-center gap-1 rounded-compact border border-gray-200 bg-gray-50 px-1.5 py-1 dark:border-dark-600 dark:bg-dark-700"
                >
                  <span
                    class="max-w-24 truncate font-mono font-semibold text-primary-700 dark:text-primary-300"
                    >{{
                      binding.group?.display_name || binding.group?.name || `#${binding.group_id}`
                    }}</span
                  >
                </span>
              </div>
              <span v-else class="py-1 text-gray-400 italic">{{ t('admin.users.none') }}</span>
            </div>
            <div class="flex items-center gap-1">
              <span
                >{{ t('admin.users.columns.created') }}: {{ formatDateTime(key.created_at) }}</span
              >
            </div>
          </div>
        </div>
      </div>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { ref, watch, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import { formatDateTime } from '@/utils/format'
import type { AdminUser } from '@/api/admin/users.types'
import type { ApiKey } from '@/api/keys.types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import UserAvatar from '@/components/common/UserAvatar.vue'
import ContentSkeleton from '@/components/common/ContentSkeleton.vue'

// UserApiKeysModal.vue lists a selected user's credentials and ordered channels.
// Administrator defaults are resolved by the server before presentation. Each
// modal session owns its pending read, so switching users or closing the modal
// prevents a late response from displaying another account's credential data.
//
// Every load clears private rows before awaiting the network and publishes only
// while its revision remains current. Loading and error observations follow the
// same rule so an obsolete request cannot clear feedback for a later account.
// Closing or disposing the modal retires outstanding continuations; server-side
// administrative authorization determines which account records may be read.

// Parent identity and visibility determine which account's private credentials
// may be displayed. Closing clears the loaded rows before another user opens.
const props = defineProps<{
  show: boolean // Parent-controlled modal visibility.
  user: AdminUser | null // Selected administrative account view.
}>()
const emit = defineEmits<{ close: [] /* Parent dismissal request. */ }>()
const { t } = useI18n()

// Rows and loading feedback belong to the latest selected account. Revision
// changes retire pending responses when a modal closes or selects another user.
const apiKeys = ref<ApiKey[]>([])
const loading = ref(false)
const error = ref('')
let revision = 0

// load fetches private credential rows for the selected account and session.
// Current failures display an error and empty rows. Late completions cannot
// replace a later user's data, and disposal retires every outstanding read.
// Each new load clears private rows before awaiting the network so a changed
// account cannot display its predecessor's secrets during loading. Completion and
// cleanup inspect the same revision, preventing an older request from ending the
// loading feedback owned by a newer account selection.
async function load() {
  const current = ++revision
  const userID = props.user?.id
  apiKeys.value = []
  error.value = ''
  if (!props.show || !userID) {
    loading.value = false
    return
  }
  loading.value = true
  try {
    const response = await adminAPI.users.getUserApiKeys(userID)
    if (current === revision) apiKeys.value = response.items ?? []
  } catch {
    if (current === revision) error.value = t('keys.failedToLoad')
  } finally {
    if (current === revision) loading.value = false
  }
}

// handleClose clears private rows before requesting parent-controlled modal dismissal.
function handleClose() {
  ++revision
  apiKeys.value = []
  emit('close')
}

// Visibility and account identity invalidate the prior read together. Unmounting
// retires its continuation as well so it cannot publish after component disposal.
watch(
  () => [props.show, props.user?.id],
  () => void load(),
  { immediate: true },
)
onBeforeUnmount(() => {
  ++revision
})
</script>
