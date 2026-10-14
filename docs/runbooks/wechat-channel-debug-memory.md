# 微信视频号采集故障记忆

这份记录用于后续排查前置复盘。它记录已经确认的事实和必须保留的检查点，不把尚未证实的猜测当成结论。

## 2026-10-14：关键词采集只得到 3 个作品

### 已确认

- TrendRadar 配置目标是 10 个作品；最近一次批处理结果是 3 个作品、8 条评论，状态为 `partial`，原因是 `search_incomplete`。
- 页面桥已经可用，评论接口也能返回数据，因此这次问题不在页面桥未就绪、评论分页或授权数据库。
- 最新批处理的搜索诊断显示：第一次 `finderSearch` 返回 1 个 `infoList` 项；后续多次搜索响应只有很小的空结果结构，但游标仍在变化。最终 3 个作品来自账号视频列表 `finderUserPage`，不是搜索结果完整返回的 10 个作品。
- 当前证据没有证明是本地去重把结果从 10 个压成 3 个，也没有证明代理把响应截断；问题边界已经收敛到 `finderSearch` 的场景、请求体、游标传递或响应解析链路。
- 2026-10-14 复核运行文件后确认：部署目录的 `wx_video_download.exe` 是 Go 1.20.14、2026-08-17 的旧 Ltaoo 二进制（SHA256 `18F0E4CA43CC16F8235C8CFD35031B6A822853C87B2493FBC2BCF87DDAD9F40`）。它具备 `server/-c/2023` 外形契约，也包含旧的 `lastBuff`，但不包含 `searchType === 3 ? 19 : 13`，因此启动检查原先会误放行旧程序。
- `.poc-build/wx_video_download_type3.exe` 是 Go 1.24.3、`with_gvisor,embed_inject,sqlite_only,embed_frontend_inject` 构建，SHA256 `4F30AB98CEE27955EF0F4C1A377BEE5E7D10E87A5B6810529C8A3FD2CAD566B1`；其内嵌桥包含 `key:channels:contact_list`、`searchType === 3 ? 19 : 13` 和 `lastBuff: data.next_marker`。后续启动检查必须验证这些内嵌标记，不能只验证 CLI 帮助。

### 已知的重复错误

1. 曾经把同名 POC 可执行文件当成 Ltaoo 运行程序交付。以后启动前必须执行 `--help` 契约检查并记录可执行文件 SHA256，确认包含 `server`、`-c/--config` 和默认端口 2023。
2. 仅检查 CLI 帮助仍然会放过旧 Ltaoo。启动前还必须检查内嵌页面桥的视频搜索标记：`searchType === 3 ? 19 : 13`、`lastBuff: data.next_marker` 和 `key:channels:contact_list`。
3. 不能直接修改授权 SQLite 来“修复”授权。运行程序更换后必须通过控制台重新检测和授权。
4. 不能在没有原始批处理诊断的情况下反复猜端口、CA、页面刷新或代理。先读取 `request.json`、`batch/manifest.json`、`page_diagnostics` 和结果清单。
5. 搜索目标数量不能用“返回了几个”反推配置。必须同时核对请求的 `limits.works`、搜索调用次数、每页结果形状、游标变化和最终作品来源。
6. 更换 `wx_video_download.exe` 后不能沿用旧的运行授权。授权记录绑定可执行文件 SHA256；如果状态返回 `ltaoo_hash_changed`，必须在管理控制台重新检测并授权，再启动采集。不能绕过这一层直接改授权 SQLite。

### 下次排查顺序

1. 先读取最新批处理目录，确认目标数量和每一次 `finderSearch` 的请求/响应形状。
2. 对照 `upstream/main` 的参考实现，逐字段核对视频搜索的 `type=3`、页面端 `scene=19`、`lastBuff` 游标和游标解码次数。
3. 用脱敏响应 fixture 重现“首屏账号项、后续空对象、游标继续变化”的序列，再分别验证请求字段、游标字段和终止条件。
4. 先写能复现该响应序列的回归测试，再修改搜索实现；修改后同时跑 Go 搜索测试、脚本测试和真实批处理。
5. 只有当新诊断再次显示桥不可用、运行程序不匹配或端口未监听时，才回到运行时/代理检查。

## 2026-10-14：重新授权后 10 个作品采集成功

### 已验证

- 新程序已由管理端重新检测并授权；授权记录中的 Ltaoo SHA256 前缀为 `bc52231e1014`。此前旧授权记录仍是 `18f0e4ca43cc`，状态明确为 `ltaoo_hash_changed`，这是换二进制后的预期保护，不是采集失败。
- 使用配置 `tiancun-shipin`、目标 `tiancun-shipinhao`、关键词“赴田村老圩，尝客家好食”、`limits.works=10` 执行了真实完整采集。
- 最新批次 `wx-657be2f5508f470eb4ee02a5b99d926f` 状态为 `succeeded`，`counts.works=10`、顶层评论 95 条、回复 10 条，结果文件包含 10 行作品记录；采集命令返回码为 0。
- 首次 `finderSearch` 响应包含 50 个 `objectList` 项，且每项都有对象 ID、nonce、描述和视频媒体字段；本次作品来源是搜索结果，不是 `finderUserPage` 账号列表。
- 这次结果证明：旧的“只得到 3 个作品”问题由旧 Ltaoo 未发送视频搜索所需的 `type=3 → scene=19` 契约造成。新程序、重新授权和页面刷新/自动刷新链路组合后，10 个作品目标可以完整收敛。

### 后续固定检查

1. 每次更换运行程序后，先记录 SHA256，并通过管理端重新检测、授权；看到 `ltaoo_hash_changed` 时停止采集排查。
2. 只读取本次运行目录的 `manifest.json`，核对 `request.limits.works`、`counts.works`、`finderSearch.objectList_present` 和作品来源；不要用旧目录结果判断新部署。
3. 只有 `counts.works` 达到目标且状态为 `succeeded`，或诊断明确给出服务端耗尽证据，才能结束本轮排查。

### 当前工作边界

这份记录不会把“已加入分页上限/空页诊断”当成最终修复。最终修复必须让配置为 10 时，在真实搜索响应覆盖足够作品，或者明确记录服务端确实只提供 3 个并给出可验证的来源耗尽证据。
