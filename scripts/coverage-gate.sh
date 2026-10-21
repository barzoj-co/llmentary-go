#!/usr/bin/env bash
# Merge unit + integration coverprofiles, apply codecov.yml ignores, fail below
# the ratchet in .coverage-threshold (90 on release-please--* branches).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

UNIT_PROFILE="${UNIT_PROFILE:-coverage-unit.out}"
INTEGRATION_PROFILE="${INTEGRATION_PROFILE:-coverage-integration.out}"
THRESHOLD_FILE="${THRESHOLD_FILE:-.coverage-threshold}"
CODECOV_YML="${CODECOV_YML:-codecov.yml}"
REQUIRE_INTEGRATION="${REQUIRE_INTEGRATION:-1}"
RELEASE_THRESHOLD=90

usage() {
  echo "usage: $0 [--self-test]" >&2
  exit 2
}

SELF_TEST=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --self-test)
      SELF_TEST=1
      shift
      ;;
    -h|--help)
      usage
      ;;
    *)
      usage
      ;;
  esac
done

head_branch() {
  if [[ -n "${GITHUB_HEAD_REF:-}" ]]; then
    echo "${GITHUB_HEAD_REF}"
    return
  fi
  echo "${GITHUB_REF_NAME:-}"
}

run_gate() {
  python3 - "$ROOT" "$UNIT_PROFILE" "$INTEGRATION_PROFILE" "$THRESHOLD_FILE" \
    "$CODECOV_YML" "$REQUIRE_INTEGRATION" "$(head_branch)" "$RELEASE_THRESHOLD" <<'PY'
from __future__ import annotations

import math
import pathlib
import re
import sys

root = pathlib.Path(sys.argv[1])
unit_path = pathlib.Path(sys.argv[2])
integration_path = pathlib.Path(sys.argv[3])
threshold_file = pathlib.Path(sys.argv[4])
codecov_yml = pathlib.Path(sys.argv[5])
require_integration = sys.argv[6] == "1"
branch = sys.argv[7]
release_threshold = float(sys.argv[8])

PROFILE_LINE = re.compile(
    r"^(?P<file>\S+):(?P<span>\d+\.\d+,\d+\.\d+) (?P<stmts>\d+) (?P<count>\d+)\s*$"
)


def die(msg: str, code: int = 1) -> None:
    print(f"::error::{msg}", file=sys.stderr)
    print(msg, file=sys.stderr)
    raise SystemExit(code)


def read_ignores(path: pathlib.Path) -> list[str]:
    if not path.is_file():
        die(f"missing {path}")
    ignores: list[str] = []
    in_ignore = False
    for raw in path.read_text().splitlines():
        if re.match(r"^ignore:\s*$", raw):
            in_ignore = True
            continue
        if in_ignore:
            m = re.match(r"^\s+-\s+[\"']?([^\"']+)[\"']?\s*$", raw)
            if m:
                ignores.append(m.group(1).strip())
                continue
            if raw.strip() == "" or raw.startswith(" ") or raw.startswith("\t"):
                if raw.strip() == "":
                    continue
                break
            if not raw.startswith(" ") and not raw.startswith("\t"):
                break
    return ignores


def ignored(file_path: str, patterns: list[str]) -> bool:
    norm = file_path.replace("\\", "/")
    for pat in patterns:
        pat = pat.replace("\\", "/").strip()
        if pat.endswith("/**"):
            prefix = pat[:-3].rstrip("/")
            if (
                f"/{prefix}/" in f"/{norm}/"
                or norm.startswith(prefix + "/")
                or f"/{prefix}/" in norm
            ):
                return True
        elif pat.endswith("/*"):
            prefix = pat[:-2].rstrip("/")
            if f"/{prefix}/" in f"/{norm}/" or norm.startswith(prefix + "/"):
                return True
        elif norm == pat or norm.endswith("/" + pat):
            return True
    return False


def load_profile(path: pathlib.Path) -> list[tuple[str, str, int, int]]:
    if not path.is_file():
        return []
    rows: list[tuple[str, str, int, int]] = []
    for raw in path.read_text().splitlines():
        if raw.startswith("mode:"):
            continue
        m = PROFILE_LINE.match(raw)
        if not m:
            continue
        rows.append(
            (
                m.group("file"),
                m.group("span"),
                int(m.group("stmts")),
                int(m.group("count")),
            )
        )
    return rows


def merge_percent(unit: pathlib.Path, integration: pathlib.Path, ignores: list[str]) -> float:
    blocks: dict[tuple[str, str], tuple[int, int]] = {}
    for src in (unit, integration):
        for file_path, span, stmts, count in load_profile(src):
            if ignored(file_path, ignores):
                continue
            key = (file_path, span)
            prev = blocks.get(key)
            if prev is None:
                blocks[key] = (stmts, count)
            else:
                blocks[key] = (prev[0], max(prev[1], count))
    total = sum(stmts for stmts, _ in blocks.values())
    covered = sum(stmts for stmts, count in blocks.values() if count > 0)
    if total == 0:
        die("merged coverage profile is empty")
    return 100.0 * covered / total


unit = unit_path if unit_path.is_absolute() else root / unit_path
integration = integration_path if integration_path.is_absolute() else root / integration_path
threshold_path = threshold_file if threshold_file.is_absolute() else root / threshold_file
codecov_path = codecov_yml if codecov_yml.is_absolute() else root / codecov_yml

if not unit.is_file():
    die(f"missing unit coverprofile: {unit}")
if require_integration and not integration.is_file():
    die(f"missing integration coverprofile: {integration}")

if branch.startswith("release-please--"):
    threshold = release_threshold
    source = "release-please branch"
else:
    if not threshold_path.is_file():
        die(f"missing threshold file: {threshold_path}")
    raw = threshold_path.read_text().strip()
    try:
        threshold = float(raw)
    except ValueError:
        die(f"invalid threshold in {threshold_path}: {raw}")
    if not math.isfinite(threshold):
        die(f"invalid threshold in {threshold_path}: {raw}")
    if threshold_path.is_relative_to(root):
        source = str(threshold_path.relative_to(root))
    else:
        source = threshold_path.name

ignores = read_ignores(codecov_path)
percent = merge_percent(unit, integration, ignores)
print(f"coverage: {percent:.1f}% (threshold {threshold:g} from {source})")
if percent + 1e-9 < threshold:
    die(f"coverage {percent:.1f}% is below threshold {threshold:g}")
PY
}

