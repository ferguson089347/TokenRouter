<template>
  <AppLayout>
    <div class="min-w-0 space-y-4">
      <div class="flex flex-wrap items-end justify-between gap-2">
        <div>
          <h1 class="page-title">{{ t('intelligence.title') }}</h1>
          <p class="page-description">{{ t('intelligence.description') }}</p>
        </div>
        <div class="flex gap-2">
          <button
            type="button"
            class="btn btn-secondary btn-sm"
            :disabled="loading"
            @click="load"
            >{{ t('common.refresh') }}</button
          >
          <button type="button" class="btn btn-primary btn-sm" @click="showSelfTest = true">{{
            t('intelligence.selfTest')
          }}</button>
        </div>
      </div>
      <p v-if="submissionMessage" role="status" class="text-sm text-emerald-600">{{
        submissionMessage
      }}</p>
      <div v-if="loading && !tests.length" role="status" class="py-16 text-center text-gray-500">
        {{ t('common.loading') }}
      </div>
      <p
        v-if="error"
        role="alert"
        class="rounded-control bg-red-50 p-4 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300"
      >
        {{ error }}
      </p>
      <div
        v-if="!loading && !error && !rows.length"
        class="card p-8 text-center text-sm text-gray-500"
      >
        {{ t('intelligence.emptyHint') }}
      </div>
      <div v-else-if="rows.length" class="card overflow-hidden">
        <IntelligenceStatusLegend class="border-b border-gray-100 px-4 py-3 dark:border-dark-700" />
        <div
          class="hidden grid-cols-[5rem_minmax(10rem,1fr)_8rem_minmax(12rem,2fr)] items-center gap-4 border-b border-gray-100 bg-gray-50/50 px-4 py-3 text-xs text-gray-500 dark:border-dark-700 dark:bg-dark-900 lg:grid"
        >
          <span>{{ t('intelligence.latestArtwork') }}</span>
          <span>{{ t('intelligence.channelModel') }}</span>
          <span>{{ t('intelligence.resultAndFirstToken') }}</span>
          <span>{{ t('intelligence.history') }}</span>
        </div>
        <div class="divide-y divide-gray-100 dark:divide-dark-700">
          <article
            v-for="row in rows"
            :key="row.key"
            :data-test-id="row.test.id"
            :data-group-id="row.test.group_id"
            class="grid min-w-0 grid-cols-[4rem_minmax(0,1fr)] items-center gap-x-3 gap-y-2 px-4 py-3 lg:grid-cols-[5rem_minmax(10rem,1fr)_8rem_minmax(12rem,2fr)] lg:gap-4"
          >
            <div
              class="aspect-[4/3] w-full overflow-hidden rounded-control bg-gray-50 dark:bg-dark-900"
              data-testid="latest-drawing"
            >
              <IntelligenceDrawing
                v-if="row.artwork"
                :run-id="row.artwork.id"
                :title="row.test.model"
                compact
              />
              <span v-else class="flex h-full items-center justify-center text-xs text-gray-400"
                >—</span
              >
            </div>
            <div class="min-w-0">
              <div class="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
                <h2 class="truncate text-sm font-semibold" :title="row.test.group_name">{{
                  row.test.group_name
                }}</h2>
                <span
                  class="rounded-compact px-1.5 py-0.5 text-xs"
                  :class="
                    row.test.source === 'user'
                      ? 'bg-primary-50 text-primary-700 dark:bg-primary-900/30 dark:text-primary-300'
                      : 'bg-gray-100 text-gray-500 dark:bg-dark-700'
                  "
                  >{{
                    t(
                      row.test.source === 'user'
                        ? 'intelligence.userTests'
                        : 'intelligence.siteTests',
                    )
                  }}</span
                >
              </div>
              <h3 class="mt-1 truncate text-xs text-gray-500" :title="row.test.model">{{
                row.test.model
              }}</h3>
            </div>
            <div
              class="col-start-2 flex flex-wrap items-center gap-2 text-xs lg:col-start-auto lg:block"
            >
              <IntelligenceStatusBadge v-if="row.latest" :status="resultStatus(row.latest)" />
              <span v-else class="text-gray-400">{{ t('intelligence.noRuns') }}</span>
              <span
                class="text-gray-500 tabular-nums lg:mt-1 lg:block"
                data-testid="row-first-token"
              >
                {{ t('intelligence.firstToken') }}
                {{
                  typeof row.latest?.first_token_ms === 'number'
                    ? `${row.latest.first_token_ms} ms`
                    : '—'
                }}
              </span>
            </div>
            <div class="col-span-2 min-w-0 lg:col-span-1">
              <IntelligenceResultBar :runs="row.test.runs || []" compact />
            </div>
          </article>
        </div>
      </div>
      <p v-if="rows.length" class="text-xs text-gray-400">{{
        t('intelligence.historyScreenshotHint')
      }}</p>
      <IntelligenceSelfTestDialog
        :show="showSelfTest"
        :tests="tests"
        @close="showSelfTest = false"
        @submitted="submitted"
      />
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import IntelligenceResultBar from '@/components/intelligence/IntelligenceResultBar.vue'
import IntelligenceDrawing from '@/components/intelligence/IntelligenceDrawing.vue'
import IntelligenceSelfTestDialog from '@/components/intelligence/IntelligenceSelfTestDialog.vue'
import IntelligenceStatusBadge from '@/components/intelligence/IntelligenceStatusBadge.vue'
import IntelligenceStatusLegend from '@/components/intelligence/IntelligenceStatusLegend.vue'
import { recentArtifacts, recentRuns, resultStatus } from '@/components/intelligence/results'
import { intelligenceAPI, type IntelligenceTest } from '@/api/intelligence'
import { extractApiErrorMessage } from '@/utils/apiError'
import { provideIntelligenceResults } from '@/components/intelligence/useIntelligenceResults'

