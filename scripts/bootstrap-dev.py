"""Install checkout-local development hooks without changing global tools."""

from __future__ import annotations

import argparse
import os
from pathlib import Path
import subprocess
import sys
import venv

ROOT = Path(__file__).resolve().parents[1]
TEMPLATE_COMMIT = "73e8483da0e4329f850681f79ea305300a3ed35c"


def run(*command: str) -> None:
    subprocess.run(command, cwd=ROOT, check=True)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--template-source",
        type=Path,
        help="Install the optional renderer from an authorized clean local template clone",
    )
    args = parser.parse_args()
    if sys.version_info < (3, 12):
        parser.error("Python 3.12 or newer is required")
    template_source = None
    if args.template_source is not None:
        source = args.template_source.resolve()
        try:
            revision = subprocess.check_output(
                ["git", "-C", str(source), "rev-parse", "HEAD"], text=True
            ).strip()
            dirty = subprocess.check_output(
                ["git", "-C", str(source), "status", "--porcelain"], text=True
            ).strip()
        except subprocess.CalledProcessError:
            parser.error("--template-source must point to a local Git checkout")
        if revision != TEMPLATE_COMMIT or dirty:
            parser.error("Local template must be clean at " + TEMPLATE_COMMIT)
        template_source = str(source)

    environment = ROOT / ".venv-dev"
    python = environment / ("Scripts/python.exe" if os.name == "nt" else "bin/python")
    if not python.exists():
        venv.create(environment, with_pip=True)
    dependencies = ["pre-commit==4.6.0", "PyYAML==6.0.3"]
    if template_source is not None:
        dependencies.extend(["tomlkit==0.15.1", template_source])
    run(str(python), "-m", "pip", "install", *dependencies)
    if template_source is not None:
        run(str(python), "scripts/render-pre-commit.py", "--check")
    run(str(python), "-m", "pre_commit", "validate-config")
    run(
        str(python), "-m", "pre_commit", "install",
        "--hook-type", "pre-commit", "--hook-type", "commit-msg",
    )
    print("Development hooks ready. Run:")
    print(f"  {python} -m pre_commit run --all-files")


if __name__ == "__main__":
    main()
