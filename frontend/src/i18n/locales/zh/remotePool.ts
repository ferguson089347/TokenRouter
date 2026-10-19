// remotePool.ts supplies account directory labels for navigation, filters, and
// administrator settings. Both user and administrator pages share these strings
// so account states and local group bindings use consistent names throughout the
// remote pool interface.
export default {
  remotePool: {
    settingsTitle: '号池配置',
    settingsDescription: '设置远端站点、刷新令牌和本地分组映射。',
    connection: '远端连接',
    title: '号池列表',
    description: '查看远端号池的账号状态、并发占用和用量。',
    configure: '远端号池配置',
    enabled: '启用远端号池',
    baseURL: '远端站点地址',
    keepSecret: '已配置，留空保持原值',
    configured: '已配置',
    sessionStatus: 'AT：{at} · RT：{rt}',
    expiresAt: 'AT 到期时间',
    loginHint:
      '填写远端 RT 后自动刷新访问令牌。RT 失效时提醒管理员并展示最近快照，令牌由后端加密保存。',
    saved: '配置已保存',
    disabledHint: '请在配置中填写远端 RT 并启用号池。',
    search: '搜索账号名称或 ID',
    allGroups: '全部分组',
    accountType: '账号类型',
    allTypes: '全部账号类型',
    allPlans: '全部套餐',
    allStatuses: '全部状态',
    filter: '筛选',
    clear: '重置',
    loadFailed: '无法获取远端号池，请检查配置或稍后重试',
    columns: {
      display_name: '账号',
      platform: '平台 / 账号类型',
      capacity: '并发占用',
      status: '状态',
      groups: '所属分组',
      rate_multiplier: '倍率',
      usage_windows: '用量窗口',
      last_used_at: '最近使用'
    },
    status: {
      active: '可用',
      rate_limited: '限流中',
      temporarily_unavailable: '暂不可用',
      paused: '已暂停',
      error: '异常'
    },
    bindings: '分组绑定',
    loadSourceGroups: '加载远端分组',
    bindingHint:
      '先保存 RT，再加载远端分组并绑定本地分组。只有已绑定且本地启用的分组才展示，名称和倍率均使用本地分组；普通用户还须具备该本地分组权限。',
    bindLocalGroup: '绑定本地分组',
    notBound: '不展示（未绑定）',
    noBindings: '尚未加载远端分组，或没有可绑定的分组。',
    rtInvalid: '远端 RT 已失效，请管理员在配置中更新 RT。列表保留最后一次成功获取的数据。',
    staleHint: '远端数据暂未更新，当前展示最近一次成功获取的数据。请联系管理员检查连接或更新 RT。',
    snapshotAt: '数据更新时间',
    sourceGroup: '远端分组'
  }
}
