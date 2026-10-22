import { apiClient } from './client'

// intelligence.ts defines the panel configuration, history, and preview
// response types and their HTTP calls. Administrator writes and manual
// execution use protected administrator endpoints. User calls submit owned
// credential identifiers and read shared observations and signed preview grants
// through the ordinary authenticated panel client.

// Panel requests use the authenticated application API; the backend generates
// drawing HTML locally and submits completed documents for remote evaluation.
// IntelligenceBenchmark identifies the candy or drawing benchmark selected for
// a check.
export type IntelligenceBenchmark = 'candy' | 'drawing'
// IntelligenceVerdict distinguishes pending execution, recognized quality
// outcomes, failure, and unknown assessment.
export type IntelligenceVerdict = 'pending' | 'passed' | 'failed' | 'error' | 'unknown'
// IntelligenceRun is a credential-free observation returned by panel result
// endpoints.
export interface IntelligenceRun {
  id: string // local record identifier
  config_id: number // owning configuration identifier
  group_id: number // configured routing group
  model: string // requested model identifier
  benchmark: IntelligenceBenchmark // selected benchmark type
  status:
    // job execution state
    'queued' | 'submitting' | 'running' | 'completed' | 'error' | 'unknown'
  verdict: IntelligenceVerdict // normalized assessment result
  phase?: string // reported execution phase
  question?: string // reported benchmark prompt
  answer?: string // reported model answer
  html?: string // untrusted drawing document
  input_tokens?: number | null // reported input tokens
  output_tokens?: number | null // reported output tokens
  reasoning_tokens?: number | null // reported reasoning tokens
  duration_ms?: number | null // reported duration milliseconds
  first_token_ms?: number | null // first token milliseconds
  assessment_reason?: string // remote assessment explanation
  error_message?: string // safe local diagnostic
  has_detail?: boolean // available observation detail
  has_artifact?: boolean // retained drawing presence
  has_thumbnail?: boolean // retained screenshot presence
  created_at: string // record creation timestamp
  updated_at: string // latest observation timestamp
  finished_at?: string | null // terminal observation timestamp
}

// IntelligenceConfig describes administrator execution settings with saved
// credential presence only.
export interface IntelligenceConfig {
  id: number // local record identifier
  group_id: number // configured routing group
  group_name: string // current group name
  model: string // requested model identifier
  benchmark: IntelligenceBenchmark // selected benchmark type
  // prompt supplies drawing instructions. Empty values select the backend's
  // default pelican animation, and candy configurations submit an empty value.
  prompt: string // optional drawing instruction
  base_url: string // public gateway endpoint
  api_key_configured: boolean // saved credential presence
  protocol: 'responses' | 'chat_completions' // remote model protocol
  reasoning_effort: string // requested reasoning level
  service_tier: string // requested service tier
  enabled: boolean // configuration availability switch
  schedule_enabled: boolean // periodic submission switch
  interval_minutes: number // scheduled interval minutes
  next_run_at?: string | null // next scheduled submission
  created_at: string // record creation timestamp
  updated_at: string // latest observation timestamp
  latest_run?: IntelligenceRun | null // newest lightweight observation
}

// IntelligenceConfigInput submits editable configuration fields and a write-
// only gateway Key. Empty api_key keeps the stored credential reference when
// an existing configuration is edited.
export type IntelligenceConfigInput = Pick<
  IntelligenceConfig,
  | 'group_id'
  | 'model'
  | 'benchmark'
  | 'prompt'
  | 'base_url'
  | 'protocol'
  | 'reasoning_effort'
  | 'service_tier'
  | 'enabled'
  | 'schedule_enabled'
  | 'interval_minutes'
> & { api_key: string }

// IntelligenceTest groups authorized model history and retained artifacts for
// user display.
export interface IntelligenceTest {
  source?: 'site' | 'user' // observed submission source
  id: number // local record identifier
  group_id: number // configured routing group
  group_name: string // current group name
  model: string // requested model identifier
  benchmark: IntelligenceBenchmark // selected benchmark type
  runs: IntelligenceRun[] // lightweight execution history
  artifacts: IntelligenceRun[] // retained artwork summaries
}

