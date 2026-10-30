<template>
  <AppLayout>
    <div class="min-w-0 space-y-4">
      <div class="flex flex-wrap items-end justify-between gap-2">
        <div>
          <h1 class="page-title">{{ t('intelligence.title') }}</h1>
          <p class="page-description">{{ t('intelligence.description') }}</p>
        </div>
        <button type="button" class="btn btn-secondary btn-sm" :disabled="loading" @click="load">
          {{ t('common.refresh') }}
        </button>
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
        v-if="!loading && !error && !groups.length"
        class="card p-8 text-center text-sm text-gray-500"
      >
        {{ t('intelligence.emptyHint') }}
      </div>
      <template v-if="groups.length">
        <div class="flex flex-wrap items-center gap-4 text-xs text-gray-500 dark:text-dark-400">
          <span class="inline-flex items-center gap-1">
            <span class="h-2 w-2 rounded-full bg-emerald-500" aria-hidden="true" />
            {{ t('intelligence.statuses.passed') }}
          </span>
          <span class="inline-flex items-center gap-1">
            <span class="h-2 w-2 rounded-full bg-rose-500" aria-hidden="true" />
            {{ t('intelligence.statuses.incorrect') }}
          </span>
          <span class="ml-auto">{{ t('intelligence.historyTimeHint') }}</span>
        </div>
        <section
          v-for="group in groups"
          :key="group.id"
          :data-group-id="group.id"
          class="card overflow-hidden"
        >
          <header
            class="flex flex-wrap items-center gap-3 border-b border-gray-100 px-4 py-3 dark:border-dark-700"
          >
            <h2 class="min-w-0 break-words text-sm font-semibold">{{ group.name }}</h2>
            <span
              class="badge badge-gray inline-flex shrink-0 items-baseline gap-2"
              data-testid="group-rate"
            >
              <span class="text-gray-500 dark:text-dark-400">{{
                t('intelligence.rateMultiplier')
              }}</span>
              <span class="font-semibold tabular-nums">{{ group.rate }}x</span>
            </span>
          </header>
          <div class="divide-y divide-gray-100 dark:divide-dark-700">
            <section
              v-for="model in group.models"
              :key="model.key"
              :data-model="model.model"
              class="grid min-w-0 lg:grid-cols-[10rem_minmax(0,1fr)]"
            >
              <div
                class="flex min-w-0 items-center gap-3 border-b border-gray-100 px-4 py-3 dark:border-dark-700 lg:flex-col lg:items-start lg:justify-center lg:gap-2 lg:border-b-0 lg:border-r lg:py-4"
              >
                <ModelIcon :model="model.model" size="24px" />
                <h3
                  class="min-w-0 break-all text-sm font-medium text-gray-900 dark:text-dark-100"
                  >{{ model.model }}</h3
                >
                <span v-if="model.benchmark !== 'drawing'" class="text-xs text-gray-400">{{
                  t('intelligence.candy')
                }}</span>
              </div>
              <div class="min-w-0 divide-y divide-gray-100 dark:divide-dark-700">
                <article
                  v-for="row in model.rows"
                  :key="row.test.source || 'site'"
                  :data-test-id="row.test.id"
                  :data-source="row.test.source || 'site'"
                  class="min-w-0 px-4 py-4 transition-colors hover:bg-gray-50 dark:hover:bg-dark-800"
                >
                  <div
                    class="grid min-w-0 grid-cols-2 items-center gap-4 xl:grid-cols-[10rem_7rem_9rem_minmax(8rem,1fr)_7rem]"
                  >
                    <div class="col-span-2 flex min-w-0 items-center gap-3 xl:col-span-1">
                      <div
                        class="aspect-[4/3] w-16 shrink-0 overflow-hidden rounded-control border border-gray-100 bg-gray-50 dark:border-dark-700 dark:bg-dark-950"
                        data-testid="latest-drawing"
                      >
                        <IntelligenceDrawing
                          v-if="row.artwork"
                          :run-id="row.artwork.id"
                          :title="model.model"
                          compact
                        />
                        <span
                          v-else
                          class="flex h-full items-center justify-center text-gray-300 dark:text-dark-600"
                        >
                          <Icon name="modalityImage" size="sm" :animate-on-hover="false" />
                        </span>
                      </div>
                      <div class="flex items-center gap-2 text-xs text-gray-500 dark:text-dark-400">
                        <Icon
                          :name="row.test.source === 'user' ? 'user' : 'shieldCheck'"
                          size="xs"
                          :animate-on-hover="false"
                        />
                        <span class="whitespace-nowrap">{{
                          t(
                            row.test.source === 'user'
                              ? 'intelligence.userTests'
                              : 'intelligence.siteTests',
                          )
                        }}</span>
                      </div>
                    </div>
                    <div>
                      <p class="mb-2 text-xs text-gray-500 dark:text-dark-400">
                        {{ t('intelligence.latestValidResult') }}
                      </p>
                      <IntelligenceStatusBadge
                        v-if="row.latest"
                        :status="resultStatus(row.latest)"
                      />
                      <span v-else class="text-xs text-gray-400 dark:text-dark-500">{{
                        t('intelligence.noValidResult')
                      }}</span>
                      <p
                        v-if="row.active"
                        data-testid="current-task"
                        class="mt-2 flex items-center gap-1 text-xs text-gray-500 dark:text-dark-400"
                      >
                        <Icon
                          name="refresh"
                          size="xs"
                          class="animate-spin text-primary-500 motion-reduce:animate-none"
                          :animate-on-hover="false"
                        />
                        <span role="status">{{ taskPhase(row.active) }}</span>
                      </p>
                    </div>
                    <div v-if="row.latest" data-testid="result-time">
                      <span class="sr-only">
                        {{
                          t(
                            row.latest.finished_at
                              ? 'intelligence.lastCompleted'
                              : 'intelligence.resultUpdated',
                          )
                        }}
                      </span>
                      <time
                        :datetime="observationTime(row.latest)"
                        :title="`${t(row.latest.finished_at ? 'intelligence.lastCompleted' : 'intelligence.resultUpdated')}: ${formatDateTime(observationTime(row.latest))}`"
                        class="block whitespace-nowrap text-sm font-medium tabular-nums text-gray-900 dark:text-dark-100"
                        >{{ completionTime(observationTime(row.latest)) }}</time
                      >
                      <span
                        class="mt-1 block text-xs text-gray-500 dark:text-dark-400"
                        data-testid="result-age"
                        >{{ relativeTime(observationTime(row.latest)) }}</span
                      >
                    </div>
                    <div v-else class="text-sm text-gray-400 dark:text-dark-500">—</div>
                    <div class="col-span-2 min-w-0 xl:col-span-1">
                      <template v-if="row.history.length">
                        <div
                          class="mb-2 flex flex-wrap items-center justify-between gap-2 text-xs text-gray-500 dark:text-dark-400"
                        >
                          <span>{{
                            t('intelligence.validResults', { count: row.history.length })
                          }}</span>
                          <span data-testid="result-counts">{{
                            t('intelligence.resultCounts', {
                              passed: row.passedCount,
                              failed: row.failedCount,
                            })
                          }}</span>
                        </div>
                        <IntelligenceResultBar :runs="row.history" compact />
                        <div
                          class="mt-1 flex justify-between text-xs text-gray-400 dark:text-dark-400"
                        >
                          <span>{{ t('intelligence.olderResults') }}</span>
                          <span>{{ t('intelligence.newestResult') }}</span>
                        </div>
                      </template>
                      <span v-else class="text-xs text-gray-400 dark:text-dark-500">{{
                        t('intelligence.noRuns')
                      }}</span>
                    </div>
                    <div class="col-span-2 justify-self-end xl:col-span-1">
                      <button
                        v-if="row.test.source === 'user' && model.site"
                        type="button"
                        class="btn btn-secondary btn-sm gap-1 whitespace-nowrap px-2"
                        :aria-label="t('intelligence.selfTestForModel', { model: model.model })"
                        @click="selfTestTarget = model.site"
                      >
                        <Icon name="play" size="xs" />
                        {{ t('intelligence.selfTest') }}
                      </button>
                    </div>
                  </div>
                </article>
              </div>
            </section>
          </div>
        </section>
        <p class="text-xs text-gray-400">{{ t('intelligence.historyScreenshotHint') }}</p>
      </template>
      <IntelligenceSelfTestDialog
        :show="!!selfTestTarget"
        :test="selfTestTarget"
        @close="selfTestTarget = null"
        @submitted="submitted"
      />
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useNow } from '@vueuse/core'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import ModelIcon from '@/components/common/ModelIcon.vue'
import IntelligenceResultBar from '@/components/intelligence/IntelligenceResultBar.vue'
import IntelligenceDrawing from '@/components/intelligence/IntelligenceDrawing.vue'
import IntelligenceSelfTestDialog from '@/components/intelligence/IntelligenceSelfTestDialog.vue'
import IntelligenceStatusBadge from '@/components/intelligence/IntelligenceStatusBadge.vue'
import { observationTime, resultStatus, summarizeTestRuns } from '@/components/intelligence/results'
import { intelligenceAPI, type IntelligenceRun, type IntelligenceTest } from '@/api/intelligence'
import { extractApiErrorMessage } from '@/utils/apiError'
import { provideIntelligenceResults } from '@/components/intelligence/useIntelligenceResults'
import { formatDateTime } from '@/utils/format'

