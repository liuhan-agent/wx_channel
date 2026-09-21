# 微信视频号关键词优先采集设计

## 背景

微信视频号关键词采集通过 ltaoo 的 `finderSearch` API 发现作品，再通过
`finderGetCommentList` 获取评论。前者返回的 `work_id` 和
`object_nonce_id` 已经足够完成评论采集，但当前批次实现仍把作品公开分享链接
作为必填字段，导致分享链接解析失败时误丢作品，并被中台误报为页面结构变化。

关键词是主要使用模式；分享链接主要用于早期代理抓包验证和已有配置兼容，不应
成为关键词采集的硬依赖。

## 目标与非目标

### 目标

- 关键词模式仅以 `work_id + object_nonce_id` 作为作品和评论采集身份。
- 分享链接解析改为尽力增强：成功则输出，失败不阻断作品和评论采集。
- 新批次协议允许关键词结果的 `locator.public_url` 为空。
- TrendRadar 能导入没有公开分享链接的关键词结果，并保留空 `content_url`。
- 保留显式分享链接模式及 v1/v2 批次兼容。
- 以回归测试锁定关键词模式不依赖分享链接。

### 非目标

- 本次不移除 ltaoo 的分享链接接口。
- 本次不删除后端 `content_urls` 配置字段或数据库历史数据。
- 本次不改变微信页面导航、UI Automation 或授权流程。

## 方案

### 采集器

`CollectWorksFromSearch` 先完成 `finderSearch`，仅保留同时具备
`work_id` 和 `object_nonce_id` 的结果。对分享链接执行 best-effort 解析：

- 成功：填充 `locator.public_url`。
- 失败：保留作品，`locator.public_url` 为 `null`，增加
  `share_url_unavailable` 内容问题。

`Collector.CollectComments` 已经使用 `object_id` 和 `nonce_id` 调用
`finderGetCommentList`，无需改为页面或 URL 流程。

### 批次协议

新增 `wechat-channels-batch/3`：

- `locator.public_url` 可为 `null`。
- 其余字段、路径边界、秘密扫描、评论和媒体约束保持 v2 规则。
- v1/v2 继续按旧规则验证，避免旧消费者在不知情时读取新语义。

### TrendRadar 导入

- v3 且目标未配置 `content_urls` 时，按 `work_id` 导入，允许空
  `content_url`。
- 目标配置了 `content_urls` 时，仍要求每条内容提供并匹配合法分享链接。
- 空 `content_url` 在已有报告和详情页中按无链接文本展示，不改变数据库模式。
- `share_url_unavailable` 映射为明确的分享链接不可用诊断，不再映射为
  `page_structure_changed`。

### 管理界面

保留后端 `content_urls` 读取和执行兼容，但从主要目标编辑 UI 隐藏分享链接输入区，
关键词输入成为默认入口。已有配置不会被删除；后续可在单独的兼容入口恢复编辑。

## 错误与状态

- 搜索 API 结构解析失败：继续使用现有的搜索接口结构错误诊断。
- 单条结果无法解析公开分享链接：`share_url_unavailable`，不减少可采集作品，
  不改变批次为失败。
- 单条结果缺少 `object_nonce_id`：记录新的作品身份缺失问题并跳过该条，因为
  评论 API 无法安全构造请求。
- 评论 API 失败：继续使用现有评论阶段错误和断点恢复语义。

## 测试与验证

### wx_channel

- 搜索响应含作品但分享 URL 接口返回缺失字段：作品保留、评论请求携带
  `object_id + nonce_id`，批次输出 v3。
- 分享 URL 解析成功时仍输出规范化 URL。
- 缺少 `object_nonce_id` 的结果被明确跳过。
- v1/v2 既有批次测试继续通过。

### trendradar-monitor

- v3 关键词批次允许空 `public_url` 并导入 `content_url=""`。
- v3 显式分享链接模式继续拒绝空或不匹配 URL。
- v1/v2 fixtures 和旧导入行为不变。
- 错误映射不再产生 `page_structure_changed`。

### 真人联调

从当前 `main` 源码重新构建实际程序，使用已授权新版 ltaoo 运行关键词任务，记录
分支、提交号、工作区状态、构建产物、运行时、数据库和输出目录。成功标准是：
即使某个结果没有分享链接，其他具备 `work_id + object_nonce_id` 的作品仍可完成
评论采集并进入 TrendRadar。

## 兼容性与风险

- 需要先部署支持 v3 的 TrendRadar，再启用新微信批次；旧中台只能继续消费 v1/v2。
- 空 `content_url` 会使该作品无法从报告直接打开微信页面，但不影响评论数据和
  `content_id` 去重。
- 分享链接模式保留后端兼容，便于回滚和处理历史目标配置。
