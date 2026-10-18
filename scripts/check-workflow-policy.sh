#!/usr/bin/env bash
# Least-privilege, timeout-minutes, and concurrency policy for GitHub workflows.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

python3 - "$ROOT" <<'PY'
from __future__ import annotations

import pathlib
import re
import sys

root = pathlib.Path(sys.argv[1])
workflow_dir = root / ".github" / "workflows"
errors: list[str] = []

PR_GROUP = "${{ github.workflow }}-${{ github.event.pull_request.number || github.ref }}"
PR_CANCEL = "${{ github.event_name == 'pull_request' }}"

ALLOWED = {
    "codeql": {"contents": {"read"}, "actions": {"read"}, "security-events": {"write"}},
    "scorecard": {
        "contents": {"read"},
        "actions": {"read"},
        "security-events": {"write"},
        "id-token": {"write"},
    },
    "coverage": {"contents": {"read"}, "id-token": {"write"}},
    "release-please": {
        "contents": {"write"},
        "pull-requests": {"write"},
        "issues": {"write"},
    },
    "labels": {"contents": {"read"}, "issues": {"write"}},
    "labels-dry": {"contents": {"read"}, "issues": {"read"}},
    "default": {"contents": {"read"}},
}

CHECKOUT_SHA = re.compile(
    r"uses:\s+\S*actions/checkout@[0-9a-fA-F]{40}"
)


def job_kind(job_id: str, job_name: str) -> str:
    name = (job_name or job_id).strip()
    if name.startswith("CodeQL"):
        return "codeql"
    if name == "Scorecard":
        return "scorecard"
    if name in {"test", "e2e"}:
        return "coverage"
    if name in {"release-please", "release-please (dry run)"}:
        return "release-please"
    if name == "labels (dry run)":
        return "labels-dry"
    if name == "labels":
        return "labels"
    return "default"


def parse_mapping(lines: list[str], start: int, base_indent: int) -> tuple[dict[str, str], int]:
    """Parse a simple YAML mapping starting at start, indented more than base_indent."""
    mapping: dict[str, str] = {}
    i = start
    while i < len(lines):
        raw = lines[i]
        if not raw.strip() or raw.lstrip().startswith("#"):
            i += 1
            continue
        indent = len(raw) - len(raw.lstrip(" "))
        if indent <= base_indent:
            break
        m = re.match(r"^(\s*)([A-Za-z0-9_. /-]+):\s*(.*)$", raw)
        if not m:
            i += 1
            continue
        key = m.group(2).strip()
        val = m.group(3).strip().strip("'\"")
        mapping[key] = val
        i += 1
    return mapping, i


def parse_workflow(path: pathlib.Path) -> dict:
    text = path.read_text()
    lines = text.splitlines()
    top_keys: dict[str, int] = {}
    i = 0
    while i < len(lines):
        raw = lines[i]
        if not raw.strip() or raw.startswith("#") or raw.startswith(" "):
            i += 1
            continue
        m = re.match(r"^([A-Za-z0-9_-]+):\s*(.*)$", raw)
        if m:
            top_keys[m.group(1)] = i
        i += 1
    return {"text": text, "lines": lines, "top": top_keys}


def top_level_block(lines: list[str], start: int) -> list[str]:
    block = [lines[start]]
    i = start + 1
    while i < len(lines):
        raw = lines[i]
        if raw.strip() and not raw.startswith(" ") and not raw.startswith("#") and not raw.startswith("-"):
            # next top-level key
            if re.match(r"^[A-Za-z0-9_-]+:", raw):
                break
        block.append(raw)
        i += 1
    return block


def parse_jobs(lines: list[str], jobs_idx: int) -> dict[str, dict]:
    jobs: dict[str, dict] = {}
    i = jobs_idx + 1
    current: str | None = None
    current_indent = None
    acc_start = None
    def flush():
        nonlocal current, acc_start
        if current is None:
            return
        jobs[current]["end"] = i
        body = lines[acc_start:i]
        jobs[current]["body"] = body
        name = None
        timeout = None
        perms = None
        for j, raw in enumerate(body):
            if re.match(r"^\s{4}name:\s*", raw):
                name = raw.split(":", 1)[1].strip().strip("'\"")
            if re.match(r"^\s{4}timeout-minutes:\s*", raw):
                timeout = raw.split(":", 1)[1].strip()
            if re.match(r"^\s{4}permissions:\s*$", raw):
                perms, _ = parse_mapping(body, j + 1, 4)
            elif re.match(r"^\s{4}permissions:\s*\{\s*\}\s*$", raw):
                perms = {}
            elif re.match(r"^\s{4}permissions:\s+\S", raw):
                rest = raw.split(":", 1)[1].strip()
                perms = {"_scalar": rest}
        jobs[current]["name"] = name
        jobs[current]["timeout"] = timeout
        jobs[current]["permissions"] = perms
        jobs[current]["raw"] = "\n".join(body)

    while i < len(lines):
        raw = lines[i]
        if raw.strip() and not raw.startswith(" ") and not raw.startswith("#"):
            if re.match(r"^[A-Za-z0-9_-]+:", raw):
                break
        m = re.match(r"^  ([A-Za-z0-9_.-]+):\s*$", raw)
        if m:
            flush()
            current = m.group(1)
            jobs[current] = {"id": current, "start": i}
            acc_start = i + 1
        i += 1
    flush()
    return jobs