// IntelligenceView.vue displays authorized model histories in routing group cards.
// Each group uses shared card and badge styles for its name and billing
// multiplier. History colors distinguish test outcomes, and the row action uses
// the same secondary button treatment as other console lists.
// Model identity occupies a shared column beside adjacent site and user rows,
// whose latest assessed artwork, status, completion time, history, and actions
// align for comparison. Active task stages sit beside the previous assessed result,
// leaving row boundaries uniform during generation and retries. Completion labels
// show a compact date and minute, with full timestamps available on hover.
// Drawing models reserve an empty user row until the first contribution, so the
// self-test entry stays beside its site's observations.
//
// The editor receives the selected site configuration, tying its personal Key
// candidates and submitted configuration identifier to the model the user chose.
// Page refresh cancels older reads before publishing results, and shared preview
// resources remain owned by the result provider until page disposal. Scheduled
// reads keep queued and completed observations current while the page is visible.

// Page observations, request feedback, and the selected self-test target belong
// to this mounted page; the result provider owns transient preview resources.
const { t, locale } = useI18n()
// useNow advances minute-based completion ages and releases its timer with this
// component; network refresh remains on the independent ten-second polling cycle.
const now = useNow({ interval: 60000 })
provideIntelligenceResults()
const tests = ref<IntelligenceTest[]>([])
const loading = ref(false)
const error = ref('')
const selfTestTarget = ref<IntelligenceTest | null>(null)
const submissionMessage = ref('')
let listController: AbortController | null = null
let refreshTimer: ReturnType<typeof setInterval> | undefined

