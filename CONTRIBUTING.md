# Development

Start with the [architecture](ARCHITECTURE.md), [product requirements](docs/desktop-enhancement/PRD.zh-CN.md) and [implementation gates](docs/desktop-enhancement/IMPLEMENTATION.md). Current fork work prepares the companion product and developer infrastructure; it does not yet deliver new runtime features.

## Checkout and hooks

Use a feature branch in your fork. Confirm the remotes before pushing: `origin` is your fork and `upstream` is `https://github.com/codex-tweaks/codex-tweaks.git`. Preserve local work; use an isolated worktree when work would otherwise interfere.

Python 3.12 or later and Git are needed for hook bootstrap:

```text
python scripts/bootstrap-dev.py
```

This creates `.venv-dev` inside the checkout, installs pinned developer dependencies and installs the real `pre-commit` and `commit-msg` hooks. It does not modify global Python packages. First setup needs network access; the hook environments may download their pinned tools on first use. See [pre-commit infrastructure](docs/development/pre-commit.md) for provenance, local-template setup, overlay changes and refresh commands.

Run checks from the repository root on Windows:

```powershell
.venv-dev/Scripts/python.exe scripts/render-pre-commit.py --check
.venv-dev/Scripts/python.exe -m pre_commit run --all-files
python scripts/check-docs.py
git diff --check
```

On macOS, use `.venv-dev/bin/python` instead of `.venv-dev/Scripts/python.exe`. Hooks check the staged files at commit time; a formatter may fix a touched file, in which case inspect and stage that fix. Conventional Commits use forms such as `docs: define companion architecture`; the subject after `: ` has a 72-character maximum. Direct commits to `main`/`master` are blocked locally. CI skips only that local branch guard because it also verifies commits already on `main`.

The Gitleaks hook scans staged changes. A clean CI checkout has no staged changes, so CI separately runs pinned Gitleaks against the PR/push commit range, including merge diffs. Manual workflow runs scan the selected HEAD commit; a new branch without a prior base scans its reachable history. Successful hook execution alone is not evidence that committed content was scanned.

Project deviations belong in `.pre-commit-template-overlay.yaml`, not hand edits to the generated config. The repository preserves upstream generated-file line endings and does not add a Node frontend toolchain or require UTF-8 BOMs. Local agent indexes and virtual environments remain ignored.

## Product toolchain and checks

The source of exact versions is `mise.toml`, `backend/go.mod`, the Windows project and `.config/dotnet-tools.json`; CI shows the tested setup. Windows requires .NET 10, the appropriate Windows SDK and Go; package building also uses Node/npm/npx. macOS additionally requires Xcode and the tools selected by mise. Bootstrap above installs development hooks, not these native product toolchains.

For shared Go or contract work, from `backend/`:

```text
go run ./cmd/contractgen -root .. -check
go vet ./...
go test ./...
```

When intentionally changing presentation definitions, generate first with `go run ./cmd/contractgen -root ..`, then inspect every generated diff. Never edit generated Swift, C#, XAML or JSON directly. On macOS, `mise run generate` also regenerates the Xcode project.

For the Windows build/package acceptance path, from the root:

```powershell
./scripts/build-windows.ps1 -Version '0.0.1-beta.1' -BuildNumber 1
./scripts/package-windows.ps1 -Version '0.0.1-beta.1' -Channel beta
./scripts/verify-windows.ps1 -Version '0.0.1-beta.1' -Channel beta -RequirePackages
```

These scripts restore tools and manage generated build outputs. Run them in a suitable development checkout and follow the host's filesystem-removal safeguards. They validate both artifact architectures; only the current host architecture executes the RPC smoke test. They do not establish ARM64 real-device or interactive UI compatibility.

On macOS, `mise run verify` covers workflow lint, Go and Swift tests, build and generated-file drift. The repository's merge gate remains green Windows and macOS CI, plus applicable developer/document checks and review. Native runtime changes additionally need the targeted manual scenarios in the implementation plan. A docs-only PR does not need to operate a daily Codex instance.

## Live tests and distribution

Keep live-test flags unset during ordinary checks. `CODEX_TWEAKS_WINDOWS_APP_INTEGRATION=1` can restart a real official application; `CODEX_TWEAKS_LIVE_CDP=1` interacts with its renderer; `CODEX_TWEAKS_INTEGRATION=1` enables real builder smoke work. These require an appropriate disposable/test setup and their specific acceptance scope. Pure unit checks are not permission to touch a daily session.

Do not publish or install a fork build over the upstream product until package IDs, data roots, update feeds and uninstall ownership are isolated. The fork's Release workflow was disabled during preparation; re-enable it only when the distribution gate is satisfied. Do not create a release tag as part of routine documentation or infrastructure delivery. CI and package creation are distinct from publishing and installation.

## Review and document ownership

Before opening or merging a PR, inspect its diff for scope, sensitive data, generated drift and temporary files. Include the concrete change, direct checks and remaining limits in the PR description. Review findings against source and requirements, fix material issues and rerun affected checks.

- Product intent and acceptance: `docs/desktop-enhancement/PRD.zh-CN.md`.
- Implementation order and outstanding runtime gates: `docs/desktop-enhancement/IMPLEMENTATION.md`.
- Structure and naming: `ARCHITECTURE.md`; visuals and native mapping: `DESIGN.md`.
- Source provenance and reuse decisions: `docs/resources/`.
- Bootstrap and hook policy: this guide and `docs/development/pre-commit.md`.

Keep real sessions, credentials, personal wallpaper and diagnostic originals out of commits. Use synthetic fixtures and retain required third-party notices for any future code or asset reuse.
