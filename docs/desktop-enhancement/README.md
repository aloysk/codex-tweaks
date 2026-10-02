# Codex Companion 开发准备

本 fork 的目标是让官方 Codex App 保持主工作窗口，由独立原生 companion 控制面板管理主题、壁纸、状态和精选社区增强，并提供可选浮动胶囊；桌宠后置。主题与壁纸作用于官方 renderer，官方应用二进制与安装包保持原样。

当前进入**分批实现与验证**。两个准备 PR 已完成需求、架构、设计方向、开发 hooks 与双平台 CI。PR3 收口日志、错误处理、配置持久化、测试隔离、运行生命周期和分发身份；新的外观面板、胶囊和数据适配按后续 PR 推进。运行基础的合成测试和构建结果不能代替官方 App 真机接入、恢复或安装验收。

## 按任务阅读

| 任务 | 入口 |
| --- | --- |
| 理解产品目标、优先级与验收 | [PRD](PRD.zh-CN.md) |
| 确定下一步及所需证据 | [开发顺序与验证关口](IMPLEMENTATION.md) |
| 修改业务、前端、契约或目录结构 | [架构](../../ARCHITECTURE.md) 与 [开发约定](../../AGENTS.md) |
| 设计面板、胶囊与官方窗口外观 | [视觉方向](../../DESIGN.md) |
| 设置开发环境、运行 hooks 和构建检查 | [贡献指南](../../CONTRIBUTING.md) |
| 修改日志、配置、错误或退出流程 | [运行基础约定](../development/runtime-foundation.md) |
| 采用社区设计或代码 | [精选资源](../resources/README.md) 与 [来源清单](../resources/manifest.json) |
| 修改连接、包信任、安装或更新 | [上游边界](UPSTREAM.md) |
| 追溯选型依据 | [社区调研](research.zh-CN.md)，保留其调研时点 |

[上游现有产品定义](../../PRODUCT.md) 描述继承的实现，[功能包契约](../../Skills/develop-codex-tweaks-package/SKILL.md) 描述现有 API。PRD 定义新增目标；资源清单和历史调研不能覆盖新决定，也不能作为已完成实现的证明。

## 仓库与资源策略

- `origin` 指向自有 fork：<https://github.com/aloysk/codex-tweaks>；`upstream` 指向 <https://github.com/codex-tweaks/codex-tweaks>。
- 开发按 [PR 交付安排](IMPLEMENTATION.md#pr-交付安排) 使用隔离工作树推进，不为每项参考提前建模块或仓库。
- 自有功能包先用本地目录/ZIP 验证，进入 Git 分发时按上游规范一包一仓库。
- Ke-Spectrum-2-Design-System 与 zcode-monitor 是精选设计/生命周期参考；只记录可追溯依据，不整体复制其架构、代码或素材。
- 公开仓库保存设计、许可与公开证据；真实会话、凭据、用户图片和诊断原件留在用户设备。

## 本阶段与下一阶段

准备阶段已交付产品规格、源码职责与视觉方向、资源索引、贡献流程和开发 hooks。实现阶段每个 PR 包含对应测试、独立代理审查和双平台 CI；最终验证结果由实际命令与 CI 记录支持，不把目标数字填作成绩。

当前从 [G1](IMPLEMENTATION.md#g1-目标身份日常启动与恢复) 的合成进程/renderer 检查开始，再推进面板驱动官方外观、可信数据源和最小胶囊。普通官方启动不保证可附加；没有已核验增强连接时，外观能力不可用，用户需正常结束 Codex 后显式启动增强模式。安装、更新与卸载身份隔离是可安装 fork 的前置条件。
