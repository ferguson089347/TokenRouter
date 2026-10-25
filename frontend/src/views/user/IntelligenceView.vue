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
        <IntelligenceStatusLegend />
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
                  class="grid min-w-0 grid-cols-[4rem_minmax(0,1fr)_auto] items-center gap-x-3 gap-y-2 px-4 py-3 transition-colors hover:bg-gray-50 dark:hover:bg-dark-800 lg:grid-cols-[4rem_5rem_8rem_minmax(6rem,1fr)_5rem]"
                >
                  <div
                    class="row-span-2 aspect-[4/3] w-full overflow-hidden rounded-control border border-gray-100 bg-gray-50 dark:border-dark-700 dark:bg-dark-950 lg:row-span-1"
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
                  <div
                    class="col-start-2 row-start-1 flex items-center gap-2 text-xs text-gray-500 dark:text-dark-400 lg:col-start-auto lg:row-start-auto"
                  >
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
                  <div
                    class="col-span-2 col-start-2 row-start-2 flex flex-wrap items-center gap-2 text-xs lg:col-span-1 lg:col-start-auto lg:row-start-auto lg:block"
                  >
                    <IntelligenceStatusBadge v-if="row.latest" :status="resultStatus(row.latest)" />
                    <span v-else class="text-gray-400 dark:text-dark-500">—</span>
                    <span
                      v-if="row.latest"
                      class="text-gray-500 tabular-nums dark:text-dark-400 lg:mt-1 lg:block"
                      data-testid="row-first-token"
                    >
                      {{ t('intelligence.firstToken') }}
                      {{
                        typeof row.latest.first_token_ms === 'number'
                          ? `${row.latest.first_token_ms} ms`
                          : '—'
                      }}
                    </span>
                  </div>
                  <div class="col-span-3 min-w-0 lg:col-span-1">
                    <IntelligenceResultBar
                      v-if="row.test.runs?.length"
                      :runs="row.test.runs"
                      compact
                    />
                    <span v-else class="text-xs text-gray-400 dark:text-dark-500">{{
                      t('intelligence.noRuns')
                    }}</span>
                  </div>
                  <div
                    class="col-start-3 row-start-1 justify-self-end lg:col-start-auto lg:row-start-auto"
                  >
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
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import ModelIcon from '@/components/common/ModelIcon.vue'
import IntelligenceResultBar from '@/components/intelligence/IntelligenceResultBar.vue'
import IntelligenceDrawing from '@/components/intelligence/IntelligenceDrawing.vue'
import IntelligenceSelfTestDialog from '@/components/intelligence/IntelligenceSelfTestDialog.vue'
import IntelligenceStatusBadge from '@/components/intelligence/IntelligenceStatusBadge.vue'
import IntelligenceStatusLegend from '@/components/intelligence/IntelligenceStatusLegend.vue'
import { recentArtifacts, recentRuns, resultStatus } from '@/components/intelligence/results'
import { intelligenceAPI, type IntelligenceTest } from '@/api/intelligence'
import { extractApiErrorMessage } from '@/utils/apiError'
import { provideIntelligenceResults } from '@/components/intelligence/useIntelligenceResults'

// IntelligenceView.vue displays authorized model histories in routing group cards.
// Each group uses shared card and badge styles for its name and billing
// multiplier. History colors distinguish test outcomes, and the row action uses
// the same secondary button treatment as other console lists.
// Model identity occupies a shared column beside adjacent site and user rows,
// whose latest artwork, status, history, and actions align for comparison.
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
const { t } = useI18n()
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
              .map((test) => ({
                test,
                latest: recentRuns(test.runs || []).slice(-1)[0],
                artwork: recentArtifacts(test.artifacts || [])[0],
              })),
          }
        }),
    }))
})

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
