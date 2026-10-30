import DOMPurify from 'dompurify'
import { marked } from 'marked'
import type { IntelligenceRun, IntelligenceTest } from '@/api/intelligence'

// results.ts converts stored quality-check observations into display states,
// chronological bars, grouped histories, and sanitized content. User rows show
// the newest assessed result alongside the newest active task. Filtering quality
// history before limiting its length keeps pending tasks and diagnostics from
// displacing completed observations. Artwork matches the selected result identity,
// so a newer generation cannot illustrate an older quality verdict.
//
// Completion timestamps describe finished observations; legacy summaries use their
// last update time. Active records use submission time when inspected through a
// history component. Execution failure and missing assessment remain separate from
// incorrect answers, and drawing URLs must match the dedicated signed content route
// before an iframe can load them.

// IntelligenceDisplayStatus separates assessed quality from diagnostics and pending work.
export type IntelligenceDisplayStatus =
  'passed' | 'incorrect' | 'error' | 'unknown' | 'running' | 'queued'

// intelligenceStatusColors supplies identical history slot colors to the legend
// and observation bars so changing a status color updates both explanations.
export const intelligenceStatusColors: Record<IntelligenceDisplayStatus, string> = {
  passed: 'bg-emerald-500',
  incorrect: 'bg-rose-500',
  error: 'bg-amber-400',
  unknown: 'bg-amber-400',
  running: 'bg-gray-300 dark:bg-dark-600',
  queued: 'bg-gray-300 dark:bg-dark-600',
}

// resultStatus derives display state from execution and assessment with error
// and unknown precedence. Active jobs remain queued or running, and only a
// recognized passing or failing verdict becomes a model-quality result.
export function resultStatus(run: IntelligenceRun): IntelligenceDisplayStatus {
  if (run.status === 'queued') return 'queued'
  if (run.status === 'submitting' || run.status === 'running') return 'running'
  if (run.status === 'error' || run.verdict === 'error') return 'error'
  if (run.status === 'unknown' || run.verdict === 'unknown') return 'unknown'
  if (run.verdict === 'passed') return 'passed'
  if (run.verdict === 'failed') return 'incorrect'
  return 'unknown'
}

// resultColor returns the shared status color for one observed result.
export function resultColor(run: IntelligenceRun): string {
  return intelligenceStatusColors[resultStatus(run)]
}

// recentRuns copies and sorts observations oldest first, then keeps the
// requested newest count. Creation timestamp and local ID determine order,
// leaving the caller's original slice available unchanged.
export function recentRuns(runs: IntelligenceRun[], count = 60): IntelligenceRun[] {
  return [...runs]
    .sort((a, b) => a.created_at.localeCompare(b.created_at) || a.id.localeCompare(b.id))
    .slice(-count)
}

// summarizeTestRuns selects a row's assessed history and newest active task
// independently. Only completed observations with passing or failing verdicts enter
// the sixty-result history; pending work and execution diagnostics remain separate.
//
// The thumbnail must belong to the latest assessed observation, even when a newer
// unfinished task already has artwork. Missing retained HTML leaves the thumbnail
// empty instead of showing another run's drawing. Counts use the same filtered
// observations as the history bar, so partial histories retain their actual sample size.
export function summarizeTestRuns(test: IntelligenceTest) {
  const runs = test.runs || []
  const history = recentRuns(
    runs.filter(
      (run) => run.status === 'completed' && ['passed', 'incorrect'].includes(resultStatus(run)),
    ),
  )
  const latest = history.length ? history[history.length - 1] : undefined
  const active = recentRuns(
    runs.filter((run) => ['queued', 'submitting', 'running'].includes(run.status)),
    1,
  )[0]
  const artwork = latest
    ? [latest, ...(test.artifacts || [])].find(
        (run) => run.id === latest.id && (run.has_artifact || !!run.html),
      )
    : undefined
  const passedCount = history.filter((run) => run.verdict === 'passed').length
  return {
    history,
    latest,
    active,
    artwork,
    passedCount,
    failedCount: history.length - passedCount,
  }
}

// observationTime returns completion time for assessed records and submission time
// for other states. Historical completed records lacking finished_at use their
// recorded update time, keeping that timestamp distinct from the task's creation.
export function observationTime(run: IntelligenceRun): string {
  return run.status === 'completed' ? run.finished_at || run.updated_at : run.created_at
}

// groupTests groups authorized checks by group identity and requested model.
// It keeps each benchmark entry beneath its model and retains input encounter
// order for the page's returned group sections.
export function groupTests(tests: IntelligenceTest[]) {
  const groups = new Map<
    number,
    { id: number; name: string; models: Map<string, IntelligenceTest[]> }
  >()
  for (const test of tests) {
    if (!groups.has(test.group_id))
      groups.set(test.group_id, {
        id: test.group_id,
        name: test.group_name,
        models: new Map(),
      })
    const group = groups.get(test.group_id)!
    if (!group.models.has(test.model)) group.models.set(test.model, [])
    group.models.get(test.model)!.push(test)
  }
  return [...groups.values()].map((group) => ({
    ...group,
    models: [...group.models].map(([name, tests]) => ({ name, tests })),
  }))
}

// resultMarkdown renders model text through Markdown and sanitizes the
// resulting HTML. Missing content returns empty, while image, frame, form and
// style elements are removed before insertion into the panel DOM.
export function resultMarkdown(content?: string): string {
  if (!content) return ''
  // Remove remote image and active-content elements before inserting Markdown
  // into the panel so model answers cannot request external resources there.
  return DOMPurify.sanitize(
    marked.parse(content, { async: false, gfm: true, breaks: true }) as string,
    {
      FORBID_TAGS: ['img', 'svg', 'math', 'iframe', 'form', 'input', 'button', 'style'],
      FORBID_ATTR: ['style'],
    },
  )
}

// intelligencePreviewURL accepts a signed relative URL on the dedicated
// drawing route. Invalid paths return empty so response data cannot navigate
// the preview iframe to an arbitrary document or origin.
export function intelligencePreviewURL(value: string): string {
  return /^\/api\/v1\/intelligence-tests\/preview-content\/[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$/.test(
    value,
  )
    ? value
    : ''
}
