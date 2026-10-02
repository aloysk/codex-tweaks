# Development hooks

Run from the repository root with Python 3.12 or newer and Git:

```sh
python scripts/bootstrap-dev.py
```

The default bootstrap creates `.venv-dev`, installs public pinned pre-commit
tooling, validates the committed configuration, and installs real `pre-commit`
and `commit-msg` hooks in this checkout. It uses the generated config and provider
already in the repository; it does not fetch or install the template source.
It does not install the product or modify global Git or Python configuration.
A competing `core.hooksPath` causes pre-commit to refuse
installation; resolve that intentionally rather than bypassing it.

On Windows, `python` must be a working Python installation, not only the Store
alias. Use `py -3.12` instead when that is the installed launcher. On macOS or
Linux, use `python3` if `python` is unavailable. First setup needs network access
for public Python packages and pinned hook repositories. The Gitleaks hook uses
Go on `PATH` or lets pre-commit provision Go; CI explicitly selects the project
Go toolchain.

## Run the checks

Windows PowerShell:

```powershell
.\.venv-dev\Scripts\python.exe -m pre_commit run --all-files --show-diff-on-failure
```

macOS, Linux, and the lightweight CI job:

```sh
.venv-dev/bin/python -m pre_commit run --all-files --show-diff-on-failure
```

Ordinary commits check staged files and require a Conventional Commit header,
for example `docs(dev): document hook setup`. The commit message provider allows
the template's `feat`, `fix`, `docs`, `style`, `refactor`, `test`, `chore`, `perf`,
`ci`, and `revert` types; the subject after `: ` is 1–72 characters. Merge,
revert, fixup, and squash commit prefixes follow the provider's exemptions.
Full-file runs do not include untracked files: stage intended additions before
the final check. Never stage local `.venv-dev`, `.serena`, or hook caches.

The Gitleaks hook always scans staged changes, even with `--all-files`. CI adds
a pinned Gitleaks commit-range scan, with first-parent merge diffs included, so
an empty index cannot produce a misleading secret-scan pass. See CONTRIBUTING
and `.github/workflows/ci.yml` for the PR, push and manual-run scopes.

These are lightweight repository checks. They do not replace Go tests,
Presentation Contract drift checks, native builds, or release verification.

## Source and project decisions

The initial installer was the user's clean `pre-commit-template` v5.7.0 source
at commit `73e8483da0e4329f850681f79ea305300a3ed35c` from
[aloysk/pre-commit-template](https://github.com/aloysk/pre-commit-template/tree/73e8483da0e4329f850681f79ea305300a3ed35c).
The copied providers retain their MIT notice in
`.githooks/LICENSE.pre-commit-template`.
Installation used `--profile csharp --no-ci --no-setup-hooks`, first with
`--dry-run`, then without it. Existing CI and the generated-source LF rules in
`.gitattributes` were preserved. Default bootstrap pins public pre-commit 4.6.0
and PyYAML 6.0.3. Template rendering is a separate maintainer operation requiring
authorized access to a clean local checkout at the pinned template commit; it
is not required to contribute or run CI. The explicit option below additionally
installs that local source and tomlkit 0.15.1, then checks generated consistency:

```sh
python scripts/bootstrap-dev.py --template-source /path/to/pre-commit-template
```

The `csharp` profile is the smallest relevant profile for this Go/Swift/WinUI
repository. `.pre-commit-template-overlay.yaml` records all hook deviations:

- Disable UTF-8 BOM enforcement: the shared repository uses UTF-8 without BOM.
- Disable changelog/open-items gates: product verification stays in the existing
  implementation plan; installation does not create a new release policy.
- Add the template's verbatim Conventional Commits provider at `commit-msg`.
- Check mixed line endings without rewriting complete Windows checkouts.
- Exempt only the Apple-generated `app/Resources/Icons/AppIcon.icon/icon.json`
  from the final-newline fixer; preserve its upstream bytes.

Core checks retain secret detection, filename/merge/symlink checks, structured
file parsing, whitespace checks, and main/master commit protection. No Node app,
frontend formatter, Go formatter, or native build hook is assumed. The editor
configuration follows the template with Go tabs; generated product files remain
owned by their generators.

The v5.7.0 installer copies profile helper files and seeds `docs/open-items.md`
even when their hooks are disabled. Those helpers remain inert, and the seed
points to the existing implementation plan. Their presence makes the template's
`--check --profile csharp --target .` completeness check meaningful without
adding another task ledger. The initial installer warning about skipped
`.editorconfig` and `.gitattributes` is expected: project-owned versions were
preserved; actual hook activation must be checked separately.

## Change or refresh the configuration

Only maintainers with authorized template source access regenerate these files.
Prepare the optional dependency before editing the overlay:

```sh
python scripts/bootstrap-dev.py --template-source /path/to/pre-commit-template
```

The source must be clean at the pinned commit; a different or modified checkout
is refused. No token or private-repository fetch is part of default bootstrap.
Ordinary contributors and CI run the committed generated hooks without importing
the template or running the renderer.

After editing the overlay, run the following with the checkout's `.venv-dev`
Python (`.venv-dev/Scripts/python.exe` on Windows, `.venv-dev/bin/python` elsewhere):

```sh
python scripts/render-pre-commit.py --write
python scripts/render-pre-commit.py --check
python -m pre_commit validate-config
python -m pre_commit run --all-files
```

The renderer uses the installed, version-checked template merge/overlay APIs. It
only writes `.pre-commit-config.yaml` and `.githooks/conventional-commit`; the
latter is a verbatim provider copy. Review those diffs before committing. Avoid
the template's blanket `--force` reinstallation, which would overwrite the
repository's `.editorconfig` and `.gitattributes` customizations. Template
upgrades require updating the bootstrap commit and renderer version together,
reviewing overlay warnings and provider changes, and rerunning all checks.
