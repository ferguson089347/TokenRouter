// remotePool.ts supplies account directory labels for navigation, filters, and
// administrator settings. Both user and administrator pages share these strings
// so account states and local group bindings use consistent names throughout the
// remote pool interface.
export default {
  remotePool: {
    settingsTitle: 'Pool settings',
    settingsDescription: 'Configure the remote site, refresh token, and local group bindings.',
    connection: 'Remote connection',
    title: 'Account Pool',
    description: 'View accounts, groups and current usage from another TokenRouter instance.',
    configure: 'Remote pool settings',
    enabled: 'Enable remote account pool',
    baseURL: 'Remote site URL',
    keepSecret: 'Configured; leave blank to keep',
    configured: 'Configured',
    sessionStatus: 'AT: {at} · RT: {rt}',
    expiresAt: 'AT expires',
    loginHint:
      'Enter a remote RT to refresh access automatically. An expired RT prompts administrators to update it while the latest snapshot stays available. The server encrypts stored tokens.',
    saved: 'Settings saved',
    disabledHint: 'Enter a remote RT in settings and enable the pool.',
    search: 'Search account name or ID',
    allGroups: 'All groups',
    allPlans: 'All plans',
    allStatuses: 'All statuses',
    filter: 'Filter',
    clear: 'Reset',
    loadFailed: 'Unable to load the remote account pool. Check settings or try again later.',
    columns: {
      display_name: 'Account',
      platform: 'Platform / Plan',
      capacity: 'Concurrency',
      status: 'Status',
      groups: 'Groups',
      rate_multiplier: 'Multiplier',
      usage_windows: 'Usage windows',
      last_used_at: 'Last used'
    },
    status: {
      active: 'Available',
      rate_limited: 'Rate limited',
      temporarily_unavailable: 'Temporarily unavailable',
      paused: 'Paused',
      error: 'Error'
    },
    bindings: 'Group bindings',
    loadSourceGroups: 'Load remote groups',
    bindingHint:
      'Save RT, then load remote groups and bind local groups. Only bound, active local groups are shown, using local names and multipliers. Users must also have access to the local groups.',
    bindLocalGroup: 'Bind local group',
    notBound: 'Hidden (not bound)',
    noBindings: 'Load remote groups to configure bindings.',
    rtInvalid:
      'Remote RT has expired. Administrators must update RT in settings. The last successful snapshot remains visible.',
    staleHint:
      'Remote data could not be updated. Showing the last successful snapshot. Contact an administrator to check the connection or update RT.',
    snapshotAt: 'Data updated',
    sourceGroup: 'Remote group'
  }
}
