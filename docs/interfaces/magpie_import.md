# Magpie 接入

本文记录 API 密钥页与 [Magpie 网页导入协议](https://usemagpie.ai/docs/import)的接入，以及独立提供商插件的模型发现、请求和用量契约。其他客户端配置和模型目录规则见[模型目录与市场](model_catalog_and_marketplace.md)。

<a id="web_import"></a>
## 网页导入

API 密钥行的更多菜单提供「导入 Magpie」。点击后，在新窗口打开 `https://usemagpie.ai/import#...`，由 Magpie 展示提供商确认页；未安装时，网页提供下载入口。Magpie 在用户确认后保存提供商。CC Switch 的显示开关单独控制 CCS 入口。

链接的 `name` 使用站点名称，空白时为 TokenRouter，最多 80 个字符。`key` 使用当前行的密钥。参数由 `URLSearchParams` 编码并放在 URL fragment 中，浏览器请求导入网页时不会发送这个片段。链接包含密钥，应按凭据处理。

接口地址取公开设置中的 `api_base_url`，未配置时使用当前站点 Origin。前端保留部署路径，移除已有的 `/v1` 或 `/v1beta` 后缀，再按下表填入地址。带查询参数、片段、用户凭据或非 HTTP(S) 协议的地址不生成导入链接。

| 分组允许的协议 | Magpie 参数 | 地址后缀 |
| --- | --- | --- |
| `openai_chat_completions` | `chat` | `/v1` |
| `openai_responses` | `responses` | `/v1` |
| `anthropic_messages` | `anthropic` | 根地址 |
| `gemini_generate_content` | `gemini` | `/v1beta` |

普通密钥从所属分组读取协议，复合密钥取绑定分组的协议并集。分组信息优先使用当前页面已加载的可见分组，缺失时读取密钥内嵌的分组。没有上述文本协议时，页面提示无法导入。

链接省略 `models`，由 Magpie 使用密钥向网关查询模型列表，因此复合前缀和密钥级别名使用服务端返回的请求名称。

<a id="provider_plugin"></a>
## 提供商插件

`plugins/magpie-moonup/` 是 moonup 的独立 Magpie 提供商插件，包名为 `magpie-moonup`，provider ID 和默认展示名均为 `moonup`，使用 OpenCode 的插件 hook 形状。安装和本地测试步骤见[插件 README](../../plugins/magpie-moonup/README.md)。独立发布仓库为 `ferguson089347/magpie-moonup`，manifest 的仓库、首页和问题地址均指向该仓库。网页导入创建普通自定义提供商，不会安装这个插件。

每次登录先在 `https://moonup.dev` 和 `https://api.moonup.dev` 中选择一个地址，再填写 API Key。没有提交选择时默认使用前者，选中的地址与凭据保存在同一账号。插件选项 `baseURL` 可以固定地址，省去新账号的选择步骤，也可用于测试或自托管部署；已保存的账号地址优先于默认值。`name` 可覆盖提供商展示名。新账号名使用域名和「密钥尾号」标签，地址加完整 Key 的摘要作为内部 `uid` 区分同尾号账号。已有账号身份保持稳定，Magpie 用它关联模型设置和限额。用量 hook 省略 `user`，由宿主保留重名账号的区分标签。包图标内嵌 `moonup.dev` 的 favicon。

插件不访问面板认证接口，不创建 Key。Magpie 负责保存登录和账号切换，TokenRouter 负责分组路由和计费。模型查询与用量查询使用 Bearer 认证、15 秒超时并拒绝重定向。生成请求替换调用方认证头，只访问当前账号的文本接口，保留请求体、取消信号和响应流；Gemini 将 loader 的 `/v1/models/...` 改为 `/v1beta/models/...`。

`queries.mjs` 合并相同账号的并发元数据读取，模型列表保留 30 秒，用量保留 5 秒。缓存以完整站点路径、Key 和接口隔离，最多 128 条；每次返回 JSON 副本。失败结果不缓存，查询或生成返回 401 时清除该账号的所有查询缓存，已被清除的在途请求不能重新发布缓存。缓存有效期内的权限变化可能延迟显示，生成请求继续由服务端实时校验。查询 JSON 的声明和实际大小均限制为 4 MiB，读取正文也受同一个 15 秒截止时间约束。

HTTP 成功响应在缓存前检查模型和额度结构。空模型数组有效；空条目、重复型号、已提供协议的格式错误或能力结构错误会拒绝整次刷新，不用部分列表替代此前发现的模型。旧格式列表可以省略协议字段。额度模式、资金对象和消费窗口的结构也要通过校验，异常响应不占用缓存有效期。账号名对少于八个字符的误填凭据省略尾号。

Gemini `countTokens` 与生成使用相同账号的认证和 `/v1beta` 路径。生成请求直接返回原始响应，不读取或缓存响应体。

<a id="plugin_models"></a>
## 插件模型发现

`GET /v1/models?include=capabilities` 沿用普通模型列表的 API Key 认证、分组资格、自定义可见列表和别名规则，返回 OpenAI list envelope。每个条目包含 `id`、`object`、`display_name`、`protocols`、`native_protocols` 和 `attributes`。协议字段始终为数组，属性未知时为 `{}`；不包含上游地址或凭据。省略该参数时保留原有响应格式。

协议来自相同的 `RequestableModel`，属性使用分组路线的共同能力和已知最小上限。Key 别名继承一跳目标的属性与协议，复合 Key 在各自分组内展开别名后加回前缀，避免不同分组的同名模型互相借用协议。最终只写出通过已有可见性筛选的 ID。能力数据用于客户端配置，生成请求仍执行原来的权限检查。

插件在允许的文本协议里优先选择原生协议，同级按 Messages、Responses、Chat Completions、Gemini 排序。有协议信息的非对话模型不进入插件列表。它不为未知容量、推理档位和价格补默认值。查询失败抛出错误，由 Magpie 保留上一次模型列表；401 同时标记登录失效。

`0.1.3` 接受没有 `protocols` 字段的标准模型列表。兼容选择先读取 `modelProtocols` 中按完整模型 ID 配置的协议，再根据去掉最后一个斜杠前缀后的型号识别 Claude、GPT / Codex / o 系列和 Gemini，分别使用 Messages、Responses 和 Gemini；其他名称使用 Chat Completions。这个名称判断仅用于选择接口，公开模型 ID 不变。服务端明确返回的协议数组始终优先，包括空数组，自定义配置不扩大其范围。旧格式列表的名称不能证明分组开放了该协议，非标准别名或协议受限的分组需要配置实际协议。

Magpie 卡片中的「未暴露模型」表示当前提供商没有启用供客户端调用的模型，由宿主根据模型选择状态生成。插件用量 hook 另外检查宿主传入的模型目录：成功读取且没有可用对话模型时提示检查站点分组和模型权限；查询失败时保留原查询错误。获取模型和保存启用状态由 Magpie 完成。

## 插件用量

`auth.usage` 读取公开的 `/v1/usage`，结合 `billing.source`、`subscription`、`quota` 和 `rate_limits`。Key 自身额度与订阅资金相互独立，因此 `quota_limited` 模式仍可同时显示订阅日、周、月窗口。`billing.unit` 优先用于金额单位，钱包零余额保持为已观测值，缺失计数不解释成零。

钱包摘要使用 `plan` 展示「余额 12.34 USD」，同时将金额和单位写入 `balance`。旧版无限额钱包响应可使用 `remaining`，密钥额度模式的同名字段不作钱包余额。金额缺失时显示「余额未返回」或「密钥额度」，小额正余额保留至多六位小数，更小值使用小于标记。额度窗口使用「密钥总额度」「每日额度」「每周额度」「每月额度」等中文标签。

重置时间只使用服务端返回值，不根据客户端时区或旧窗口开始时间推算。Key 和订阅都有到期时间时显示较早的时间。额度耗尽、扣费来源不可用和到期不会要求重新登录；认证被 401 拒绝时才标记 `signIn=expired`，其他查询失败保留登录。窗口百分比交给 Magpie 路由，余额只作为余额展示。

## 验证

`magpieImport.spec.ts` 检查接口地址和版本后缀、协议筛选、特殊字符编码及非法输入。`KeysView.spec.ts` 和 `KeyActionMenu.spec.ts` 检查当前行密钥、复合分组和菜单事件。

后端 `models_test.go` 和 `models_capabilities_test.go` 检查可选能力查询、可见性、别名继承、复合分组隔离和缺失属性。插件 `npm test` 使用本地 HTTP 服务检查多 Key、四种文本协议、响应和取消，以及订阅与 Key 限额组合。

`queries.test.mjs` 覆盖并发合并、返回副本、账号与接口隔离、缓存期限、401 失效、在途请求失效、查询失败恢复和响应大小限制。插件 hook 测试覆盖生成 401 清除模型缓存，以及 Gemini token 计数。

模型和额度的格式错误、修复后的立即恢复、短 Key 的账号展示及未知消费窗口也由插件测试覆盖。

账号测试检查可读名称、同尾号 Key 的内部身份隔离和已有身份稳定。用量测试检查旧版钱包金额、零余额、小额余额、密钥剩余额度和空模型目录提示。
