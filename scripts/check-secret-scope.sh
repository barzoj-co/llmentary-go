#!/usr/bin/env bash
# Reject workflow/job-level secrets, secrets in run:/with:, and install-step secrets.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

E2E_JOB="e2e"
E2E_ENVIRONMENT="openai-e2e"
OPENAI_SECRET="OPENAI_API_KEY"
SELF_TEST=0
PATHS=()

usage() {
  echo "usage: $0 [--e2e-job ID] [--e2e-environment NAME] [--openai-secret NAME] [--self-test] [path ...]" >&2
  exit 2
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --e2e-job)
      [[ $# -ge 2 ]] || usage
      E2E_JOB="$2"
      shift 2
      ;;
    --e2e-environment)
      [[ $# -ge 2 ]] || usage
      E2E_ENVIRONMENT="$2"
      shift 2
      ;;
    --openai-secret)
      [[ $# -ge 2 ]] || usage
      OPENAI_SECRET="$2"
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

export E2E_JOB E2E_ENVIRONMENT OPENAI_SECRET SELF_TEST ROOT
python3 - "$ROOT" "$E2E_JOB" "$E2E_ENVIRONMENT" "$OPENAI_SECRET" "$SELF_TEST" "${PATHS[@]+"${PATHS[@]}"}" <<'PY'
from __future__ import annotations

import pathlib
import re
import sys

import yaml

root = pathlib.Path(sys.argv[1])
e2e_job = sys.argv[2]
e2e_environment = sys.argv[3]
openai_secret = sys.argv[4]
self_test = sys.argv[5] == "1"
cli_paths = [pathlib.Path(p) for p in sys.argv[6:]]

INSTALL_NEEDLES = (
    "npm ci",
    "npm install",
    "npm i ",
    "pnpm install",
    "yarn install",
    "pip install",
    "python -m pip install",
    "uv sync",
    "uv pip install",
    "poetry install",
    "go mod download",
    "go get",
)

RAW_SECRET = re.compile(
    r"\$\{\{\s*(?:secrets(?:\.([A-Za-z0-9_]+)|\[\s*['\"]([A-Za-z0-9_]+)['\"]\s*\])|github\.token)\s*\}\}"
)
BOOL_SECRET = re.compile(
    r"""\$\{\{\s*secrets(?:\.[A-Za-z0-9_]+|\[\s*['"][A-Za-z0-9_]+['"]\s*\])\s*(?:!=|==)\s*(?:''|"")\s*\}\}"""
)
RUN_SECRET = re.compile(
    r"\$\{\{[^}]*\b(?:secrets\b|github\.token)"
)
ANY_SECRET_REF = re.compile(r"\$\{\{[^}]*\bsecrets\b")
WHOLE_SECRETS = re.compile(
    r"\$\{\{[^}]*(?:toJSON\s*\(\s*secrets\s*\)|(?<![\w.])secrets\s*\}\})"
)
TOKEN_EXEMPT = {"GITHUB_TOKEN", "github.token"}
BANNED_SECRETS = {"LLMTESTLAB_REPO_TOKEN"}


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
    # unique, keep order
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


def trigger_has_pull_request_target(on_value):
    hits = []
    if isinstance(on_value, yaml.ScalarNode) and on_value.value == "pull_request_target":
        hits.append(on_value)
    elif isinstance(on_value, yaml.SequenceNode):
        for item in on_value.value:
            if isinstance(item, yaml.ScalarNode) and item.value == "pull_request_target":
                hits.append(item)
    elif isinstance(on_value, yaml.MappingNode):
        for name, key_node, _ in iter_mapping(on_value):
            if name == "pull_request_target":
                hits.append(key_node)
    return hits


def walk_strings(node):
    if isinstance(node, yaml.ScalarNode):
        yield node
    elif isinstance(node, yaml.SequenceNode):
        for item in node.value:
            yield from walk_strings(item)
    elif isinstance(node, yaml.MappingNode):
        for _, _, value_node in iter_mapping(node):
            yield from walk_strings(value_node)


def raw_secrets_in(text: str) -> list[str]:
    if text is None:
        return []
    names = []
    for match in RAW_SECRET.finditer(text):
        names.append(match.group(1) or match.group(2) or "github.token")
    return names


def is_boolean_secret(text: str) -> bool:
    return bool(BOOL_SECRET.fullmatch(text.strip())) if text else False


def contains_secret_ref(text: str) -> bool:
    if not text or is_boolean_secret(text):
        return False
    if ANY_SECRET_REF.search(text):
        return True
    return "${{" in text and "github.token" in text


def is_checkout_uses(value: str | None) -> bool:
    return bool(value) and value.startswith("actions/checkout@")


def job_environment(job_node):
    _, env_node = mapping_get(job_node, "environment")
    if env_node is None:
        return None, None
    if isinstance(env_node, yaml.ScalarNode):
        return env_node.value, env_node
    if isinstance(env_node, yaml.MappingNode):
        _, name_node = mapping_get(env_node, "name")
        if name_node is not None:
            return scalar(name_node), name_node
    return None, env_node


def openai_secret_hits(job_node):
    dotted = f"secrets.{openai_secret}"
    bracket = re.compile(
        rf"secrets\[\s*['\"]{re.escape(openai_secret)}['\"]\s*\]"
    )
    hits = []
    for s in walk_strings(job_node):
        val = s.value or ""
        if dotted in val or bracket.search(val) or WHOLE_SECRETS.search(val):
            hits.append(s)
    return hits


def job_needs_list(needs_node) -> list[str]:
    if needs_node is None:
        return []
    if isinstance(needs_node, yaml.ScalarNode):
        return [needs_node.value]
    if isinstance(needs_node, yaml.SequenceNode):
        return [scalar(item) for item in needs_node.value if scalar(item) is not None]
    return []


def check_file(path: pathlib.Path) -> list[str]:
    errors: list[str] = []
    rel = path.as_posix()
    try:
        rel = path.resolve().relative_to(root.resolve()).as_posix()
    except ValueError:
        rel = str(path)

    text = path.read_text()
    root_node = yaml.compose(text)
    if root_node is None:
        errors.append(f"{rel}:1: missing or malformed e2e-gate job")
        return errors

    _, on_value = on_node(root_node)
    if on_value is not None:
        for hit in trigger_has_pull_request_target(on_value):
            errors.append(f"{rel}:{line_of(hit)}: pull_request_target is forbidden")

    _, env_node = mapping_get(root_node, "env")
    if env_node is not None:
        for s in walk_strings(env_node):
            if contains_secret_ref(s.value):
                errors.append(f"{rel}:{line_of(s)}: secret in workflow-level env")

    _, jobs_node = mapping_get(root_node, "jobs")
    jobs: dict[str, yaml.Node] = {}
    if isinstance(jobs_node, yaml.MappingNode):
        for job_id, _, job_node in iter_mapping(jobs_node):
            jobs[job_id] = job_node
            check_job(rel, job_id, job_node, errors)

    for s in walk_strings(root_node):
        val = s.value or ""
        for name in BANNED_SECRETS:
            if name in val:
                errors.append(f"{rel}:{line_of(s)}: {name} is not allowed")

    if path.name in {"ci.yml", "ci.yaml"}:
        gate = jobs.get("e2e-gate")
        ok = False
        if gate is not None:
            _, if_node = mapping_get(gate, "if")
            _, needs_node = mapping_get(gate, "needs")
            if_text = scalar(if_node) or ""
            needs = job_needs_list(needs_node)
            normalized_if = "".join(if_text.split())
            if normalized_if in {"always()", "${{always()}}"} and e2e_job in needs:
                ok = True
        if not ok:
            line = 1
            if gate is not None:
                line = line_of(gate)
            errors.append(f"{rel}:{line}: missing or malformed e2e-gate job")

        live = jobs.get(e2e_job)
        env_name, env_node = job_environment(live) if live is not None else (None, None)
        if env_name != e2e_environment:
            line = 1
            if env_node is not None:
                line = line_of(env_node)
            elif live is not None:
                line = line_of(live)
            errors.append(
                f"{rel}:{line}: missing or malformed environment {e2e_environment} on job {e2e_job}"
            )

    return errors


def check_job(rel: str, job_id: str, job_node, errors: list[str]) -> None:
    env_name, env_node = job_environment(job_node)
    if job_id != e2e_job:
        if env_name == e2e_environment:
            errors.append(
                f"{rel}:{line_of(env_node)}: environment {e2e_environment} is reserved for job {e2e_job}"
            )
        for hit in openai_secret_hits(job_node):
            errors.append(
                f"{rel}:{line_of(hit)}: {openai_secret} is reserved for job {e2e_job}"
            )
    elif (
        env_name != e2e_environment
        and openai_secret_hits(job_node)
        and not rel.endswith("ci.yml")
        and not rel.endswith("ci.yaml")
    ):
        line = line_of(env_node) if env_node is not None else line_of(job_node)
        errors.append(
            f"{rel}:{line}: missing or malformed environment {e2e_environment} on job {e2e_job}"
        )

    _, secrets_node = mapping_get(job_node, "secrets")
    if secrets_node is not None:
        if isinstance(secrets_node, yaml.ScalarNode) and secrets_node.value == "inherit":
            errors.append(
                f"{rel}:{line_of(secrets_node)}: secrets: inherit is forbidden; pass named secrets"
            )

    _, env_node = mapping_get(job_node, "env")
    if env_node is not None:
        for s in walk_strings(env_node):
            if contains_secret_ref(s.value):
                errors.append(f"{rel}:{line_of(s)}: secret in job-level env")

    _, steps_node = mapping_get(job_node, "steps")
    raw_uses: dict[str, list[int]] = {}
    if isinstance(steps_node, yaml.SequenceNode):
        for step in steps_node.value:
            if not isinstance(step, yaml.MappingNode):
                continue
            check_step(rel, job_id, step, errors, raw_uses)

    for name, lines in raw_uses.items():
        if name in TOKEN_EXEMPT:
            continue
        if len(set(lines)) > 1:
            errors.append(
                f"{rel}:{lines[1]}: secret {name} used raw in more than one step of job {job_id}; "
                "presence checks must use the boolean form"
            )


def check_step(rel, job_id, step, errors, raw_uses) -> None:
    _, uses_node = mapping_get(step, "uses")
    uses = scalar(uses_node)
    _, run_node = mapping_get(step, "run")
    run = scalar(run_node) or ""
    _, with_node = mapping_get(step, "with")
    _, env_node = mapping_get(step, "env")

    if run_node is not None and RUN_SECRET.search(run):
        errors.append(
            f"{rel}:{line_of(run_node)}: secret interpolated into run:; pass it via step env"
        )

    if with_node is not None:
        for key, key_node, value_node in iter_mapping(with_node):
            for s in walk_strings(value_node):
                secrets = raw_secrets_in(s.value)
                if not secrets and not contains_secret_ref(s.value):
                    continue
                if key == "token" and is_checkout_uses(uses):
                    for name in secrets or ["<secret>"]:
                        raw_uses.setdefault(name, []).append(line_of(s))
                    continue
                errors.append(f"{rel}:{line_of(s)}: secret in with:")

    step_raw: list[tuple[str, int]] = []
    if env_node is not None:
        for s in walk_strings(env_node):
            if is_boolean_secret(s.value):
                continue
            names = raw_secrets_in(s.value)
            if not names and contains_secret_ref(s.value):
                names = ["<secret>"]
            for name in names:
                step_raw.append((name, line_of(s)))
                raw_uses.setdefault(name, []).append(line_of(s))

    if step_raw and run:
        if any(needle in run for needle in INSTALL_NEEDLES):
            errors.append(f"{rel}:{step_raw[0][1]}: secret on an install step")


def report(errors: list[str]) -> int:
    if errors:
        for err in errors:
            print(err, file=sys.stderr)
        return 1
    return 0


def expect_line(path: pathlib.Path) -> str:
    first = path.read_text().splitlines()[0]
    prefix = "# expect: "
    if not first.startswith(prefix):
        raise SystemExit(f"{path}: first line must be '{prefix}<substring>'")
    return first[len(prefix) :]


def self_test_main() -> int:
    good = root / "scripts" / "testdata" / "secret-scope" / "good"
    bad = root / "scripts" / "testdata" / "secret-scope" / "bad"
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

    print("== bad fixtures (each must fail with file:line: and expected reason) ==")
    bads = collect_files([bad])
    if not bads:
        print(f"missing bad fixtures in {bad}", file=sys.stderr)
        return 1
    for path in bads:
        print(f"-- {path} --")
        expect = expect_line(path)
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
        if expect not in text:
            print(f"self-test: expected reason {expect!r} in rejection of {path}", file=sys.stderr)
            failed = 1

    if failed:
        print("self-test failed", file=sys.stderr)
        return 1
    print("self-test passed")
    return 0


def default_paths() -> list[pathlib.Path]:
    workflow_dir = root / ".github" / "workflows"
    return collect_files([workflow_dir])


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
    print("secret-scope policy failures:", file=sys.stderr)
    sys.exit(report(all_errors))

print(f"secret-scope policy OK ({len(files)} files)")
PY
