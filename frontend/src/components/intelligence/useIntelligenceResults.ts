import { inject, onBeforeUnmount, provide, type InjectionKey } from 'vue'
import {
  intelligenceAPI,
  type IntelligencePreview,
  type IntelligenceRun,
} from '@/api/intelligence'

// useIntelligenceResults.ts shares detail and signed-preview loading within
// one mounted page. Pending reads with the same key share a promise, and at
// most two remote reads occupy slots while additional requests wait in order.
// Completed details are cached, while active observations remain refreshable.
//
// Disposal aborts running HTTP requests and drains waiting callbacks so their
// promises settle. Preview tickets refresh before expiration, and bounded per-
// page maps are released on unmount so another user or page instance receives
// its own cache.

// IntelligenceResultsLoader exposes page-shared observation reads and request-
// lifetime cleanup operations.
export interface IntelligenceResultsLoader {
  // detail returns a saved observation through the shared page cache. Active
  // jobs remain refreshable, and concurrent reads for the same run reuse one
  // pending request within the loader lifetime.
  detail(id: string): Promise<IntelligenceRun>
  // preview returns a signed drawing URL with its expiry timestamp. Cached
  // grants refresh before expiration; the content request still performs its own
  // current user and artifact authorization checks.
  preview(id: string): Promise<IntelligencePreview>
  // dispose cancels active HTTP reads and rejects queued work before releasing
  // cached observations. The page calls it on unmount, preventing request
  // results from being retained by another page instance.
  dispose(): void
}
// intelligenceResultsKey connects result bars, drawing cards and detail
// dialogs to their owning page's shared request loader.
export const intelligenceResultsKey: InjectionKey<IntelligenceResultsLoader> =
  Symbol('intelligence-results')

// createIntelligenceResultsLoader creates a page-owned cache and bounded
// request scheduler. Detail and preview requests deduplicate by kind and run
// identity, and cached previews are renewed before their signed lifetime ends.
//
// At most two active reads share the page's abort signal; queued reads settle
// after disposal observes cancellation. Call dispose when leaving the page so
// cached observations and credentials cannot outlive the owning page instance.
export function createIntelligenceResultsLoader(
  admin = false,
): IntelligenceResultsLoader {
  const controller = new AbortController()
  const details = new Map<string, IntelligenceRun>()
  const previews = new Map<string, IntelligencePreview>()
  const pending = new Map<string, Promise<unknown>>()
  const queue: Array<() => void> = []
  let active = 0
  // schedule deduplicates a keyed read and queues it behind the two active
  // slots. Completion releases a slot even after failure, and disposal causes
  // waiting callbacks to reject on the shared signal.
  //
  // The pending map retains a promise until its completion cleanup runs, so
  // concurrent consumers receive the same result. Active counting belongs to
  // this single JavaScript page instance and is updated synchronously before
  // another callback can start.
  function schedule<T>(key: string, fn: () => Promise<T>): Promise<T> {
    if (pending.has(key)) return pending.get(key) as Promise<T>
    const work = new Promise<T>((resolve, reject) => {
      const execute = () => {
        if (controller.signal.aborted) {
          reject(new DOMException('Aborted', 'AbortError'))
          queue.shift()?.()
          return
        }
        active++
        fn()
          .then(resolve, reject)
          .finally(() => {
            // Release this slot before starting the next queued read, including
            // failures, so request errors cannot stall the remaining gallery cards.
            active--
            queue.shift()?.()
          })
      }
      if (active < 2) execute()
      else queue.push(execute)
    }).finally(() => pending.delete(key))
    pending.set(key, work)
    return work
  }
  // remember stores a completed observation in a bounded page-owned cache. At
  // capacity the oldest inserted entry is discarded, and the supplied value is
  // returned for the active request consumer.
  function remember<T>(cache: Map<string, T>, id: string, value: T): T {
    // Bound retained observations independently of the full history size.
    if (cache.size >= 80) cache.delete(cache.keys().next().value!)
    cache.set(id, value)
    return value
  }
  return {
    // detail returns cached terminal observations and deduplicates outstanding
    // reads. Active observations continue through the bounded request scheduler
    // so later polling can return their completed content.
    detail(id) {
      if (details.has(id)) return Promise.resolve(details.get(id)!)
      return schedule(`detail:${id}`, async () => {
        const result = await intelligenceAPI.detail(
          id,
          admin,
          controller.signal,
        )
        // Keep active observations refreshable because subsequent polls may supply
        // completed assessment, diagnostics or generated content for the same run.
        return ['queued', 'submitting', 'running'].includes(result.status)
          ? result
          : remember(details, id, result)
      })
    },
    // preview reuses grants until their safety margin approaches expiry.
    // Refreshing uses the shared scheduler and abort signal so leaving the
    // page cancels access-grant requests along with other result reads.
    preview(id) {
      const cached = previews.get(id)
      // Refresh before expiry to leave time for iframe navigation.
      if (cached && Date.parse(cached.expires_at) > Date.now() + 30000)
        return Promise.resolve(cached)
      return schedule(`preview:${id}`, async () =>
        remember(
          previews,
          id,
          await intelligenceAPI.preview(id, admin, controller.signal),
        ),
      )
    },
    // dispose aborts running reads before clearing completed values and
    // rejecting queued work. Queue callbacks observe the same cancellation
    // signal, so waiting consumers settle when page ownership ends.
    dispose() {
      controller.abort()
      details.clear()
      previews.clear()
      while (queue.length) queue.shift()?.()
    },
  }
}

// provideIntelligenceResults creates and provides a loader for the mounted
// page. Administrator mode chooses the protected administrator routes, and
// automatic unmount cleanup cancels pending reads and clears the page cache.
export function provideIntelligenceResults(
  admin = false,
): IntelligenceResultsLoader {
  const loader = createIntelligenceResultsLoader(admin)
  provide(intelligenceResultsKey, loader)
  onBeforeUnmount(loader.dispose)
  return loader
}

// useIntelligenceResults obtains the loader provided by the current quality-
// check page. Components must mount beneath provideIntelligenceResults; a
// missing provider throws an initialization error before any request can be
// attempted.
export function useIntelligenceResults(): IntelligenceResultsLoader {
  const loader = inject(intelligenceResultsKey)
  if (!loader) throw new Error('Intelligence results loader is missing')
  return loader
}
