<template>
  <!-- Thumbnail frames leave wheel and pointer navigation with this gallery, and native touch scrolling remains owned by the browser. -->
  <div
    ref="gallery"
    role="region"
    :aria-label="t('intelligence.previewTitle')"
    tabindex="0"
    class="scrollbar-hide flex min-w-0 touch-auto select-none gap-4 overflow-x-auto overscroll-x-contain pb-3 focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500"
    :class="dragging ? 'cursor-grabbing' : 'cursor-grab'"
    data-testid="drawing-gallery"
    @wheel="onWheel"
    @pointerdown="startDrag"
    @pointermove="moveDrag"
    @pointerup="endDrag"
    @pointercancel="endDrag"
    @lostpointercapture="endDrag"
    @keydown="onKeydown"
    @dragstart.prevent
  >
    <article
      v-for="artifact in recentArtifacts(artifacts)"
      :key="artifact.id"
      class="relative w-[min(78vw,360px)] shrink-0 overflow-hidden rounded-surface border border-gray-200 bg-white dark:border-dark-600"
    >
      <div class="pointer-events-none h-56 overflow-hidden">
        <IntelligenceDrawing
          :run-id="artifact.id"
          :title="`${model} · ${formatDateTime(artifact.created_at)}`"
          thumbnail
        />
      </div>
      <span
        class="pointer-events-none absolute right-2 top-2 rounded-compact px-2 py-1 text-xs font-semibold text-white shadow-sm"
        :class="resultColor(artifact)"
        >{{ t(`intelligence.statuses.${resultStatus(artifact)}`) }}</span
      >
      <button
        type="button"
        class="flex w-full cursor-pointer items-center justify-between gap-3 border-t border-gray-100 px-3 py-3 text-left text-xs hover:bg-gray-50 dark:border-dark-700 dark:bg-dark-800 dark:hover:bg-dark-700"
        @click="emit('select', artifact)"
      >
        <span class="text-gray-500">{{ formatDateTime(artifact.created_at) }}</span
        ><span class="font-medium text-primary-600 dark:text-primary-400">{{
          t('intelligence.preview')
        }}</span>
      </button>
    </article>
  </div>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { IntelligenceRun } from '@/api/intelligence'
import { formatDateTime } from '@/utils/format'
import IntelligenceDrawing from './IntelligenceDrawing.vue'
import { recentArtifacts, resultColor, resultStatus } from './results'

// IntelligenceDrawingGallery.vue arranges the newest retained drawings in a
// horizontal gallery with wheel, mouse, keyboard and touch navigation.
// Thumbnail frames leave pointer and focus handling with the surrounding
// gallery, while separate artwork buttons open interactive detail. Scrolling
// is clamped to available content so edge input can continue moving the outer
// page.
//
// Mouse dragging records one captured pointer and its initial scroll position.
// A movement threshold distinguishes a drag from a click, and pointer release,
// cancellation, focus loss and unmount all clear capture. Interactive
// descendants keep their ordinary button behavior, and native touch scrolling
// remains browser-owned.

// Gallery focus, captured pointer identity and initial scroll offset determine
// horizontal navigation while artifact buttons emit their selected run to the
// page.
defineProps<{ artifacts: IntelligenceRun[]; model: string }>()
const emit = defineEmits<{ select: [run: IntelligenceRun] }>()
const { t } = useI18n()
const gallery = ref<HTMLElement>()
const dragging = ref(false)
let drag: { pointerId: number; startX: number; scrollLeft: number } | null = null

// setScrollLeft clamps gallery position to its available horizontal range and
// reports movement. Callers consume wheel and keyboard events only after
// movement, allowing edge input to continue scrolling the surrounding page.
function setScrollLeft(left: number) {
  const element = gallery.value
  if (!element) return false
  const before = element.scrollLeft
  element.scrollLeft = Math.max(0, Math.min(element.scrollWidth - element.clientWidth, left))
  return element.scrollLeft !== before
}

