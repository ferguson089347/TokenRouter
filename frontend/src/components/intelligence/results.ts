import DOMPurify from 'dompurify'
import { marked } from 'marked'
import type { IntelligenceRun, IntelligenceTest } from '@/api/intelligence'

// results.ts converts stored quality-check observations into display states,
// chronological bars, grouped histories, and sanitized content. Execution
// failure and missing assessment remain separate from incorrect answers, and
// drawing URLs must match the dedicated signed content route before an iframe
// can load them.

export type IntelligenceDisplayStatus =
  'passed' | 'incorrect' | 'error' | 'unknown' | 'running' | 'queued'

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
  const status = resultStatus(run)
  if (status === 'passed') return 'bg-emerald-500'
  if (status === 'incorrect') return 'bg-rose-500'
  if (status === 'error' || status === 'unknown') return 'bg-amber-400'
  return 'bg-gray-300 dark:bg-dark-600'
}

// recentRuns copies and sorts observations oldest first, then keeps the
// requested newest count. Creation timestamp and local ID determine order,
// leaving the caller's original slice available unchanged.
export function recentRuns(runs: IntelligenceRun[], count = 60): IntelligenceRun[] {
  return [...runs]
    .sort((a, b) => a.created_at.localeCompare(b.created_at) || a.id.localeCompare(b.id))
    .slice(-count)
}

// recentArtifacts returns at most ten retained drawings in newest-first
// display order. Both summary artifact flags and already loaded HTML qualify,
// allowing lightweight list responses and full details to share rendering.
export function recentArtifacts(runs: IntelligenceRun[]): IntelligenceRun[] {
  return recentRuns(
    runs.filter((run) => run.has_artifact || !!run.html),
    10,
  ).reverse()
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
