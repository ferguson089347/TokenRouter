import { apiClient } from './client'

/**
 * channelMonitor.ts reads Kedaya V2 passive channel metrics and versioned settings.
 * Repeated query keys preserve multi-select filters for Gin's QueryArray reader.
 * Callers supply AbortSignal to cancel obsolete page requests; writes forward
 * the saved configuration version so conflicting editors receive HTTP 409.
 */

/** MonitorRange names the four supported fixed passive observation windows. */
export type MonitorRange = '90m' | '24h' | '7d' | '30d'
/** HealthState distinguishes measured health bands from insufficient request observations. */
export type HealthState = 'unknown' | 'healthy' | 'warning' | 'critical'
/** MonitorMatrixGroupBy names platform, group, and model combinations supported by history queries. */
export type MonitorMatrixGroupBy =
  'platform' | 'platform_group' | 'platform_model' | 'platform_group_model'

/** MonitorFilter contains selected product scope forwarded as repeated query keys. */
export interface MonitorFilter {
  range: MonitorRange // selected product time window.
  platforms: string[] // enabled platform scope entries.
  groupIds: number[] // configured groupIds value.
  models: string[] // selected display model identifiers.
}

/** LatencyMetric contains nullable histogram percentile estimates and observed latency sample counts. */
export interface LatencyMetric {
  sample_count: number // observed valid latency count.
  p50_ms: number | null // milliseconds; null when unobserved.
  p90_ms?: number | null // milliseconds; null when unobserved.
  p95_ms: number | null // milliseconds; null when unobserved.
  avg_ms: number | null // milliseconds; null when unobserved.
}

/** MonitorMetric contains observed request ratios, token rates, and upstream latency metrics. */
export interface MonitorMetric {
  success_requests: number // observed successful eligible requests.
  error_requests: number // all observed failed requests.
  request_count: number // successful plus failed requests.
  token_count: number // combined measured token count.
  rpm: number // configured rpm value.
  tpm: number // configured tpm value.
  success_rate: number // actual successful request ratio.
  error_rate: number // scored nonignored error ratio.
  cache_rate: number // cached input token ratio.
  cache_rate_numerator: number // cache-read token count.
  cache_rate_denominator: number // input plus cache token count.
  ttft: LatencyMetric // configured ttft value.
  duration: LatencyMetric // upstream exchange latency metrics.
  upstream_affected_requests?: number // requests with recorded upstream failures.
  upstream_attempt_count?: number // recorded upstream error attempt count.
}

/** MonitorHealth contains sample-gated health labels and optional weighted signal scores. */
export interface MonitorHealth {
  overall: HealthState // combined weighted health category.
  error_rate: HealthState // scored nonignored error ratio.
  ttft: HealthState // configured ttft value.
  cache?: HealthState // cache ratio health category.
  /** 0–100 blended score when samples are sufficient. */
  score?: number | null // combined weighted health score.
  error_rate_score?: number | null // error ratio health score.
  ttft_score?: number | null // configured ttft score value.
  cache_score?: number | null // cache ratio health score.
  minimum_sample: number // minimum eligible request count.
  thresholds?: {
    minimum_sample?: number // minimum eligible request count.
    warning_error_rate: number // warning scored error ratio.
    critical_error_rate: number // critical scored error ratio.
    target_ttft_ms: number // configured target ttft ms value.
    warning_ttft_ms: number // configured warning ttft ms value.
    critical_ttft_ms: number // configured critical ttft ms value.
    warning_cache_rate?: number // warning cache ratio floor.
    critical_cache_rate?: number // critical cache ratio floor.
    error_weight: number // configured error scoring weight.
    ttft_weight: number // configured ttft weight value.
    cache_weight?: number // configured cache scoring weight.
  }
}

/** First-upgrade historical fill for 90m/24h/7d/30d; omitted when complete. */
export interface MonitorBootstrap {
  active: boolean // historical product coverage incomplete.
  progress_percent: number // completed product history percentage.
  covered_from?: string // earliest recomputed historical minute.
  target_start?: string // required product history start.
}

/** MonitorCoverage describes requested display bounds and the committed aggregation coverage. */
export interface MonitorCoverage {
  requested_start: string // requested display interval start.
  /** Exclusive upper bound of the UI-selected range (filter end). */
  requested_end?: string // exclusive display interval end.
  coverage_start: string // earliest shared covered time.
  data_through: string // latest committed covered minute.
  computed_at: string // latest successful aggregation time.
  aggregation_lag_seconds: number // seconds.
  coverage_complete: boolean // requested history fully covered.
  bucket_seconds: number // seconds.
  /** Present while initial aggregation has not covered the 30d product window. */
  bootstrap?: MonitorBootstrap | null // optional initial history progress.
}

