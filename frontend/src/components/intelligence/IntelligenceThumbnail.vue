<template>
  <div
    class="flex aspect-[4/3] w-full items-center justify-center overflow-hidden rounded-control bg-gray-50 dark:bg-dark-900"
  >
    <img v-if="url" :src="url" :alt="title" class="h-full w-full object-contain" />
    <p v-else class="px-3 text-center text-xs text-gray-500" :role="failed ? undefined : 'status'">
      {{ t(failed ? 'intelligence.noScreenshot' : 'common.loading') }}
    </p>
  </div>
</template>

<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useIntelligenceResults } from './useIntelligenceResults'

// IntelligenceThumbnail.vue displays a saved PNG in a history popover. It asks
// the page-owned loader for an authorized image, while a selection revision
// prevents late requests from replacing another run's screenshot. Unmounting
// invalidates pending display updates; the loader owns cached image URL cleanup.
//
// History selection can move before a server finishes capturing a missing image.
// Each selection therefore clears the visible image and owns a new revision;
// an earlier completion can populate the shared cache but cannot update this
// popover. Keeping network and object-URL ownership at the page also lets a later
// hover reuse a completed capture after the first popover has been removed.

// The selected observation and accessible image label come from the history
// node. Display state belongs to this mounted popover and resets on selection.
const props = defineProps<{ runId: string; title: string }>()
const { t } = useI18n()
const loader = useIntelligenceResults()
const url = ref('')
const failed = ref(false)
let revision = 0

// load reads the selected observation's persistent screenshot through the page
// cache. Each invocation owns an increasing revision and clears its preceding
// image before awaiting a result. A completion updates display state only when
// its revision still matches, including after the component has unmounted.
//
// Errors show screenshot unavailability for the current selection. The page owns
// pending HTTP reads and cached object URLs, allowing completed captures to be
// reused even when their initiating popover has already closed.
async function load() {
  const current = ++revision
  url.value = ''
  failed.value = false
  try {
    const image = await loader.thumbnail(props.runId)
    if (current === revision) url.value = image
  } catch {
    // Keep a failed image local to this popover so another history selection
    // remains usable and the latest document continues running in its isolated frame.
    if (current === revision) failed.value = true
  }
}
watch(() => props.runId, load, { immediate: true })
onBeforeUnmount(() => {
  ++revision
})
</script>