// ModelHistory holds one benchmark's ordered source rows and its self-test target.
interface ModelHistory {
  key: string // model and benchmark identity
  model: string // requested model identifier
  benchmark: IntelligenceTest['benchmark'] // selected benchmark type
  // site supplies the matching drawing's execution settings. User-only histories
  // and candy tests leave it absent, so their rows cannot open the self-test editor.
  site?: IntelligenceTest // available site drawing configuration
  tests: IntelligenceTest[] // observations for each source
}

// GroupHistory collects model observations under one group name and billing rate.
interface GroupHistory {
  id: number // routing group identifier
  name: string // current group name
  rate: number // group billing multiplier
  models: Map<string, ModelHistory> // benchmark histories by model
}

// groups organizes observations by group, then by model and benchmark. Site
// drawings receive an empty user row when shared history has no contribution;
// persisted user rows replace that placeholder while the site's identifier remains
// the submission target. Sorting keeps models stable across periodic reads and
// places each site observation immediately before its matching user history.
// Candy and drawing benchmarks retain separate histories even when their model
// identifiers match, because their results measure different benchmark tasks.
const groups = computed(() => {
  const grouped = new Map<number, GroupHistory>()
  for (const test of tests.value) {
    let group = grouped.get(test.group_id)
    if (!group) {
      group = {
        id: test.group_id,
        name: test.group_name,
        rate: test.rate_multiplier,
        models: new Map(),
      }
      grouped.set(test.group_id, group)
    }
    const key = `${test.model}:${test.benchmark}`
    let model = group.models.get(key)
    if (!model) {
      model = { key, model: test.model, benchmark: test.benchmark, tests: [] }
      group.models.set(key, model)
    }
    model.tests.push(test)
    if (test.source !== 'user' && test.benchmark === 'drawing') model.site = test
  }
  return [...grouped.values()]
    .sort((a, b) => a.id - b.id)
    .map((group) => ({
      ...group,
      models: [...group.models.values()]
        .sort((a, b) => a.key.localeCompare(b.key))
        .map((model) => {
          const entries = [...model.tests]
          if (model.site && !entries.some((test) => test.source === 'user')) {
            entries.push({
              ...model.site,
              id: -model.site.id,
              source: 'user',
              runs: [],
              artifacts: [],
            })
          }
          return {
            ...model,
            rows: entries
              .sort((a, b) => Number(a.source === 'user') - Number(b.source === 'user'))
              .map((test) => ({ test, ...summarizeTestRuns(test) })),
          }
        }),
    }))
})

