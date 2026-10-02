# Upstream baseline and fork boundaries

This file records the evidence needed to continue the desktop enhancement work without confusing inherited code, proposed behavior, and verified runtime behavior.

## Source identity

| Item | Verified value |
| --- | --- |
| Upstream | https://github.com/codex-tweaks/codex-tweaks |
| Fork | https://github.com/aloysk/codex-tweaks |
| Baseline commit | `2c768f42579463254357ff1afaa4c38fc7d40cb1` |
| Baseline commit time | 2026-10-02 01:44:30 Asia/Singapore |
| Observed release | `v3.5.9`, 2026-10-02 Asia/Singapore |
| Upstream license | MIT; retained unchanged in the repository root |
| Preparation scope | Requirements, project documentation and development infrastructure; no new product feature code |

The fork was created from upstream, then cloned. It was not initialized as an unrelated empty repository. `origin` points to the fork; `upstream` points to the original repository. Local `remote.pushDefault` and the GitHub CLI default repository point to the fork to reduce accidental upstream writes.

## Intended companion direction

The fork's new product center is an independent native control panel with an optional floating capsule. The official Codex app remains the main work window. Theme, wallpaper and reading settings are intended to affect the official renderer through verified runtime adapters; styling the companion alone does not satisfy that requirement. Pets are a later presentation option.

This direction is not inherited functionality. The baseline has package management, CDP injection and native frontends; it does not yet implement the proposed capsule, status/speed adapters or a verified general control-panel-to-renderer settings bridge. Normal official launch followed by companion attachment is a target experience, not a proven capability. Companion startup alongside the official app or at login should ultimately stay in the background without launching Codex itself or taking focus. An explicit request to open the control panel should display or reuse that window; an explicit request to start Codex may start the official app. Existing startup behavior must be tested and changed explicitly before making that claim.

No official binary, application bundle, installer or `app.asar` is patched, repacked or replaced. Missing runtime access disables the affected capability rather than widening that boundary. Shared architecture lives in [ARCHITECTURE.md](../../ARCHITECTURE.md), visual mapping in [DESIGN.md](../../DESIGN.md), and product acceptance in the [PRD](PRD.zh-CN.md).

Ke-Spectrum-2-Design-System and zcode-monitor are curated design/lifecycle references, not imported subsystems. The [resource catalog](../resources/README.md) and [manifest](../resources/manifest.json) record source pins, review boundaries and reuse decisions. Their source-specific monitoring or design infrastructure is not a Codex API.

## Existing architecture to preserve

The root `AGENTS.md` is authoritative for implementation conventions. Go owns business logic, process control, CDP, injection, packages, updates, logging and presentation state. SwiftUI and WinUI 3 are native thin frontends. Presentation types and resources are generated; change their source rather than editing generated files.

| Surface | Code or contract |
| --- | --- |
| Business and runtime control | `backend/internal/core/` |
| Windows process behavior | `backend/internal/core/platform_windows.go` |
| Injection lifecycle | `backend/internal/core/injection.go`, `controller_runtime.go`, `cdp.go` |
| Settings adaptation | `backend/internal/core/cdp_settings.go` |
| Node authorization and runtime | `backend/internal/core/node_authorization.go`, `node_runtime.go` |
| Update source | `backend/internal/core/update.go` |
| Windows frontend | `windows/CodexTweaks.Windows/` |
| Presentation contract | `contract/presentation-contract.json` and `backend/cmd/contractgen/` |
| Package API | `Skills/develop-codex-tweaks-package/SKILL.md` |
| Windows build and packaging | `scripts/build-windows.ps1`, `package-windows.ps1`, `verify-windows.ps1` |
| CI and release | `.github/workflows/ci.yml`, `.github/workflows/release.yml` |

API v3 defines package lifecycle and capabilities such as `ui.settingsSections` v1. It is not a public Codex API for thread identity, usage, context, navigation or composer state. Those capabilities require separate adapters and evidence. Registration does not prove navigation, a native settings bridge, selected-thread binding or generation timing. A settings adapter can break even when the package API version remains valid. Account quota, observed activity and output rate retain different scope and precision.

## Verified issues to address before runtime adoption

These are static source findings at the baseline, not results of an executed prototype.

