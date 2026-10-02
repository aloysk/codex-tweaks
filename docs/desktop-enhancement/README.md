# Codex Companion 开发准备

本 fork 的目标是让官方 Codex App 保持主工作窗口，由独立原生 companion 控制面板管理主题、壁纸、状态和精选社区增强，并提供可选浮动胶囊；桌宠后置。主题与壁纸作用于官方 renderer，官方应用二进制与安装包保持原样。

当前阶段为**需求与开发基础设施**。仓库继承上游产品；首轮规划已完成双平台 CI，开发 hooks 与项目文档在本轮完善。新的面板、胶囊、数据适配和恢复修复尚未实现；没有启动或安装我们的产品，也没有连接、重启或注入用户的官方 Codex。CI 通过不等于这些产品能力已经可用。

## 按任务阅读

| 任务 | 入口 |
| --- | --- |
| 理解产品目标、优先级与验收 | [PRD](PRD.zh-CN.md) |
| 确定下一步及所需证据 | [开发顺序与验证关口](IMPLEMENTATION.md) |
| 修改业务、前端、契约或目录结构 | [架构](../../ARCHITECTURE.md) 与 [开发约定](../../AGENTS.md) |
| 设计面板、胶囊与官方窗口外观 | [视觉方向](../../DESIGN.md) |
| 设置开发环境、运行 hooks 和构建检查 | [贡献指南](../../CONTRIBUTING.md) |
| 采用社区设计或代码 | [精选资源](../resources/README.md) 与 [来源清单](../resources/manifest.json) |
| 修改连接、包信任、安装或更新 | [上游边界](UPSTREAM.md) |
| 追溯选型依据 | [社区调研](research.zh-CN.md)，保留其调研时点 |

[上游现有产品定义](../../PRODUCT.md) 描述继承的实现，[功能包契约](../../Skills/develop-codex-tweaks-package/SKILL.md) 描述现有 API。PRD 定义新增目标；资源清单和历史调研不能覆盖新决定，也不能作为已完成实现的证明。

## 仓库与资源策略

- `origin` 指向自有 fork：<https://github.com/aloysk/codex-tweaks>；`upstream` 指向 <https://github.com/codex-tweaks/codex-tweaks>。
- 本轮在 `docs/companion-project-foundation` 完善文档与开发基础；后续按功能开分支，不为每项参考提前建模块或仓库。
- 自有功能包先用本地目录/ZIP 验证，进入 Git 分发时按上游规范一包一仓库。
- Ke-Spectrum-2-Design-System 与 zcode-monitor 是精选设计/生命周期参考；只记录可追溯依据，不整体复制其架构、代码或素材。
- 公开仓库保存设计、许可与公开证据；真实会话、凭据、用户图片和诊断原件留在用户设备。

## 本阶段与下一阶段

本阶段交付可执行的产品规格、源码职责与视觉方向、资源索引、贡献流程和开发 hooks，并完成适用的文档/基础设施检查。最终验证结果由实际命令与 CI 记录支持，不把目标数字填作成绩。

下一阶段从 [G1](IMPLEMENTATION.md#g1-目标身份日常启动与恢复) 的合成进程/renderer 检查开始，再推进面板驱动官方外观、可信数据源和最小胶囊。日常从官方入口正常启动后接入是待验证目标；若仍需手动准备 CDP，产品必须明确该限制。安装、更新与卸载身份隔离是未来可安装 fork 的前置条件。