// completionTime formats a compact local finish timestamp through minutes.
// Today's observations use the translated today label; older dates include their
// calendar date, and prior years retain the year to distinguish old observations.
function completionTime(value: string): string {
  const date = new Date(value)
  const clock: Intl.DateTimeFormatOptions = { hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }
  if (date.toDateString() === now.value.toDateString()) {
    return t('intelligence.todayAt', { time: formatDateTime(value, clock) })
  }
  return formatDateTime(value, {
    ...clock,
    year: date.getFullYear() === now.value.getFullYear() ? undefined : 'numeric',
    month: '2-digit',
    day: '2-digit',
  })
}

// relativeTime describes how long ago a result completed using whole elapsed
// minutes, hours, or days. Future timestamps clamp to the present to tolerate
// clock skew, while invalid observations retain the unknown marker.
function relativeTime(value: string): string {
  // Whole seconds avoid displaying partial units as a completed minute or hour.
  const seconds = Math.max(0, Math.floor((now.value.getTime() - Date.parse(value)) / 1000))
  if (!Number.isFinite(seconds)) return '—'
  if (seconds < 60) return t('common.time.justNow')
  // Choose the largest completed unit, matching the console's relative-time labels.
  const [unit, divisor]: [Intl.RelativeTimeFormatUnit, number] =
    seconds >= 86400 ? ['day', 86400] : seconds >= 3600 ? ['hour', 3600] : ['minute', 60]
  return new Intl.RelativeTimeFormat(locale.value, { numeric: 'always' }).format(
    -Math.floor(seconds / divisor),
    unit,
  )
}

// taskPhase translates the observable execution stage of an active task.
// Retry phase takes precedence over queued status because the same submitted
// task can return to the queue after a transient generation failure.
// Saved-artwork phases indicate evaluation even before the next worker claims
// the task. A generating phase identifies model output work, while remote or
// unrecognized stages retain the generic running label until an observable
// stage arrives in a later list response.
function taskPhase(run: IntelligenceRun): string {
  if (run.phase === 'retrying') return t('intelligence.phases.retrying')
  if (['generated', 'assessing', 'classifying'].includes(run.phase || ''))
    return t('intelligence.phases.assessing')
  if (run.phase === 'generating') return t('intelligence.phases.generating')
  if (run.status === 'queued') return t('intelligence.statuses.queued')
  return t('intelligence.statuses.running')
}

// submitted refreshes shared history after the user's paid drawing is queued.
// The pending observation appears in its public user row while background reads
// continue following generation and evaluation until the run reaches a terminal state.
function submitted() {
  submissionMessage.value = t('intelligence.submitted')
  void load()
}

// load refreshes authorized histories and their current group multipliers.
// It cancels the previous request and ignores aborted completions so an older
// response cannot overwrite a later refresh or restore results after access changes.
async function load() {
  // Polling and manual refresh share one owner, so abort the previous read before
  // assigning a new controller and allowing its results to update the visible groups.
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
      // A failed authorized read clears previously visible groups because their
      // permissions may have changed since the last successful list response.
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
  // Disposal stops future polling and aborts the last list request so an abandoned
  // page cannot publish results after its preview resources have been released.
  listController?.abort()
  if (refreshTimer) clearInterval(refreshTimer)
})
</script>
