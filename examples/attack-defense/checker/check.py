#!/usr/bin/env python3
"""Trusted checker harness for the aCTFb Attack+Defense example image.

The image is the only code the worker trusts. Participant repositories are
mounted read-only at /source and /exploit and are treated as untrusted data.
Only the final ACTFB_VERDICT line printed by this harness is authoritative.
"""
import argparse
import json
import os
import subprocess
import sys

PREFIX = "ACTFB_VERDICT:"


def safe_line(line: str) -> str:
    # A participant script must not be able to forge the verdict marker.
    return line.replace(PREFIX, " " + PREFIX)


def emit(line: str) -> None:
    for part in line.replace("\r\n", "\n").split("\n")[:200]:
        print(safe_line(part)[:4000], flush=True)


def parse_selectors(values):
    result = {}
    for raw in values:
        if "=" not in raw:
            continue
        key, selectors = raw.split("=", 1)
        exploit, _, patch = selectors.partition("|")
        result[key] = (exploit, patch)
    return result


def inside(base: str, candidate: str) -> bool:
    base = os.path.realpath(base)
    candidate = os.path.realpath(candidate)
    return candidate == base or candidate.startswith(base + os.sep)


def run_script(script: str, timeout: int):
    path = os.path.join("/exploit", script)
    if not inside("/exploit", path) or not os.path.isfile(path):
        return None, f"selector not found: {script}"
    env = {
        "PATH": "/usr/local/bin:/usr/bin:/bin",
        "HOME": "/tmp",
        "PYTHONPATH": "/exploit",
        "ACTFB_SOURCE": "/source",
        "PYTHONDONTWRITEBYTECODE": "1",
    }
    try:
        completed = subprocess.run(
            [sys.executable, path, "/source"],
            cwd="/exploit",
            env=env,
            capture_output=True,
            text=True,
            timeout=max(1, timeout),
        )
    except subprocess.TimeoutExpired:
        return None, "selector timed out"
    output = (completed.stdout or "") + (completed.stderr or "")
    for line in output.splitlines():
        emit(line)
    return completed, None


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--run-kind", required=True)
    parser.add_argument("--source", default="/source")
    parser.add_argument("--exploit", default="/exploit")
    parser.add_argument("--selector", action="append", default=[])
    args = parser.parse_args()

    timeout = int(os.environ.get("ACTFB_TIMEOUT_SECONDS", "300"))
    selectors = parse_selectors(args.selector)
    results = []
    for key, (exploit_selector, patch_selector) in selectors.items():
        selector = exploit_selector if args.run_kind == "EXPLOIT" else patch_selector
        completed, error = run_script(selector, timeout)
        if error is not None:
            results.append({"key": key, "status": "ERROR", "message": error[:1024]})
            continue
        if completed.returncode != 0:
            results.append({
                "key": key,
                "status": "FAILED",
                "message": f"selector exited {completed.returncode}",
                "securityTests": {"exit": completed.returncode},
                "regressionTests": {},
            })
            continue
        if args.run_kind == "EXPLOIT":
            results.append({"key": key, "status": "PASSED"})
            continue
        security_fixed = False
        regression_passed = False
        for line in reversed((completed.stdout or "").splitlines()):
            try:
                payload = json.loads(line)
            except ValueError:
                continue
            if isinstance(payload, dict) and "securityFixed" in payload:
                security_fixed = payload.get("securityFixed") is True
                regression_passed = payload.get("regressionPassed") is True
                break
        status = "PASSED" if security_fixed and regression_passed else "FAILED"
        results.append({
            "key": key,
            "status": status,
            "securityFixed": security_fixed,
            "regressionPassed": regression_passed,
            "securityTests": {"fixed": 1 if security_fixed else 0},
            "regressionTests": {"passed": 1 if regression_passed else 0},
        })
    print(PREFIX + json.dumps({"vulnerabilities": results}, separators=(",", ":")), flush=True)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())