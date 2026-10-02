"""Validate the fork's document links and reference catalog without network access."""

from __future__ import annotations

import json
from pathlib import Path
import re
import sys
from urllib.parse import unquote

ROOT = Path(__file__).resolve().parents[1]
DOCUMENTS = [ROOT / name for name in (
    "README.md", "AGENTS.md", "PRODUCT.md", "ARCHITECTURE.md", "DESIGN.md",
    "CONTRIBUTING.md", "docs/windows-ui-brief.md", "docs/open-items.md",
)]
for folder in ("desktop-enhancement", "resources", "development"):
    DOCUMENTS.extend(sorted((ROOT / "docs" / folder).glob("*.md")))


def check() -> list[str]:
    errors = []
    for path in DOCUMENTS:
        if not path.is_file():
            errors.append(f"Missing document: {path.relative_to(ROOT)}")
            continue
        content = path.read_text(encoding="utf-8")
        if len(re.findall(r"(?m)^```", content)) % 2:
            errors.append(f"Unbalanced code fences: {path.relative_to(ROOT)}")
        # This repository uses inline Markdown and HTML links, not reference links.
        links = re.findall(r"\]\(([^)]+)\)", content)
        links.extend(re.findall(r'(?:href|src)="([^"]+)"', content))
        for link in links:
            target = link.strip("<>").split("#", 1)[0]
            if not target or re.match(r"^[a-zA-Z][a-zA-Z0-9+.-]*:", target):
                continue
            if not (path.parent / unquote(target)).resolve().exists():
                errors.append(f"Broken local link: {path.relative_to(ROOT)} -> {target}")

    catalog = ROOT / "docs/resources/manifest.json"
    try:
        data = json.loads(catalog.read_text(encoding="utf-8"))
        if data.get("schemaVersion") != 1 or data.get("kind") != "reference-catalog":
            errors.append("Unsupported reference catalog format")
        ids = set()
        for item in data["resources"]:
            identity = item["id"]
            if identity in ids:
                errors.append(f"Duplicate resource: {identity}")
            ids.add(identity)
            if not re.fullmatch(r"[0-9a-f]{40}", item["revision"]):
                errors.append(f"Unpinned resource: {identity}")
            if not re.fullmatch(r"https://github\.com/[^/]+/[^/]+", item["repository"]):
                errors.append(f"Invalid resource repository: {identity}")
            if item["decision"] not in {
                "adopt-base", "adopt-tooling", "adapt-concept", "reference", "research", "defer"
            }:
                errors.append(f"Unknown resource decision: {identity}")
            if item["access"] not in {"public", "restricted"}:
                errors.append(f"Unknown resource access: {identity}")
            for copied in item["copiedProductFiles"]:
                candidate = (ROOT / copied).resolve()
                if not candidate.is_relative_to(ROOT) or not candidate.is_file():
                    errors.append(f"Invalid copied-product path: {identity} -> {copied}")
        for required in {"ke-spectrum-2", "zcode-monitor", "pre-commit-template", "codex-tweaks"}:
            if required not in ids:
                errors.append(f"Missing primary resource: {required}")
    except (OSError, ValueError, KeyError, TypeError) as error:
        errors.append(f"Invalid reference catalog: {error}")
    return errors


if __name__ == "__main__":
    failures = check()
    if failures:
        print("\n".join(failures), file=sys.stderr)
        raise SystemExit(1)
    print(f"Document checks passed: {len(DOCUMENTS)} documents and pinned reference catalog.")