run_case() {
  local name="$1"
  local expect_rc="$2"
  local expect_msg="$3"
  shift 3
  local rc=0
  local out
  out="$("$@" 2>&1)" || rc=$?
  printf -- '-- %s (rc=%s) --\n' "${name}" "${rc}"
  printf '%s\n' "${out}"
  if [[ "${rc}" -ne "${expect_rc}" ]]; then
    echo "self-test ${name}: expected exit ${expect_rc}, got ${rc}" >&2
    return 1
  fi
  if [[ -n "${expect_msg}" ]] && ! grep -Fq "${expect_msg}" <<<"${out}"; then
    echo "self-test ${name}: expected message: ${expect_msg}" >&2
    return 1
  fi
  return 0
}

self_test() {
  local tmp failed=0
  tmp="$(mktemp -d)"
  trap 'rm -rf "${tmp}"' RETURN

  cat >"${tmp}/codecov.yml" <<'EOF'
ignore:
  - "examples/**"
  - "e2e/**"
EOF
  echo 50 >"${tmp}/.coverage-threshold"

  cat >"${tmp}/coverage-unit.out" <<'EOF'
mode: atomic
github.com/barzoj-co/llmentary-go/client.go:1.1,2.2 8 1
github.com/barzoj-co/llmentary-go/e2e/skip.go:1.1,2.2 50 1
github.com/barzoj-co/llmentary-go/miss.go:1.1,2.2 2 0
EOF
  cat >"${tmp}/coverage-integration.out" <<'EOF'
mode: atomic
github.com/barzoj-co/llmentary-go/client.go:1.1,2.2 8 0
github.com/barzoj-co/llmentary-go/miss.go:1.1,2.2 2 1
github.com/barzoj-co/llmentary-go/examples/go/demo.go:1.1,2.2 40 1
EOF

  run_case merge-pass 0 "coverage: 100.0% (threshold 50 from .coverage-threshold)" \
    env UNIT_PROFILE="${tmp}/coverage-unit.out" \
    INTEGRATION_PROFILE="${tmp}/coverage-integration.out" \
    THRESHOLD_FILE="${tmp}/.coverage-threshold" \
    CODECOV_YML="${tmp}/codecov.yml" \
    GITHUB_HEAD_REF="" GITHUB_REF_NAME="cursor/coverage-gate-340a" \
    bash "${ROOT}/scripts/coverage-gate.sh" || failed=1

  echo 101 >"${tmp}/.coverage-threshold"
  run_case below-threshold 1 "coverage 100.0% is below threshold 101" \
    env UNIT_PROFILE="${tmp}/coverage-unit.out" \
    INTEGRATION_PROFILE="${tmp}/coverage-integration.out" \
    THRESHOLD_FILE="${tmp}/.coverage-threshold" \
    CODECOV_YML="${tmp}/codecov.yml" \
    GITHUB_HEAD_REF="" GITHUB_REF_NAME="main" \
    bash "${ROOT}/scripts/coverage-gate.sh" || failed=1

  echo 50 >"${tmp}/.coverage-threshold"
  cat >"${tmp}/coverage-low.out" <<'EOF'
mode: atomic
github.com/barzoj-co/llmentary-go/client.go:1.1,2.2 8 1
github.com/barzoj-co/llmentary-go/miss.go:1.1,2.2 2 0
EOF
  run_case release-please-90 1 "threshold 90 from release-please branch" \
    env UNIT_PROFILE="${tmp}/coverage-low.out" \
    INTEGRATION_PROFILE="${tmp}/coverage-low.out" \
    THRESHOLD_FILE="${tmp}/.coverage-threshold" \
    CODECOV_YML="${tmp}/codecov.yml" \
    GITHUB_HEAD_REF="release-please--branches--main" GITHUB_REF_NAME="main" \
    bash "${ROOT}/scripts/coverage-gate.sh" || failed=1

  run_case release-please-ref-name 1 "threshold 90 from release-please branch" \
    env UNIT_PROFILE="${tmp}/coverage-low.out" \
    INTEGRATION_PROFILE="${tmp}/coverage-low.out" \
    THRESHOLD_FILE="${tmp}/.coverage-threshold" \
    CODECOV_YML="${tmp}/codecov.yml" \
    GITHUB_HEAD_REF="" GITHUB_REF_NAME="release-please--branches--main" \
    bash "${ROOT}/scripts/coverage-gate.sh" || failed=1

  run_case missing-integration 1 "missing integration coverprofile" \
    env UNIT_PROFILE="${tmp}/coverage-unit.out" \
    INTEGRATION_PROFILE="${tmp}/no-such.out" \
    THRESHOLD_FILE="${tmp}/.coverage-threshold" \
    CODECOV_YML="${tmp}/codecov.yml" \
    GITHUB_HEAD_REF="" GITHUB_REF_NAME="main" \
    bash "${ROOT}/scripts/coverage-gate.sh" || failed=1

  run_case missing-unit 1 "missing unit coverprofile" \
    env UNIT_PROFILE="${tmp}/no-unit.out" \
    INTEGRATION_PROFILE="${tmp}/coverage-integration.out" \
    THRESHOLD_FILE="${tmp}/.coverage-threshold" \
    CODECOV_YML="${tmp}/codecov.yml" \
    GITHUB_HEAD_REF="" GITHUB_REF_NAME="main" \
    bash "${ROOT}/scripts/coverage-gate.sh" || failed=1

  printf 'NaN\n' >"${tmp}/.coverage-threshold"
  run_case nan-threshold 1 "invalid threshold" \
    env UNIT_PROFILE="${tmp}/coverage-unit.out" \
    INTEGRATION_PROFILE="${tmp}/coverage-integration.out" \
    THRESHOLD_FILE="${tmp}/.coverage-threshold" \
    CODECOV_YML="${tmp}/codecov.yml" \
    GITHUB_HEAD_REF="" GITHUB_REF_NAME="main" \
    bash "${ROOT}/scripts/coverage-gate.sh" || failed=1

  printf 'Inf\n' >"${tmp}/.coverage-threshold"
  run_case inf-threshold 1 "invalid threshold" \
    env UNIT_PROFILE="${tmp}/coverage-unit.out" \
    INTEGRATION_PROFILE="${tmp}/coverage-integration.out" \
    THRESHOLD_FILE="${tmp}/.coverage-threshold" \
    CODECOV_YML="${tmp}/codecov.yml" \
    GITHUB_HEAD_REF="" GITHUB_REF_NAME="main" \
    bash "${ROOT}/scripts/coverage-gate.sh" || failed=1

  if [[ "${failed}" -ne 0 ]]; then
    echo "coverage-gate self-test failed" >&2
    return 1
  fi
  echo "coverage-gate self-test OK"
}

if [[ "${SELF_TEST}" -eq 1 ]]; then
  self_test
else
  run_gate
fi
