# 实现与验收记录

日期：2026-10-03（SGT）。本表集中记录文件、已执行检查和未完成关口；需求与通过标准仍由 [实施计划](IMPLEMENTATION.md) 负责。测试、构建、运行验收和发行分别记录。

| 批次 / 模块 | 主要文件 | 当前结果 | 验证与剩余关口 |
| --- | --- | --- | --- |
| PR3 运行基础 | `backend/internal/core/controller_runtime.go`、`cdp.go`、`platform*.go`、`identity.go` | 已合并 #3 | 后续变化复用其身份绑定、测试隔离和有界退出；合并不代表真实安装验收 |
| PR4 外观 | `controller_appearance.go`、`appearance_renderer.go`、`app/Sources/AppearanceView.swift`、`windows/CodexTweaks.Windows/Pages/AppearancePage.*` | 已合并 #4；受限预览 | 官方旧版本空白窗口证据不能外推到登录后会话或当前新版本；完整 G2 仍待验收 |
| PR5 统计规范化 | `backend/internal/core/signals.go`、`signals_test.go` | 草稿实现 | 4 项新增 Go 测试覆盖空值、账户切换、陈旧缓存、频率限制、身份竞态和保存失败；当前任务及速率来源未成立，明确不可用 |
| PR5 额度来源 | `backend/internal/core/signals_renderer.go` | 私有只读适配，未取得真机有效样本 | 唯一 renderer、唯一 scoped active query；账户 scope 仅在内存使用 SHA-256，缺失/歧义撤下旧值；完整缓存最多每 60 秒读一次，身份探测复用现有两秒刷新 |
| PR5 共享契约 / RPC | `backend/internal/rpc/server.go`、`backend/cmd/contractgen/main.go`、Go presentation 源与生成文件 | 协议 13，原生客户端共用快照 | Go RPC/生成器测试通过；瞬时统计不进入配置，胶囊仅保存开关与收起偏好 |
| PR5 双端概览 | `app/Sources/SignalsView.swift`、`windows/CodexTweaks.Windows/Pages/SignalsView.cs`、两端 AppModel / MainWindow | 原生统计与来源展开 | 单一页面滚动容器；未知值不当零；读取、缓存与源更新时间分开；Windows 契约/回复 7 项测试通过；macOS 等待 CI |
| PR5 最小胶囊 | `app/Sources/CapsuleWindowController.swift`、`windows/CodexTweaks.Windows/CapsuleWindow.*` | 默认关闭；显示、收起、隐藏、打开同一面板、拖动及重置位置 | 复用一个客户端；Win32 无激活保护 / NSPanel；Windows x64、ARM64 编译通过；100 次更新焦点、IME、DPI、拔屏与无障碍真机检查未执行 |
| PR6 日用与发行 | 原生启动和既有构建 / 打包 / 验证脚本 | 待实施与验收 | 全部 G1–G4、真实安装/升级/卸载、版本与性能证据仍是交付前置条件；不发布 Beta 或启用更新源 |

## 来源证据边界

额度的研究依据为 [Usage Overview 的 scoped Query Cache 选择](https://github.com/codex-tweaks/codex-tweaks-usage-overview/blob/2cfbec5563b5e0eb46cd146574d8986a14cc5e45/src/usage-query.js)，本项目自行实现有界只读观察，不分发其源码或资源。本机官方 Windows `26.930.3748.0` 的只读源码检查确认：`app-shared` 构建使用 `rate-limit-status`，SSE 为 user/account scope，轮询可以是无 scope key；无 scope key 在本项目保持不可用。真实账户内容未读取、保存或提交。

额度源尚无明确源更新时间，`dataUpdatedAt` 只作为缓存时间；不能以每次读取伪造源更新。单一真实任务信号和真实有效额度样本仍缺失，所以此草稿不能称为 G3 完成或状态版日用交付。精确速率未成立时保留不可用说明，不推算 token/s。

胶囊的代码与编译证据不能代替真实窗口焦点与工作区验证。ARM64 交叉构建不代表 ARM64 真机支持。未取得证据的项继续记为未执行。

## 独立审查与复验

PR5 已完成一次独立子代理审查，发现并修复 3 项问题：异步哈希后重新取得 Query Cache 数组快照；Windows 断连清空概览并阻止迟到快照与 DPI 消息重新显示胶囊；双端默认使用普通窗口层级。4 项受影响的 Go 测试复验通过，fixture 使用 `getAll()` 数组副本覆盖身份竞态。代码审查不替代 G3/G4 真机验收。

本地双架构构建、打包与产物验证曾通过（x64 执行 RPC；ARM64 仅 PE 校验，6 个发行文件）；审查修复后的最终产物由新检出的 CI 再验证。构建产物未签名、未安装、未发布。
