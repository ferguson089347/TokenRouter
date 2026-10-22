<template>
  <div
    ref="container"
    class="flex h-full w-full items-center justify-center bg-gray-50 dark:bg-dark-900"
  >
    <!-- The dedicated response supplies a drawing-specific content policy so isolated scripts can run while the parent panel retains its nonce policy. -->
    <iframe
      v-if="visible && pageVisible && url"
      :src="url"
      sandbox="allow-scripts"
      referrerpolicy="no-referrer"
      :title="title"
      :scrolling="thumbnail ? 'no' : undefined"
      :tabindex="thumbnail ? -1 : undefined"
      class="h-full w-full border-0 bg-white"
      :class="{ 'pointer-events-none': thumbnail }"
      allow="
        camera 'none';
        microphone 'none';
        geolocation 'none';
        clipboard-read 'none';
        clipboard-write 'none';
      "
    />
    <p v-else-if="error" class="px-4 text-center text-xs text-gray-500">
      {{ error }}
    </p>
    <p v-else role="status" class="text-xs text-gray-400">
      {{ t('common.loading') }}
    </p>
  </div>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { intelligencePreviewURL } from './results'
import { useIntelligenceResults } from './useIntelligenceResults'

// IntelligenceDrawing.vue loads signed drawing previews when a card approaches
// the visible area. Hidden pages and offscreen cards remove iframe content,
// while a revision rejects responses from preceding selections. The frame
// grants scripts inside its isolated sandbox and uses the server's dedicated
// content policy.
//
// Thumbnail frames suppress scrolling, pointer interaction and keyboard focus
// so the surrounding gallery owns navigation. Detail frames retain local
// drawing interaction. The page-owned loader limits concurrent reads and
// caches ticket responses; component teardown disconnects visibility
// observation and prevents its pending responses from updating removed
// content.
// Thumbnail frames leave scrolling, pointer input and keyboard focus with the
// gallery; detail previews allow interaction inside the isolated document.
// Visibility, preview URL and selection revision belong to this drawing card;
// its observer and listeners are released when the card unmounts.
const props = defineProps<{
  runId: string
  title: string
  thumbnail?: boolean
}>()
const { t } = useI18n()
const loader = useIntelligenceResults()
const container = ref<HTMLElement>()
const visible = ref(false)
const pageVisible = ref(!document.hidden)
const url = ref('')
const error = ref('')
let observer: IntersectionObserver | undefined
let revision = 0
let disposed = false
// load requests a signed preview when the card and document are visible. A
// monotonically increasing revision and disposal flag reject older
// completions, while invalid URLs and failed requests show the unavailable-
// artwork message.
async function load() {
  if (!visible.value || document.hidden) return
  const current = ++revision
  error.value = ''
  try {
    const result = await loader.preview(props.runId)
    if (current !== revision || disposed) return
    url.value = intelligencePreviewURL(result.url)
    if (!url.value) error.value = t('intelligence.noArtwork')
  } catch {
    if (current === revision && !disposed) error.value = t('intelligence.noArtwork')
  }
}
// visibilityChanged removes hidden-page iframe content and reloads visible
// drawing cards when the document becomes active. Ticket acquisition stays
// subject to the shared page loader's concurrency and expiration rules.
function visibilityChanged() {
  pageVisible.value = !document.hidden
  if (!pageVisible.value) url.value = ''
  else if (visible.value) void load()
}
watch(
  () => props.runId,
  () => {
    url.value = ''
    void load()
  },
)
onMounted(() => {
  document.addEventListener('visibilitychange', visibilityChanged)
  if (typeof IntersectionObserver === 'undefined') {
    visible.value = true
    void load()
    return
  }
  observer = new IntersectionObserver(
    (entries) => {
      visible.value = entries.some((entry) => entry.isIntersecting)
      if (visible.value) void load()
      else url.value = ''
    },
    { rootMargin: '80px' },
  )
  if (container.value) observer.observe(container.value)
})
onBeforeUnmount(() => {
  disposed = true
  ++revision
  observer?.disconnect()
  document.removeEventListener('visibilitychange', visibilityChanged)
})
</script>
