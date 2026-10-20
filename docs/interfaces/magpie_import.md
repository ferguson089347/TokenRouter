# Magpie 导入

本文记录 API 密钥页与 [Magpie 网页导入协议](https://usemagpie.ai/docs/import)的接入。其他客户端配置和模型目录规则见[模型目录与市场](model_catalog_and_marketplace.md)。

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

## 验证

`magpieImport.spec.ts` 检查接口地址和版本后缀、协议筛选、特殊字符编码及非法输入。`KeysView.spec.ts` 和 `KeyActionMenu.spec.ts` 检查当前行密钥、复合分组和菜单事件。
