# Development

Start with the [architecture](ARCHITECTURE.md), [product requirements](docs/desktop-enhancement/PRD.zh-CN.md) and [implementation gates](docs/desktop-enhancement/IMPLEMENTATION.md). The fork implements the companion runtime foundation and a native appearance panel; remaining runtime and distribution acceptance is recorded in the implementation plan.

## Checkout and hooks

Use a feature branch in your fork. Confirm the remotes before pushing: `origin` is your fork and `upstream` is `https://github.com/codex-tweaks/codex-tweaks.git`. Preserve local work; use an isolated worktree when work would otherwise interfere.

Python 3.12 or later and Git are needed for hook bootstrap:

```text
python scripts/bootstrap-dev.py
```

This creates `.venv-dev` inside the checkout, installs pinned public developer tooling and installs the real `pre-commit` and `commit-msg` hooks using the committed configuration. It does not modify global Python packages or require access to the maintainer's private template repository. First setup needs network access; the hook environments may download their pinned tools on first use. See [pre-commit infrastructure](docs/development/pre-commit.md) for provenance, authorized local-template setup, overlay changes and refresh commands.

Run checks from the repository root on Windows:

```powershell
.venv-dev/Scripts/python.exe -m pre_commit run --all-files
python scripts/check-docs.py
git diff --check
```

On macOS, use `.venv-dev/bin/python` instead of `.venv-dev/Scripts/python.exe`. Hooks check the staged files at commit time; a formatter may fix a touched file, in which case inspect and stage that fix. Conventional Commits use forms such as `docs: define companion architecture`; the subject after `: ` has a 72-character maximum. Direct commits to `main`/`master` are blocked locally. CI skips only that local branch guard because it also verifies commits already on `main`.

The Gitleaks hook scans staged changes. A clean CI checkout has no staged changes, so CI separately runs pinned Gitleaks against the PR/push commit range, including merge diffs. Manual workflow runs scan the selected HEAD commit; a new branch without a prior base scans its reachable history. Successful hook execution alone is not evidence that committed content was scanned.

Project deviations belong in `.pre-commit-template-overlay.yaml`, not hand edits to the generated config. Refreshing or reproducing its generation is a maintainer operation: provide an authorized local checkout of the pinned template through `--template-source`, then run `scripts/render-pre-commit.py --write` or `--check` with `.venv-dev` Python. Ordinary clones and CI use the committed result. The repository preserves upstream generated-file line endings and does not add a Node frontend toolchain or require UTF-8 BOMs. Local agent indexes and virtual environments remain ignored.

## Product toolchain and checks

The source of exact versions is `mise.toml`, `backend/go.mod`, the Windows project and `.config/dotnet-tools.json`; CI shows the tested setup. Windows requires .NET 10, the appropriate Windows SDK and Go; package building also uses Node/npm/npx. macOS additionally requires Xcode and the tools selected by mise. Bootstrap above installs development hooks, not these native product toolchains.

Run the read-only preflight before development; choose the profile for the work you will verify:

```text
python scripts/check-dev.py --profile core
python scripts/check-dev.py --profile windows
python scripts/check-dev.py --profile macos
```

Each profile checks Python, Git, Go against `backend/go.mod`, Node/npm/npx and an existing Chrome, Edge or Chromium executable. Node runs offline DOM fixtures; a headless browser verifies real CSS, geometry and image decoding against synthetic content in a temporary profile. These tests never open the official app or use a real browser profile, account or session. Set `CODEX_TWEAKS_TEST_BROWSER` to an executable outside the usual installation paths. Missing browsers fail the tests instead of silently skipping them. npm/npx support optional package builds. `windows` additionally checks .NET and Windows SDK requirements from the project; `macos` checks Xcode and xcodegen. Use native profiles on their matching host. Missing optional CodeGraph/Serena CLIs are informational; MCP availability is not inferred from PATH. Preflight fails if a `CODEX_*LIVE*` or `CODEX_*INTEGRATION*` environment flag equals `1`. It does not install software, change settings or run tests, and a pass establishes tool readiness only.

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