// IntelligenceView.vue displays authorized model quality histories grouped by
// routing group, model, and test source in compact rows. Each row combines a
// small latest animation, first-token measurement, labeled verdict badge, and shared
// history popover. User submissions enter a public user-test row beside site checks.
// The dialog uses personal Key identifiers, and queued results refresh in place.
//
// Each loader cancels its predecessor so polling and manual refresh cannot
// publish observations from an older request after current permissions change.
// Drawing rows reserve an empty user history before the first contribution,
// then use the server's persisted source identity when a submission arrives.
// Preview requests remain owned by the shared result provider until page disposal.

// Authorized histories share the current page loader, while periodic reads
// refresh visible rows. History nodes own their transient preview.
const { t } = useI18n()
provideIntelligenceResults()
const tests = ref<IntelligenceTest[]>([])
const loading = ref(false)
const error = ref('')
const showSelfTest = ref(false)
const submissionMessage = ref('')
const rows = computed(() => {
  const entries = [...tests.value]
  for (const test of tests.value) {
    if (test.benchmark !== 'drawing' || test.source === 'user') continue
    if (
      !entries.some(
        (candidate) =>
          candidate.source === 'user' &&
          candidate.group_id === test.group_id &&
          candidate.model === test.model,
      )
    ) {
      entries.push({ ...test, id: -test.id, source: 'user', runs: [], artifacts: [] })
    }
  }
  return entries
    .sort(
      (a, b) =>
        a.group_id - b.group_id ||
        a.model.localeCompare(b.model) ||
        (a.source === 'user' ? 1 : 0) - (b.source === 'user' ? 1 : 0),
    )
    .map((test) => ({
      key: `${test.group_id}:${test.model}:${test.benchmark}:${test.source || 'site'}`,
      test,
      latest: recentRuns(test.runs || []).slice(-1)[0],
      artwork: recentArtifacts(test.artifacts || [])[0],
    }))
})

// submitted refreshes shared history after the user's paid drawing is queued.
// The pending observation appears in its public user row while background reads
// continue following generation and evaluation until the run reaches a terminal state.
function submitted() {
  submissionMessage.value = t('intelligence.submitted')
  void load()
}
let listController: AbortController | null = null
let refreshTimer: ReturnType<typeof setInterval> | undefined

// load refreshes the visible configuration or result list for this page. It
// cancels the previous request and ignores aborted completions so an older
// response cannot overwrite a later refresh.
async function load() {
  listController?.abort()
  const controller = new AbortController()
  listController = controller
  loading.value = true
  error.value = ''
  try {
    const result = await intelligenceAPI.list(controller.signal)
    if (!controller.signal.aborted) tests.value = result
  } catch (err) {
    if (!controller.signal.aborted) {
      tests.value = []
      error.value = extractApiErrorMessage(err, t('intelligence.loadFailed'))
    }
  } finally {
    if (!controller.signal.aborted) loading.value = false
  }
}

onMounted(() => {
  void load()
  // Pause background list refresh while hidden so preview images and visible
  // observations do not trigger unnecessary authenticated reads in a background tab.
  refreshTimer = setInterval(() => {
    if (!document.hidden && !loading.value) void load()
  }, 10000)
})
onBeforeUnmount(() => {
  listController?.abort()
  if (refreshTimer) clearInterval(refreshTimer)
})
</script>