def has_pr_trigger(text: str) -> bool:
    # 'on' can be parsed loosely; look for a pull_request key in the trigger block.
    return bool(re.search(r"(?m)^on:\s*$", text)) and bool(
        re.search(r"(?m)^\s+pull_request:\s*$|^(on:\s*\n(?:.*\n)*?\s+)pull_request\b|^on:\s*\[[^\]]*pull_request", text)
    ) or bool(re.search(r"(?m)^  pull_request:\s*(#.*)?$", text)) or bool(
        re.search(r"(?m)^on:\s*\n(?:  .+\n)*  pull_request:", text)
    )


def check_file(path: pathlib.Path) -> None:
    parsed = parse_workflow(path)
    lines = parsed["lines"]
    text = parsed["text"]
    top = parsed["top"]
    rel = path.relative_to(root).as_posix()

    if "permissions" not in top:
        errors.append(f"{rel}: missing top-level permissions")
        return

    perm_line = lines[top["permissions"]]
    if re.match(r"^permissions:\s*\{\s*\}\s*$", perm_line):
        pass
    elif re.match(r"^permissions:\s*$", perm_line):
        mapping, _ = parse_mapping(lines, top["permissions"] + 1, 0)
        extra = {k: v for k, v in mapping.items() if not (k == "contents" and v == "read")}
        if extra:
            errors.append(f"{rel}: top-level permissions must be contents: read or {{}}: {mapping}")
        if "contents" not in mapping and mapping:
            errors.append(f"{rel}: top-level permissions must be contents: read or {{}}")
        if mapping == {}:
            pass
        elif mapping.get("contents") != "read" or len(mapping) != 1:
            # already reported extra; ensure contents:read-only
            if mapping != {"contents": "read"}:
                errors.append(f"{rel}: top-level permissions limited to contents: read, got {mapping}")
    else:
        errors.append(f"{rel}: top-level permissions must be a mapping limited to contents: read or {{}}")

    if "jobs" not in top:
        errors.append(f"{rel}: missing jobs")
        return

    jobs = parse_jobs(lines, top["jobs"])
    if not jobs:
        errors.append(f"{rel}: no jobs parsed")
        return

    for job_id, job in jobs.items():
        kind = job_kind(job_id, job.get("name") or "")
        allowed = ALLOWED[kind]
        if not job.get("timeout"):
            errors.append(f"{rel}: job {job_id!r} missing timeout-minutes")
        perms = job.get("permissions")
        if perms is None:
            if kind != "default":
                # Jobs that need extra permissions must declare them (GitHub
                # does not merge job permissions with the workflow defaults).
                errors.append(
                    f"{rel}: job {job_id!r} ({kind}) must declare permissions {sorted(allowed)}"
                )
        else:
            if "_scalar" in perms:
                errors.append(f"{rel}: job {job_id!r} uses scalar permissions {perms['_scalar']!r}")
                continue
            for key, val in perms.items():
                if key not in allowed or val not in allowed[key]:
                    errors.append(
                        f"{rel}: job {job_id!r} grants {key}: {val} (not allowed for {kind})"
                    )

        # persist-credentials: false on every checkout in this job
        raw = job.get("raw") or ""
        for m in re.finditer(r"uses:\s+\S*actions/checkout@[0-9a-fA-F]{40}.*", raw):
            # look ahead in the job raw from this match
            after = raw[m.end() :]
            # next step starts at '\n      - ' or end
            nxt = re.search(r"\n\s+-\s+", after)
            block = after if nxt is None else after[: nxt.start()]
            if "persist-credentials: false" not in block:
                errors.append(
                    f"{rel}: job {job_id!r} checkout is missing persist-credentials: false"
                )

    is_release = path.name == "release-please.yml"
    if is_release:
        if "concurrency:" not in text:
            errors.append(f"{rel}: missing concurrency")
        elif not re.search(r"cancel-in-progress:\s*false", text):
            errors.append(f"{rel}: release-please concurrency must set cancel-in-progress: false")
        elif PR_GROUP in text:
            errors.append(f"{rel}: release-please must use a fixed concurrency group")
        return

    if has_pr_trigger(text):
        if "concurrency:" not in text:
            errors.append(f"{rel}: PR-triggered workflow missing concurrency")
        else:
            if PR_GROUP not in text:
                errors.append(
                    f"{rel}: concurrency.group must be {PR_GROUP}"
                )
            if f"cancel-in-progress: {PR_CANCEL}" not in text:
                errors.append(
                    f"{rel}: concurrency.cancel-in-progress must be {PR_CANCEL}"
                )


files = sorted(workflow_dir.glob("*.yml")) + sorted(workflow_dir.glob("*.yaml"))
if not files:
    print("no workflows found", file=sys.stderr)
    sys.exit(1)

for path in files:
    check_file(path)

if errors:
    print("workflow policy failures:", file=sys.stderr)
    for e in errors:
        print(f"  - {e}", file=sys.stderr)
    sys.exit(1)

print(f"workflow policy OK ({len(files)} workflows)")
PY
