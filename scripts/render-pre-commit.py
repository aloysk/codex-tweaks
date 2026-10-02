"""Check or refresh the template-derived config and Conventional Commits gate."""

from __future__ import annotations

import argparse
import importlib.resources
from pathlib import Path
import sys

from pre_commit_template import __version__
from pre_commit_template.config import load_yaml_list
from pre_commit_template.merger import generate_config, merge_hooks
from pre_commit_template.overlay import apply_overlay, load_overlay, validate_overlay

ROOT = Path(__file__).resolve().parents[1]
TEMPLATE_VERSION = "5.7.0"


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    mode = parser.add_mutually_exclusive_group(required=True)
    mode.add_argument("--check", action="store_true")
    mode.add_argument("--write", action="store_true")
    args = parser.parse_args()
    if __version__ != TEMPLATE_VERSION:
        parser.error(f"Expected pre-commit-template {TEMPLATE_VERSION}, got {__version__}")
    package = importlib.resources.files("pre_commit_template")
    data = Path(str(package / "data"))
    merged = merge_hooks(
        load_yaml_list(data / "core/hooks.yaml"),
        load_yaml_list(data / "profiles/csharp/hooks.yaml"),
    )
    overlay = load_overlay(ROOT)
    if not overlay:
        parser.error("The repository overlay is required")
    validate_overlay(overlay, {h["id"] for repo in merged for h in repo["hooks"]})
    merged, _ = apply_overlay(merged, overlay)
    expected = {
        ".pre-commit-config.yaml": (
            f"# Installed by pre-commit-template v{TEMPLATE_VERSION}\n"
            + generate_config(merged, overlay["default_install_hook_types"])
        ),
        ".githooks/conventional-commit": (
            package / "conventional_commit.py"
        ).read_text(encoding="utf-8"),
    }
    drifted = []
    for name, content in expected.items():
        path = ROOT / name
        if args.write:
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(content, encoding="utf-8", newline="\n")
            print(f"Rendered {name}")
        elif not path.exists() or path.read_text(encoding="utf-8") != content:
            drifted.append(name)
    if drifted:
        print("Template drift: " + ", ".join(drifted), file=sys.stderr)
        print("Review the overlay, then run this script with --write.", file=sys.stderr)
        return 1
    if args.check:
        print("Template config and Conventional Commits provider match v5.7.0 + overlay.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
