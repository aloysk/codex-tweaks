"""Read-only developer preflight; does not install tools or run tests."""

from __future__ import annotations

import argparse
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import xml.etree.ElementTree as ET

ROOT = Path(__file__).resolve().parents[1]


def version(text: str) -> tuple[int, ...]:
    return tuple(int(part) for part in text.split("."))


def probe(name: str, *arguments: str) -> str | None:
    executable = shutil.which(name)
    if executable is None:
        print(f"FAIL {name}: missing from PATH")
        return None
    environment = os.environ.copy()
    # Version discovery must not download a Go toolchain or initialize .NET tooling.
    environment.update({
        "GOTOOLCHAIN": "local",
        "DOTNET_SKIP_FIRST_TIME_EXPERIENCE": "1",
        "DOTNET_CLI_TELEMETRY_OPTOUT": "1",
        "DOTNET_NOLOGO": "1",
    })
    try:
        result = subprocess.run(
            [executable, *arguments], cwd=ROOT, env=environment,
            capture_output=True, text=True, encoding="utf-8", errors="replace",
            timeout=15, check=False,
        )
    except (OSError, subprocess.TimeoutExpired) as error:
        print(f"FAIL {name}: version probe failed ({type(error).__name__})")
        return None
    output = result.stdout.strip()
    if result.returncode != 0 or not output:
        print(f"FAIL {name}: version probe returned {result.returncode} or no version")
        return None
    return output


def core() -> int:
    failures = 0
    if sys.version_info < (3, 12):
        print("FAIL Python: 3.12 or later is required for developer tooling")
        failures += 1
    else:
        print(f"PASS Python: {sys.version.split()[0]}")
    minimum = re.search(r"(?m)^go (\d+\.\d+(?:\.\d+)?)$", (ROOT / "backend/go.mod").read_text())
    if minimum is None:
        print("FAIL Go: cannot read the required version from backend/go.mod")
        failures += 1
    else:
        output = probe("go", "version")
        installed = re.search(r"\bgo(\d+\.\d+(?:\.\d+)?)(?=\s)", output or "")
        required = version(minimum[1])
        actual = version(installed[1]) if installed else ()
        # Go 1.x historically omitted .0 from the first stable release's version.
        if actual and actual + (0,) * (3 - len(actual)) >= required + (0,) * (3 - len(required)):
            print(f"PASS Go: {installed[1]} (requires >= {minimum[1]})")
        else:
            if output is not None:
                print(f"FAIL Go: stable version >= {minimum[1]} is required by backend/go.mod")
            failures += 1
    for name in ("git", "node", "npm", "npx"):
        output = probe(name, "--version")
        if output is None:
            failures += 1
        else:
            print(f"PASS {name}: {output.splitlines()[0]}")
    print("INFO Node runs offline DOM fixtures; npm/npx are used by optional package builds.")
    return failures


def windows() -> int:
    if sys.platform != "win32":
        print("FAIL windows profile: run this profile on Windows")
        return 1
    project = ET.parse(ROOT / "windows/CodexTweaks.Windows/CodexTweaks.Windows.csproj")
    target = project.findtext(".//TargetFramework", "")
    framework = re.fullmatch(r"net(\d+)\.\d+-windows(\d+(?:\.\d+){3})", target)
    if framework is None:
        print("FAIL Windows: cannot read .NET and Windows SDK requirements from the project")
        return 1
    failures = 0
    output = probe("dotnet", "--list-sdks")
    installed = re.findall(r"(?m)^(\d+\.\d+\.\d+) \[", output or "")
    matching = [sdk for sdk in installed if version(sdk)[0] == int(framework[1])]
    if matching:
        print(f"PASS .NET SDK: {max(matching, key=version)}")
    else:
        if output is not None:
            print(f"FAIL .NET SDK: stable .NET {framework[1]} SDK is required")
        failures += 1

    roots = {Path(value) for name in ("WindowsSdkDir", "WindowsSdkRoot") if (value := os.getenv(name))}
    if program_files := os.getenv("ProgramFiles(x86)"):
        roots.add(Path(program_files) / "Windows Kits/10")
    import winreg
    for view in (winreg.KEY_WOW64_32KEY, winreg.KEY_WOW64_64KEY):
        try:
            with winreg.OpenKey(
                winreg.HKEY_LOCAL_MACHINE, r"SOFTWARE\Microsoft\Windows Kits\Installed Roots",
                0, winreg.KEY_READ | view,
            ) as key:
                roots.add(Path(winreg.QueryValueEx(key, "KitsRoot10")[0]))
        except OSError:
            continue
    sdks = set()
    for sdk_root in roots:
        try:
            for include in (sdk_root / "Include").glob("*"):
                sdk = include.name
                if not re.fullmatch(r"\d+(?:\.\d+){3}", sdk) or version(sdk) < version(framework[2]):
                    continue
                if ((include / "um/Windows.h").is_file()
                        and (sdk_root / "Lib" / sdk / "um/x64/kernel32.lib").is_file()):
                    sdks.add(sdk)
        except OSError:
            continue
    if sdks:
        print(f"PASS Windows SDK: {max(sdks, key=version)} (requires >= {framework[2]})")
    else:
        print(f"FAIL Windows SDK: need >= {framework[2]} with Windows.h and x64 kernel32.lib")
        failures += 1
    return failures


def macos() -> int:
    if sys.platform != "darwin":
        print("FAIL macos profile: run this profile on macOS")
        return 1
    failures = 0
    for name, arguments in (("xcodebuild", ("-version",)), ("xcodegen", ("--version",))):
        output = probe(name, *arguments)
        if output is None:
            failures += 1
        else:
            print(f"PASS {name}: {output.splitlines()[0]}")
    return failures


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--profile", choices=("core", "windows", "macos"), default="core")
    args = parser.parse_args()
    print(f"Developer preflight ({args.profile}); no installs, builds or tests are run.")
    active_flags = sorted(
        name for name, value in os.environ.items()
        if name.upper().startswith("CODEX_")
        and ("LIVE" in name.upper() or "INTEGRATION" in name.upper()) and value == "1"
    )
    failures = len(active_flags)
    for name in active_flags:
        print(f"FAIL {name}=1: unset live/integration flags before ordinary developer checks")
    if not active_flags:
        print("PASS Live/integration flags: no CODEX_*LIVE* or CODEX_*INTEGRATION* flag is set to 1")
    failures += core()
    if args.profile == "windows":
        failures += windows()
    elif args.profile == "macos":
        failures += macos()
    for name in ("codegraph", "serena"):
        state = "found on PATH" if shutil.which(name) else "not on PATH (MCP availability is not checked)"
        print(f"INFO Optional {name}: {state}")
    if failures:
        print(f"Preflight failed: {failures} requirement(s) need attention; see CONTRIBUTING.md.")
        return 1
    print("Preflight passed. This checks tool readiness, not build success or live-app compatibility.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