// onWheel converts the dominant wheel delta into bounded horizontal gallery
// movement. Line and page modes use approximate line height or viewport width,
// and zoom gestures retain their browser behavior.
function onWheel(event: WheelEvent) {
  const element = gallery.value
  if (!element || event.ctrlKey) return
  const delta = Math.abs(event.deltaX) > Math.abs(event.deltaY) ? event.deltaX : event.deltaY
  const unit = event.deltaMode === 1 ? 16 : event.deltaMode === 2 ? element.clientWidth : 1
  // Consume wheel input after actual horizontal movement so reaching a gallery
  // edge allows the surrounding page to continue scrolling.
  if (setScrollLeft(element.scrollLeft + delta * unit)) event.preventDefault()
}

// startDrag captures a primary mouse pointer when the gallery has horizontal
// overflow. Interactive controls retain their click behavior; a valid capture
// records pointer identity, starting position and initial scroll offset.
function startDrag(event: PointerEvent) {
  const element = gallery.value
  if (
    !element ||
    event.pointerType !== 'mouse' ||
    event.button !== 0 ||
    element.scrollWidth <= element.clientWidth
  )
    return
  // Keep artifact buttons and other interactive descendants outside mouse
  // capture so their click and keyboard activation can open drawing details.
  if (
    event.target instanceof Element &&
    event.target.closest('button, a, input, textarea, select, [contenteditable="true"]')
  )
    return
  drag = {
    pointerId: event.pointerId,
    startX: event.clientX,
    scrollLeft: element.scrollLeft,
  }
  element.setPointerCapture(event.pointerId)
}

// moveDrag updates scroll position for the captured pointer after a small
// movement threshold. Releasing the primary button clears drag state, and
// matching active movement prevents competing browser drag behavior.
function moveDrag(event: PointerEvent) {
  if (!drag || event.pointerId !== drag.pointerId) return
  if (!(event.buttons & 1)) {
    endDrag()
    return
  }
  const distance = event.clientX - drag.startX
  if (!dragging.value && Math.abs(distance) <= 5) return
  dragging.value = true
  setScrollLeft(drag.scrollLeft - distance)
  event.preventDefault()
}

// endDrag clears the captured drag and releases pointer capture when held.
// Events from other pointers leave it intact, while focus loss and teardown
// can call without a pointer to finish cleanup.
function endDrag(event?: PointerEvent | FocusEvent) {
  if (event && 'pointerId' in event && drag && event.pointerId !== drag.pointerId) return
  const pointerId = drag?.pointerId
  drag = null
  dragging.value = false
  const element = gallery.value
  if (element && pointerId !== undefined && element.hasPointerCapture(pointerId))
    element.releasePointerCapture(pointerId)
}

// onKeydown scrolls the focused gallery using arrows, page keys, Home or End.
// Modified shortcuts and descendant controls retain their behavior, and
// handled keys are consumed only when scrolling moves.
function onKeydown(event: KeyboardEvent) {
  const element = gallery.value
  if (!element || event.target !== element || event.ctrlKey || event.metaKey || event.altKey) return
  const offsets: Record<string, number> = {
    ArrowLeft: element.scrollLeft - 320,
    ArrowRight: element.scrollLeft + 320,
    PageUp: element.scrollLeft - element.clientWidth,
    PageDown: element.scrollLeft + element.clientWidth,
    Home: 0,
    End: element.scrollWidth,
  }
  const offset = offsets[event.key]
  if (typeof offset === 'number' && setScrollLeft(offset)) event.preventDefault()
}

// Clear capture on focus loss and teardown as well as pointer completion,
// preventing a drag from surviving its interaction context.
onMounted(() => window.addEventListener('blur', endDrag))
onBeforeUnmount(() => {
  window.removeEventListener('blur', endDrag)
  endDrag()
})
</script>