/** MonitorConfig contains versioned display scopes, ignored categories, and health thresholds. */
export interface MonitorConfig {
  version: number // optimistic configuration revision number.
  enabled: boolean // configured passive monitoring switch.
  refresh_interval_seconds: 60 | 300 // seconds.
  platforms: Array<{ platform: string; enabled: boolean; models: string[] }> // enabled platform scope entries.
  group_ids: number[] // configured group ids value.
  health_thresholds: {
    minimum_sample: number // minimum eligible request count.
    warning_error_rate: number // warning scored error ratio.
    critical_error_rate: number // critical scored error ratio.
    target_ttft_ms: number // configured target ttft ms value.
    warning_ttft_ms: number // configured warning ttft ms value.
    critical_ttft_ms: number // configured critical ttft ms value.
    warning_cache_rate: number // warning cache ratio floor.
    critical_cache_rate: number // critical cache ratio floor.
    error_weight: number // configured error scoring weight.
    ttft_weight: number // configured ttft weight value.
    cache_weight: number // configured cache scoring weight.
  }
  /** Categories excluded from error_rate / health; still listed in error breakdown. */
  ignored_error_categories?: string[] // configured ignored error categories value.
}

/** MONITOR_ERROR_CATEGORIES supplies the ordered taxonomy shared by filters and settings. */
export const MONITOR_ERROR_CATEGORIES = [
  'content_policy',
  'authentication',
  'context_limit',
  'invalid_request',
  'model_unsupported',
  'group_access',
  'quota_or_balance',
  'account_pool_unavailable',
  'rate_or_capacity',
  'timeout',
  'transport_or_stream',
  'upstream_forbidden',
  'not_found',
  'client_cancelled',
  'upstream_5xx',
  'internal',
  'other',
] as const

/** MonitorErrorCategory names one entry in the ordered versioned error taxonomy. */
export type MonitorErrorCategory = (typeof MONITOR_ERROR_CATEGORIES)[number]

/** MonitorSnapshot combines current metrics, health, settings, and ordered trend buckets. */
export interface MonitorSnapshot {
  config: MonitorConfig // normalized saved monitor settings.
  coverage: MonitorCoverage // shared committed coverage details.
  metrics: MonitorMetric // measured rates and latency.
  health: MonitorHealth // sample-gated channel health decision.
  trend: Array<{ bucket_start: string; metrics: MonitorMetric; health: MonitorHealth }> // ordered display bucket metrics.
}

/** MonitorMatrixBucket contains the measured metrics and health for one display interval. */
export interface MonitorMatrixBucket {
  bucket_start: string // UTC display bucket start.
  metrics: MonitorMetric // measured rates and latency.
  health: MonitorHealth // sample-gated channel health decision.
}

/** MonitorMatrixRow contains one dimension total and its ordered fixed history buckets. */
export interface MonitorMatrixRow {
  platform: string // saved execution platform name.
  group_id?: number // configured group id value.
  group_name?: string // current group display name.
  model?: string // stored or displayed model.
  metrics: MonitorMetric // measured rates and latency.
  health: MonitorHealth // sample-gated channel health decision.
  buckets: MonitorMatrixBucket[] // ordered fixed history cells.
}

/** MonitorMatrixResponse contains history rows and the common committed observation coverage. */
export interface MonitorMatrixResponse {
  coverage: MonitorCoverage // shared committed coverage details.
  group_by: MonitorMatrixGroupBy // selected matrix grouping dimensions.
  items: MonitorMatrixRow[] // ordered query result rows.
}

/** MonitorDimensions contains authorized platform, group, and model choices for filtering. */
export interface MonitorDimensions {
  platforms: Array<{ value: string; label: string; request_count: number }> // enabled platform scope entries.
  groups: Array<{ id: number; name: string; platform?: string; request_count: number }> // available group filter choices.
  models: Array<{ value: string; label: string; platform?: string; request_count: number }> // selected display model identifiers.
}

/** MonitorModelRow contains observed metrics and health for one displayed platform model. */
export interface MonitorModelRow {
  platform: string // saved execution platform name.
  model: string // stored or displayed model.
  metrics: MonitorMetric // measured rates and latency.
  health: MonitorHealth // sample-gated channel health decision.
}
/** MonitorErrorRow contains an error-category rate with optional administrator diagnostic details. */
export interface MonitorErrorRow {
  category: string // versioned error taxonomy label.
  count: number // observed categorized error count.
  rate: number // categorized request error ratio.
  details?: Array<{
    platform?: string // saved execution platform name.
    model?: string // stored or displayed model.
    error_type?: string // stored gateway error category.
    status_code?: number // observed downstream HTTP status.
    upstream_status_code?: number // observed upstream HTTP status.
    message?: string // bounded redacted diagnostic text.
    count: number // observed categorized error count.
  }>
  /** true when category is in config.ignored_error_categories */
  ignored?: boolean // excluded from health numerator.
}
/** MonitorUserRow contains ranked metrics and viewer-authorized account identity for display. */
export interface MonitorUserRow {
  user_id?: number // configured user id value.
  rank: number // one-based visible rank position.
  email?: string // current account email label.
  username?: string // current account display name.
  display_label: string // viewer-safe ranking display label.
  is_self: boolean // authenticated current viewer row.
  can_drilldown: boolean // viewer can inspect identity.
  metrics: MonitorMetric // measured rates and latency.
}