// IntelligenceSelfTestInput selects a personal Key for the channel's system drawing.
// The server checks ownership and copies the first enabled site drawing's execution
// parameters before queuing a publicly shared result under the user's credential.
export interface IntelligenceSelfTestInput {
  api_key_id: number // selected personal credential
}

// IntelligencePreview grants short-lived access to one sandboxed drawing
// document.
export interface IntelligencePreview {
  url: string // signed relative path
  expires_at: string // ticket expiration timestamp
}

// intelligenceAPI sends authenticated panel requests for authorized results,
// configuration management, manual execution, and signed artifact access.
export const intelligenceAPI = {
  // selfTest queues a drawing using the authenticated user's selected personal Key.
  // Results join the public user-test history for the Key's group and model;
  // credential text stays on the server and creation returns a queued observation.
  async selfTest(input: IntelligenceSelfTestInput): Promise<IntelligenceRun> {
    return (await apiClient.post<IntelligenceRun>('/intelligence-tests/run', input)).data
  },
  // thumbnail reads an authorized PNG snapshot using the ordinary panel bearer
  // credential. The backend captures a missing image from retained HTML and
  // stores it before responding; callers own any object URL created from the blob.
  async thumbnail(id: string, admin = false, signal?: AbortSignal): Promise<Blob> {
    return (
      await apiClient.get<Blob>(
        `${admin ? '/admin' : ''}/intelligence-tests/runs/${encodeURIComponent(id)}/thumbnail`,
        { signal, responseType: 'blob' },
      )
    ).data
  },
  // list reads the current user's authorized quality-check histories and
  // artifact summaries.
  async list(signal?: AbortSignal): Promise<IntelligenceTest[]> {
    return (await apiClient.get<IntelligenceTest[]>('/intelligence-tests', { signal })).data
  },
  // detail reads a complete saved observation using user scope by default.
  // Administrator scope selects the protected administrator endpoint, and an
  // optional signal cancels the outstanding HTTP read.
  async detail(id: string, admin = false, signal?: AbortSignal): Promise<IntelligenceRun> {
    return (
      await apiClient.get<IntelligenceRun>(
        `${admin ? '/admin' : ''}/intelligence-tests/runs/${encodeURIComponent(id)}`,
        { signal },
      )
    ).data
  },
  // preview requests a short-lived signed artifact URL using user scope by
  // default. Administrator mode selects the protected route; the content
  // endpoint rechecks current permissions when the frame navigates.
  async preview(id: string, admin = false, signal?: AbortSignal): Promise<IntelligencePreview> {
    return (
      await apiClient.get<IntelligencePreview>(
        `${admin ? '/admin' : ''}/intelligence-tests/runs/${encodeURIComponent(id)}/preview`,
        { signal },
      )
    ).data
  },
  // configs reads administrator configurations with lightweight latest-run
  // summaries and credential presence.
  async configs(signal?: AbortSignal): Promise<IntelligenceConfig[]> {
    return (
      await apiClient.get<IntelligenceConfig[]>('/admin/intelligence-tests', {
        signal,
      })
    ).data
  },
  // save creates a configuration when id is absent and edits the identified
  // record otherwise. The supplied gateway secret is write-only, and empty edit
  // input retains the saved credential reference.
  async save(input: IntelligenceConfigInput, id?: number): Promise<IntelligenceConfig> {
    return id
      ? (await apiClient.put<IntelligenceConfig>(`/admin/intelligence-tests/${id}`, input)).data
      : (await apiClient.post<IntelligenceConfig>('/admin/intelligence-tests', input)).data
  },
  // remove deletes the administrator-selected idle configuration and its
  // dependent observation history.
  async remove(id: number): Promise<void> {
    await apiClient.delete(`/admin/intelligence-tests/${id}`)
  },
  // run requests an explicit paid check through the administrator queue
  // endpoint. The server validates current feature and Key eligibility and
  // rejects concurrent work for the same configuration.
  async run(id: number): Promise<IntelligenceRun> {
    return (await apiClient.post<IntelligenceRun>(`/admin/intelligence-tests/${id}/run`)).data
  },
  // history reads the newest lightweight administrator observations for one
  // saved configuration.
  async history(id: number, signal?: AbortSignal): Promise<IntelligenceRun[]> {
    return (
      await apiClient.get<IntelligenceRun[]>(`/admin/intelligence-tests/${id}/runs`, { signal })
    ).data
  },
}
