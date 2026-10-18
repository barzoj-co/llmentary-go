#!/usr/bin/env bash
# Reject id-token: write on workflows triggered by pull_request or pull_request_target.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

REPO="barzoj-co/llm-test-lab-go"
SELF_TEST=0
PATHS=()

usage() {
  echo "usage: $0 [--repo owner/name] [--self-test] [path ...]" >&2
  exit 2
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo)
      [[ $# -ge 2 ]] || usage
      REPO="$2"
      shift 2
      ;;
    --self-test)
      SELF_TEST=1
      shift
      ;;
    -h|--help)
      usage
      ;;
    --)
      shift
      PATHS+=("$@")
      break
      ;;
    -*)
      usage
      ;;
    *)
      PATHS+=("$1")
      shift
      ;;
  esac
done

if [[ "${SELF_TEST}" -eq 1 && ${#PATHS[@]} -ne 0 ]]; then
  usage
fi

export REPO SELF_TEST ROOT
python3 - "$ROOT" "$REPO" "$SELF_TEST" "${PATHS[@]+"${PATHS[@]}"}" <<'PY'
from __future__ import annotations

import pathlib
import re
import sys

import yaml

root = pathlib.Path(sys.argv[1])
repo = sys.argv[2]
self_test = sys.argv[3] == "1"
cli_paths = [pathlib.Path(p) for p in sys.argv[4:]]

PR_TRIGGERS = {"pull_request", "pull_request_target"}
REPO_IF = f"github.repository == '{repo}'"


def iter_mapping(node):
    if not isinstance(node, yaml.MappingNode):
        return
    for key_node, value_node in node.value:
        if isinstance(key_node, yaml.ScalarNode):
            yield key_node.value, key_node, value_node


def mapping_get(node, key):
    for name, key_node, value_node in iter_mapping(node):
        if name == key:
            return key_node, value_node
    return None, None


def scalar(node):
    if isinstance(node, yaml.ScalarNode):
        return node.value
    return None


def line_of(node) -> int:
    return int(node.start_mark.line) + 1


def collect_files(paths: list[pathlib.Path]) -> list[pathlib.Path]:
    files: list[pathlib.Path] = []
    for path in paths:
        if path.is_file() and path.suffix in {".yml", ".yaml"}:
            files.append(path)
            continue
        if path.is_dir():
            files.extend(sorted(path.rglob("*.yml")))
            files.extend(sorted(path.rglob("*.yaml")))
            continue
        print(f"not a file or directory: {path}", file=sys.stderr)
        sys.exit(2)
    seen: set[pathlib.Path] = set()
    out: list[pathlib.Path] = []
    for f in files:
        if f not in seen:
            seen.add(f)
            out.append(f)
    return out


def on_node(root_node):
    for name, key_node, value_node in iter_mapping(root_node):
        if name in {"on", "true", "True"} or name is True:
            return key_node, value_node
    return None, None


def trigger_names(on_value) -> tuple[set[str], dict[str, yaml.Node]]:
    names: set[str] = set()
    nodes: dict[str, yaml.Node] = {}
    if isinstance(on_value, yaml.ScalarNode):
        names.add(on_value.value)
        nodes[on_value.value] = on_value
    elif isinstance(on_value, yaml.SequenceNode):
        for item in on_value.value:
            if isinstance(item, yaml.ScalarNode):
                names.add(item.value)
                nodes[item.value] = item
    elif isinstance(on_value, yaml.MappingNode):
        for name, key_node, _ in iter_mapping(on_value):
            names.add(name)
            nodes[name] = key_node
    return names, nodes


def id_token_write_hits(perms_node) -> list[yaml.Node]:
    hits: list[yaml.Node] = []
    if perms_node is None:
        return hits
    if isinstance(perms_node, yaml.ScalarNode):
        if perms_node.value == "write-all":
            hits.append(perms_node)
        return hits
    if isinstance(perms_node, yaml.MappingNode):
        for name, key_node, value_node in iter_mapping(perms_node):
            if name == "id-token" and scalar(value_node) == "write":
                hits.append(key_node)
    return hits


def normalize_if(text: str) -> str:
    text = text.strip()
    if text.startswith("${{") and text.endswith("}}"):
        text = text[3:-2].strip()
    return " ".join(text.split())


def check_file(path: pathlib.Path) -> list[str]:
    errors: list[str] = []
    rel = path.as_posix()
    try:
        rel = path.resolve().relative_to(root.resolve()).as_posix()
    except ValueError:
        rel = str(path)

    root_node = yaml.compose(path.read_text())
    if root_node is None:
        return errors

    _, on_value = on_node(root_node)
    triggers, _ = trigger_names(on_value)
    is_pr = bool(triggers & PR_TRIGGERS)
    is_call = "workflow_call" in triggers

    _, top_perms = mapping_get(root_node, "permissions")
    top_hits = id_token_write_hits(top_perms)

    _, jobs_node = mapping_get(root_node, "jobs")
    jobs: list[tuple[str, yaml.Node]] = []
    if isinstance(jobs_node, yaml.MappingNode):
        jobs = [(job_id, job_node) for job_id, _, job_node in iter_mapping(jobs_node)]

    if is_pr:
        for hit in top_hits:
            errors.append(f"{rel}:{line_of(hit)}: id-token: write is forbidden on pull_request workflows")
        for job_id, job_node in jobs:
            _, job_perms = mapping_get(job_node, "permissions")
            for hit in id_token_write_hits(job_perms):
                errors.append(
                    f"{rel}:{line_of(hit)}: id-token: write is forbidden on pull_request workflows"
                )
        return errors

    if is_call:
        for job_id, job_node in jobs:
            _, job_perms = mapping_get(job_node, "permissions")
            hits = id_token_write_hits(job_perms)
            if not hits and top_hits:
                hits = top_hits
            if not hits:
                continue
            _, if_node = mapping_get(job_node, "if")
            if_text = normalize_if(scalar(if_node) or "")
            if if_text != REPO_IF:
                errors.append(
                    f"{rel}:{line_of(hits[0])}: id-token: write on workflow_call must set "
                    f"if: {REPO_IF}"
                )
    return errors


def report(errors: list[str]) -> int:
    if errors:
        for err in errors:
            print(err, file=sys.stderr)
        return 1
    return 0


def self_test_main() -> int:
    good = root / "scripts" / "testdata" / "pr-oidc" / "good"
    bad = root / "scripts" / "testdata" / "pr-oidc" / "bad"
    failed = 0

    print("== good fixtures (must pass) ==")
    goods = collect_files([good])
    if not goods:
        print(f"missing good fixtures in {good}", file=sys.stderr)
        return 1
    for path in goods:
        print(f"-- {path} --")
        errors = check_file(path)
        if errors:
            print("self-test: good fixture must pass", file=sys.stderr)
            for err in errors:
                print(err, file=sys.stderr)
            failed = 1
        else:
            print("ok")

    print("== bad fixtures (each must fail with file:line: and id-token: write) ==")
    bads = collect_files([bad])
    if not bads:
        print(f"missing bad fixtures in {bad}", file=sys.stderr)
        return 1
    for path in bads:
        print(f"-- {path} --")
        errors = check_file(path)
        text = "\n".join(errors)
        print(text)
        if not errors:
            print(f"self-test: expected failure for {path}", file=sys.stderr)
            failed = 1
            continue
        if not re.search(r":\d+:", text):
            print(f"self-test: expected file:line: in rejection of {path}", file=sys.stderr)
            failed = 1
        if "id-token: write" not in text:
            print(f"self-test: expected 'id-token: write' in rejection of {path}", file=sys.stderr)
            failed = 1

    if failed:
        print("self-test failed", file=sys.stderr)
        return 1
    print("self-test passed")
    return 0


def default_paths() -> list[pathlib.Path]:
    return collect_files([root / ".github" / "workflows"])


if self_test:
    sys.exit(self_test_main())

paths = cli_paths if cli_paths else default_paths()
files = collect_files(paths)
if not files:
    print("no workflow YAML files found", file=sys.stderr)
    sys.exit(1)

all_errors: list[str] = []
for path in files:
    all_errors.extend(check_file(path))

if all_errors:
    print("pr-oidc policy failures:", file=sys.stderr)
    sys.exit(report(all_errors))

print(f"pr-oidc policy OK ({len(files)} files)")
PY