/** params converts selected scope values into the repeated-query request shape. */
function params(filter: MonitorFilter) {
  return {
    range: filter.range,
    platform: filter.platforms.length ? filter.platforms : undefined,
    group_id: filter.groupIds.length ? filter.groupIds : undefined,
    model: filter.models.length ? filter.models : undefined,
  }
}

/** repeatedArrayParamsSerializer encodes arrays as repeated URL keys.
 * Null and empty values are omitted; callers provide query data rather than
 * interpolating serialized values into paths or executable expressions. */
export function repeatedArrayParamsSerializer(values: Record<string, unknown>): string {
  const search = new URLSearchParams()
  for (const [key, rawValue] of Object.entries(values)) {
    if (rawValue == null || rawValue === '') continue
    const entries = Array.isArray(rawValue) ? rawValue : [rawValue]
    for (const value of entries) {
      if (value != null && value !== '') search.append(key, String(value))
    }
  }
  return search.toString()
}

/** requestConfig combines scope, endpoint options, and request cancellation.
 * Endpoint options take precedence over shared scope keys; each request uses
 * the caller-owned AbortSignal and creates its own query object. */
const requestConfig = (
  filter: MonitorFilter,
  signal?: AbortSignal,
  extraParams: Record<string, unknown> = {},
) => ({
  params: { ...params(filter), ...extraParams },
  paramsSerializer: { serialize: repeatedArrayParamsSerializer },
  signal,
})

/** base selects the authenticated user or administrator monitor prefix. */
function base(admin: boolean) {
  return admin ? '/admin/channel-monitor/v2' : '/channel-monitor/v2'
}

/** getDimensions loads available platform, group, and model filter choices. */
export async function getDimensions(filter: MonitorFilter, admin = false, signal?: AbortSignal) {
  const { data } = await apiClient.get<MonitorDimensions>(
    `${base(admin)}/dimensions`,
    requestConfig(filter, signal),
  )
  return data
}
/** getSnapshot loads current metrics, configured health, trends, and coverage. */
export async function getSnapshot(filter: MonitorFilter, admin = false, signal?: AbortSignal) {
  const { data } = await apiClient.get<MonitorSnapshot>(
    `${base(admin)}/snapshot`,
    requestConfig(filter, signal),
  )
  return data
}
/** getMatrix loads fixed history buckets for the requested grouping. */
export async function getMatrix(
  filter: MonitorFilter,
  groupBy: MonitorMatrixGroupBy,
  admin = false,
  signal?: AbortSignal,
) {
  const { data } = await apiClient.get<MonitorMatrixResponse>(
    `${base(admin)}/matrix`,
    requestConfig(filter, signal, { group_by: groupBy }),
  )
  return data
}
/** getModels loads per-model health metrics within the selected scope. */
export async function getModels(filter: MonitorFilter, admin = false, signal?: AbortSignal) {
  const { data } = await apiClient.get<{ coverage: MonitorCoverage; items: MonitorModelRow[] }>(
    `${base(admin)}/models`,
    requestConfig(filter, signal),
  )
  return data
}
/** getErrors loads categorized failures with administrator-only diagnostic samples. */
export async function getErrors(filter: MonitorFilter, admin = false, signal?: AbortSignal) {
  const { data } = await apiClient.get<{ coverage: MonitorCoverage; items: MonitorErrorRow[] }>(
    `${base(admin)}/errors`,
    requestConfig(filter, signal),
  )
  return data
}
/** getUsers loads rankings with ordinary-viewer identities redacted by the server. */
export async function getUsers(filter: MonitorFilter, admin = false, signal?: AbortSignal) {
  const { data } = await apiClient.get<{ coverage: MonitorCoverage; items: MonitorUserRow[] }>(
    `${base(admin)}/users`,
    requestConfig(filter, signal),
  )
  return data
}
/** getConfig retrieves administrator configuration even when passive monitoring is disabled. */
export async function getConfig(signal?: AbortSignal) {
  const { data } = await apiClient.get<MonitorConfig>('/admin/channel-monitor/v2/config', {
    signal,
  })
  return data
}
/** updateConfig replaces local monitor settings using optimistic version checking.
 * Callers retain their draft on failure and reload configuration after a
 * conflict before deciding whether to retry their intended changes. */
export async function updateConfig(config: MonitorConfig, signal?: AbortSignal) {
  const { data } = await apiClient.put<MonitorConfig>('/admin/channel-monitor/v2/config', config, {
    signal,
  })
  return data
}
