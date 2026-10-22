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
      :scrolling="compact ? 'no' : undefined"
      class="h-full w-full border-0 bg-white"
      :style="compact ? compactStyle : undefined"
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
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { intelligencePreviewURL } from './results'
import { useIntelligenceResults } from './useIntelligenceResults'

// IntelligenceDrawing.vue loads signed drawing previews when a card approaches
// the visible area. Hidden pages and offscreen cards remove iframe content,
// while a revision rejects responses from preceding selections. The frame
// grants scripts inside its isolated sandbox and uses the server's dedicated
// content policy.
//
// Latest previews use a desktop viewport scaled into their compact card so a
// fixed-size drawing remains fully visible and animated. Detail frames use
// their available viewport. The page-owned loader limits concurrent reads and
// caches ticket responses; component teardown disconnects visibility
// observation and prevents its pending responses from updating removed
// content.

// Frame visibility, the signed URL and measured scale belong to this mounted
// drawing; observers and global listeners are released together during teardown.
const props = defineProps<{
  runId: string
  title: string
  // compact scales a desktop viewport into its card, keeping fixed-size
  // documents fully visible while their scripts and pointer controls remain active.
  compact?: boolean
}>()
const { t } = useI18n()
const loader = useIntelligenceResults()
const container = ref<HTMLElement>()
const visible = ref(false)
const pageVisible = ref(!document.hidden)
const url = ref('')
const error = ref('')
const previewScale = ref(1)
// The full drawing viewport matches saved screenshots so compact live cards
// show the same scene while retaining their independent HTML animation state.
const compactStyle = computed(() => ({
  width: '1280px',
  height: '960px',
  flexShrink: 0,
  transform: `scale(${previewScale.value})`,
}))
let observer: IntersectionObserver | undefined
let sizeObserver: ResizeObserver | undefined
let revision = 0
let disposed = false

// resizePreview fits the complete desktop drawing into its latest-result card.
// Both dimensions constrain the scale, so a fixed-size model document remains
// visible when responsive layout or sidebar changes alter the available space.
function resizePreview() {
  const element = container.value
  if (!element || !props.compact) return
  // Use the screenshot's viewport as the scaling basis for live compact cards.
  const scale = Math.min(element.clientWidth / 1280, element.clientHeight / 960)
  if (scale > 0) previewScale.value = scale
}
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
  if (props.compact) {
    resizePreview()
    window.addEventListener('resize', resizePreview)
    if (typeof ResizeObserver !== 'undefined') {
      sizeObserver = new ResizeObserver(resizePreview)
      if (container.value) sizeObserver.observe(container.value)
    }
  }
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
  // Release card measurement with visibility tracking so removed latest results
  // cannot retain resizing callbacks after their page or model group disappears.
  sizeObserver?.disconnect()
  window.removeEventListener('resize', resizePreview)
  document.removeEventListener('visibilitychange', visibilityChanged)
})
</script>
