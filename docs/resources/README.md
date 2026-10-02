# Curated reference resources

This catalog turns community research into scoped inputs for the companion product. It records what to borrow, what requires adaptation and what stays outside the first release. Registration here does not install, import or activate software. The machine-readable [manifest](manifest.json) records the inspected source revisions; it is a documentation catalog, not a package manager or runtime registry.

The product combines selected ideas through the existing Go/native/package architecture. It does not bundle multiple competing CDP controllers or replace the official Codex app. Current priorities belong to the [PRD](../desktop-enhancement/PRD.zh-CN.md); the broader [research](../desktop-enhancement/research.zh-CN.md) remains a dated record.

## Primary references

| Resource | Decision | What we take | Boundary |
| --- | --- | --- | --- |
| [Ke Spectrum 2](https://github.com/aloysk/Ke-Spectrum-2-Design-System/tree/061a4e410437c5fba503e7672c5e2048d9eba095) | Reference | Semantic colors, calm hierarchy, spacing, Chinese text and keyboard principles | Native mapping in [DESIGN.md](../../DESIGN.md); no React/CSS/font bundle, no license file found in inspected checkout |
| [zcode-monitor](https://github.com/aloysk/zcode-monitor/tree/a4c00288776edf648be3a0d51f17065cc393ba29) | Adapt concept | Companion lifecycle, capsule/pet hierarchy, follow-window behavior and honest speed labels | MIT; ZCode hook, SQLite and telemetry fields do not establish Codex equivalents; no dashboard or gamification scope imported |
| [Codex Tweaks](https://github.com/codex-tweaks/codex-tweaks/tree/2c768f42579463254357ff1afaa4c38fc7d40cb1) | Adopt base | Go sidecar, native clients, package lifecycle and generated presentation contract | MIT; inherited restoration/process/update identity gaps remain explicit in UPSTREAM.md |
| [pre-commit-template](https://github.com/aloysk/pre-commit-template/tree/73e8483da0e4329f850681f79ea305300a3ed35c) | Adopt tooling | Layered hooks with a project overlay and reproducible bootstrap | MIT; installed development infrastructure, never a product runtime dependency |

The maintainer's local checkouts are `F:/project/Ke-Spectrum-2-Design-System`, `F:/project/zcode-monitor` and `F:/project/pre-commit-template`. They are convenient source references, not required absolute build paths. Another developer can use the pinned remote revisions.

## Community feature inputs

| Resource | Decision and intended use | Why it is not imported wholesale |
| --- | --- | --- |
| [Codex Token Overlay](https://github.com/soleillevant0125/codex-token-overlay/tree/b3a38d727fb2e0cf8e8c92ffff3f65da9a592dc5) | Adapt selected-window binding, unobtrusive capsule and local token observations | MIT; internal IPC and logs need current-version, multi-window and task-switch validation |
| [Usage Overview](https://github.com/codex-tweaks/codex-tweaks-usage-overview/tree/2cfbec5563b5e0eb46cd146574d8986a14cc5e45) | Research a low-permission account-quota adapter | MIT; React Fiber/query cache is a private interface, not package API v3; ambiguous account scopes remain unavailable |
| [Custom Background](https://github.com/codex-tweaks/codex-tweaks-custom-background/tree/6aadc38356d6d07ba3b3c1fa64bb5835557c4745) | Adapt local wallpaper settings and cleanup behavior | MIT; shipped package declares Node authority including remote-image features; selecting a local image does not remove that authority |
| [Codex Dream Skin](https://github.com/Fei-Away/Codex-Dream-Skin/tree/34335d27d54300eccb325cc652f6c93fef428b84) | Adapt preview/apply/restore and readable wallpaper composition | MIT code; wallpaper assets need their own rights, and its launcher must not become a second concurrent controller |
| [CodeDrobe Desktop](https://github.com/CodeDrobe/desktop/tree/2401437afd4302821574bae75cc2ed5ce1b75e4a) / [Core](https://github.com/CodeDrobe/core/tree/23ffdef633ddd9b0258a957923907c88be4a5fd0) | Research declarative themes, preflight and restoration | Desktop MPL-2.0 / Core Apache-2.0; component-specific obligations, and Core may edit appearance configuration; no store/account subsystem planned |
| [Token Monitor](https://github.com/Javis603/token-monitor/tree/a63cc48e84b75cd6c3d28cda5407e9fea9fd57b1) | Reference widget density, docking and configurable presentation | MIT; multi-provider synchronization, live authentication changes and account switching are outside our monitoring boundary |

Token Monitor's manifest intentionally pins the source inspected in the research, not a subsequently observed newer HEAD. A pinned reference describes evidence, not a promise to track or install the newest version automatically.

Pets, notifications, voice/hardware helpers and existing Skills/MCP entry points can be evaluated after the companion baseline. Full replacement clients such as Harnss, AionUi and CodexMonitor remain interaction references. Archived binary-patching projects, sources with unresolved licensing, credential-writing monitors and offline retrospective dashboards are not first-release dependencies. Exact candidate links and limitations remain in the research rather than being duplicated as another backlog.

## Reuse procedure

1. Name the concrete user benefit and its PRD module; select reference, concept adaptation or code reuse explicitly.
2. Inspect the pinned source and license for the exact files or assets to be reused. Public readability alone is not a reuse grant. Keep required copyright, license and notices with copied code; list copied paths and local adaptations in this catalog if reuse occurs.
3. Implement through the existing architecture and one enhancement controller. Demonstrate source identity, cleanup and compatibility for any private Codex interface.
4. Test the accepted behavior and failure case before changing the decision to implemented. Refresh the revision only after reviewing its changes; no automatic vendoring or broad repository mirroring.

At this preparation stage, external community code/assets have not been copied into product code. The existing fork retains its MIT license, and the installed development-template files retain their provenance.