1. Windows restart targets the `ChatGPT.exe` image name and includes a force-termination path. Replace broad targeting and automatic escalation with verified process identity and a user-controlled exit/reconnect flow before exposing routine restart behavior. See [platform_windows.go](https://github.com/codex-tweaks/codex-tweaks/blob/2c768f42579463254357ff1afaa4c38fc7d40cb1/backend/internal/core/platform_windows.go#L143).
2. Disabling injection does not prove restoration. Per-target cleanup errors can be logged without propagating failure, the controller can mark cleanup complete despite failure, and callback errors can be swallowed. The design must distinguish injection stopped, page cleanup verified, and debugging listener closed. See [cdp.go](https://github.com/codex-tweaks/codex-tweaks/blob/2c768f42579463254357ff1afaa4c38fc7d40cb1/backend/internal/core/cdp.go), [controller_runtime.go](https://github.com/codex-tweaks/codex-tweaks/blob/2c768f42579463254357ff1afaa4c38fc7d40cb1/backend/internal/core/controller_runtime.go#L82) and [injection.go](https://github.com/codex-tweaks/codex-tweaks/blob/2c768f42579463254357ff1afaa4c38fc7d40cb1/backend/internal/core/injection.go#L714).
3. Node permission is whole-package trust at a specific revision, not a fine-grained sandbox. The upstream background package declares Node even when the chosen image source is local. Renderer-only experiments must remove the Node entry and remote-image feature path rather than merely changing an option. See [package manifest](https://github.com/codex-tweaks/codex-tweaks-custom-background/blob/main/package.json).
4. Live settings probes are runtime interactions: they can enable the debugger and import private modules even if no visible test page is mounted. They were not run during preparation.

## Fork distribution identity

Forking source does not establish an independent install or update channel:

- `UpdateRepository` in `backend/internal/core/update.go` still names `codex-tweaks/codex-tweaks`.
- `scripts/package-windows.ps1` still uses the upstream `com.crzhichen.CodexTweaks.$architecture` package ID and upstream title/author metadata.
- Application IDs, package IDs, update feeds, settings/data directories, signing identity and uninstall ownership must be reviewed together before distributing an installable fork.

The default product decision is side-by-side development with separate fork-owned state and no automatic migration. The final identifiers and branding must be chosen before the first installable fork build, then treated as persistent compatibility contracts. Preparation does not rename upstream namespaces, publish installers, create a release, or configure a fork updater. The Release workflow was disabled during preparation; this is separate from changing product update endpoints. CI artifacts remain verification outputs, not isolated installers approved for daily use.

## Initial development environment snapshot

These initial read-only checks were performed on 2026-10-02 Asia/Singapore, before development-hook setup and CI completion. This dated table records local tool presence only; later validation is recorded below and must not be confused with actual Codex runtime acceptance.

| Dependency | Upstream evidence | Local observation | Follow-up |
| --- | --- | --- | --- |
| Go | `go.mod` requires 1.26.0; `mise.toml` pins 1.26.4 | 1.27.0 windows/amd64 | Check with the repository-selected toolchain in a task-scoped environment; preserve the global install |
| .NET | Windows project/CI uses .NET 10 | SDK 10.0.112 and 10.0.401 present | Restore/build has not been performed |
| Windows SDK | Windows project targets Windows APIs | 10.0.26100.0 present | Verify actual build compatibility |
| Node / npm / npx | Needed for package compilation | Node 24.11.1; npm/npx 11.18.0 | Prefer package lockfiles; no package install was performed |
| Git / GitHub CLI | Required for repository and Git packages | Git 2.55.0.windows.3; gh 2.102.0 | Repository identity and fork relationship verified |
| PowerShell | Windows scripts | 7.6.6 | Scripts read, not executed |
| mise | Upstream task runner | Not found on PATH | macOS-oriented tasks are not a Windows setup recipe; resolve task-scoped tooling later |
| Official Codex | Target application | Store package 26.930.2377.0, x64 | No CDP connection, restart or injection was performed |
| Windows ARM64 / macOS | Upstream supported targets | No runtime evidence from this preparation | Use appropriate hosts for future release acceptance |

## Subsequent preparation validation and remaining gates

The first planning change was merged after Windows and macOS CI passed. This validates the checks performed by those workflows at that commit; it does not establish the fork's new UI, real-device installation, ARM64 runtime behavior or official-renderer compatibility.

The next preparation change installs repository-local development hooks and adds the companion requirements, architecture, design direction, curated resources and contribution workflow. Follow [CONTRIBUTING.md](../../CONTRIBUTING.md) for the authoritative commands and [development hooks](../development/pre-commit.md) for template provenance. Final local and CI outcomes belong to the delivery record; the presence of a script is not evidence it passed.

No prepared companion product has been started or installed, and the user's official Codex has not been connected, restarted or injected during this work. Product gates G1–G4 remain unexecuted; in particular the inherited process-targeting and cleanup issues above are not fixed by documentation or hooks.

Keep ordinary checks separate from opt-in live tests. Build/package scripts manage their own generated output directories; live probes interact with a real renderer and can enable debugging/import private modules. Review actual side effects and use the appropriate task-scoped setup. A Windows-only run cannot establish the macOS `mise run verify` gate, and ARM64 PE inspection on x64 cannot establish ARM64 runtime behavior. The [implementation plan](IMPLEMENTATION.md) defines the evidence required before runtime adoption or distribution.

## Upstream synchronization

Fetch upstream and inspect the diff from the recorded baseline before integrating updates. Resolve conflicts in the relevant modules and generated-contract source. Preserve the fork PRD separately from upstream's description of currently implemented behavior. Re-run affected compatibility checks when a change touches injection, process identity, settings adaptation, package trust or updates.

Public Git-distributed enhancement packages follow upstream's one-repository-per-package layout. A package stored in a subdirectory of this host fork is suitable for local development or ZIP export, not automatically an installable Git package.
