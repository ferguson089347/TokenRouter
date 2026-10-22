import { apiClient } from '../client'

// remoteAccountPool.ts defines remote directory responses and authenticated HTTP
// operations for the user and administrator pages. Saved tokens are write-only;
// list responses contain local group names and multipliers with snapshot timestamps
// so callers can identify data retained after a synchronization failure.

/** RemotePoolBinding maps one source group to its local display group. */
export interface RemotePoolBinding {
  remote_group_id: number // source group identifier
  local_group_id: number // local group identifier
}

/** RemotePoolSnapshotStatus describes the freshness of a saved remote directory. */
export interface RemotePoolSnapshotStatus {
  stale: boolean // previous snapshot being served
  auth_invalid: boolean // refresh token rejected remotely
  snapshot_at?: string // ISO snapshot completion timestamp
  warning?: string // sanitized remote synchronization warning
}

/** RemotePoolSettings reports configuration and token availability with secrets omitted. */
export interface RemotePoolSettings {
  enabled: boolean // remote directory enabled flag
  base_url: string // normalized remote site URL
  access_token_configured: boolean // saved access token present
  refresh_token_configured: boolean // saved refresh token present
  auth_invalid: boolean // refresh token rejected remotely
  bindings: RemotePoolBinding[] // source to local mappings
  expires_at?: string // ISO access token expiration
  snapshot_at?: string // ISO snapshot completion timestamp
  warning?: string // sanitized remote synchronization warning
}

/** RemotePoolSettingsInput carries administrator configuration and an optional replacement token. */
export interface RemotePoolSettingsInput {
  enabled: boolean // remote directory enabled flag
  base_url: string // administrator selected source URL
  /** An empty refresh_token retains the saved session when the source site remains unchanged. */
  refresh_token: string
  bindings: RemotePoolBinding[] // source to local mappings
}

/** RemotePoolGroup describes a local group visible in the account directory. */
export interface RemotePoolGroup {
  id: number // local group identifier
  name: string // local group display name
  rate_multiplier: number // local display cost multiplier
}

/** RemotePoolOptions supplies group and plan choices for the current caller. */
export interface RemotePoolOptions extends RemotePoolSnapshotStatus {
  groups: RemotePoolGroup[] // visible local group choices
  plan_types: Array<{ value: string; label: string }> // visible subscription tier choices
}

/** RemotePoolAccount exposes a site-local reference, tier and authorized local memberships. */
export interface RemotePoolAccount {
  id: string // site-local account reference
  display_name: string // account tier or platform
  platform: string // source upstream platform name
  auth_type: string // source authentication type label
  plan_type: string // source subscription tier identifier
  plan_label: string // source subscription tier label
  concurrency: number // source concurrent request limit
  current_concurrency: number // source occupied request slots
  status: string // source account availability state
  groups: RemotePoolGroup[] | null // current local group memberships
  usage_windows: Array<{ key: string; label: string; utilization: number | null }> | null // source usage period percentages
  rate_multiplier: number // local display cost multiplier
  account_rate_enabled: boolean // account specific multiplier flag
  last_used_at: string | null // ISO latest source usage
}

/** RemotePoolPage contains one filtered directory page and its snapshot status. */
export interface RemotePoolPage extends RemotePoolSnapshotStatus {
  items: RemotePoolAccount[] // visible filtered account summaries
  total: number // matching visible account count
  page: number // one based page number
  page_size: number // maximum accounts per page
  pages: number // total matching page count
}

/** RemotePoolFilters selects a local group and filters the saved account directory. */
export interface RemotePoolFilters {
  page: number // one based page number
  page_size: number // maximum accounts per page
  search?: string // account tier or local reference
  plan_type?: string // source subscription tier filter
  status?: string // source account availability filter
  group_id?: number // local group identifier filter
}

// path identifies administrator pool routes; timeout covers the service's bounded synchronization and persistence budget.
const path = '/admin/remote-account-pool'
const timeout = 70000

// remoteAccountPoolAPI supplies authenticated directory reads and administrator-only configuration operations through the shared API client.
export const remoteAccountPoolAPI = {
  /** getSettings returns administrator configuration with saved token values omitted. */
  async getSettings(signal?: AbortSignal) {
    return (await apiClient.get<RemotePoolSettings>(`${path}/settings`, { timeout, signal })).data
  },
  /** saveSettings saves source configuration, bindings, and an optional replacement token.
   * An empty token keeps the saved same-site session. The response contains token
   * availability flags so callers clear the submitted credential after success.
   */
  async saveSettings(input: RemotePoolSettingsInput) {
    return (await apiClient.put<RemotePoolSettings>(`${path}/settings`, input, { timeout })).data
  },
  /** sourceGroups returns the source directory used for administrator group bindings. */
  async sourceGroups(signal?: AbortSignal) {
    return (
      await apiClient.get<Array<{ id: number; name: string }>>(`${path}/source-groups`, {
        signal,
        timeout,
      })
    ).data
  },
  /** options returns the group and plan choices visible to this caller. */
  async options(signal?: AbortSignal) {
    return (
      await apiClient.get<RemotePoolOptions>('/account-pool/options', {
        signal,
        timeout,
      })
    ).data
  },
  /** list returns a filtered snapshot page through authenticated user directory routes. */
  async list(params: RemotePoolFilters, signal?: AbortSignal) {
    return (
      await apiClient.get<RemotePoolPage>('/account-pool', {
        params,
        signal,
        timeout,
      })
    ).data
  },
}
