#!/usr/bin/env bash
# Verify GitHub Actions 'uses:' pins: 40-char SHA + version comment, local, or
# docker digest. Default scan: .github/workflows and .github/actions.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

SHA40='[0-9a-fA-F]{40}'
SHA256='[0-9a-fA-F]{64}'
# owner/repo or owner/repo/path (codeql-action/init, etc.)
ACTION_REF='[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*'

usage() {
  echo "usage: $0 [--self-test] [dir ...]" >&2
  exit 2
}

is_comment_line() {
  [[ "$1" =~ ^[[:space:]]*# ]]
}

# Prints "file:line: uses-value" for every uses: line (comments skipped).
collect_uses() {
  local path="$1"
  local line value
  local n=0
  while IFS= read -r line || [[ -n "$line" ]]; do
    n=$((n + 1))
    if is_comment_line "$line"; then
      continue
    fi
    if [[ "$line" =~ ^[[:space:]]*(-[[:space:]]+)?uses:[[:space:]]*(.+)$ ]]; then
      value="${BASH_REMATCH[2]}"
      value="${value#"${value%%[![:space:]]*}"}"
      value="${value%"${value##*[![:space:]]}"}"
      value="${value#\'}"
      value="${value%\'}"
      value="${value#\"}"
      value="${value%\"}"
      printf '%s:%s: %s\n' "$path" "$n" "$value"
    fi
  done <"$path"
}

valid_uses() {
  local value="$1"
  local action comment

  if [[ "$value" == ./* ]]; then
    return 0
  fi

  if [[ "$value" =~ ^docker://[^[:space:]]+@sha256:${SHA256}([[:space:]]+#.*)?$ ]]; then
    return 0
  fi

  if [[ "$value" =~ ^(${ACTION_REF})@(${SHA40})([[:space:]]+#[[:space:]]*v[^[:space:]]+.*)$ ]]; then
    return 0
  fi

  return 1
}

scan_paths() {
  local -a files=()
  local p f
  local bad=0
  local found=0

  if [[ $# -eq 0 ]]; then
    echo "scan_paths: no paths" >&2
    return 2
  fi

  for p in "$@"; do
    if [[ -f "$p" ]]; then
      files+=("$p")
      continue
    fi
    if [[ ! -d "$p" ]]; then
      echo "not a file or directory: $p" >&2
      return 2
    fi
    while IFS= read -r f; do
      files+=("$f")
    done < <(find "$p" \( -name '*.yml' -o -name '*.yaml' \) -type f | sort)
  done

  if [[ ${#files[@]} -eq 0 ]]; then
    echo "no workflow YAML files under: $*" >&2
    return 1
  fi

  local rec path_line value
  for f in "${files[@]}"; do
    while IFS= read -r rec; do
      found=$((found + 1))
      path_line="${rec%%: *}"
      value="${rec#*: }"
      # rec is "file:line: value" but file may contain colons. We formatted
      # 'path:n: value' with a space after the second colon.
      path_line="${rec%%: *}"
      value="${rec#*: }"
      if ! valid_uses "$value"; then
        echo "${path_line}: unpinned or malformed uses: ${value}" >&2
        bad=$((bad + 1))
      fi
    done < <(collect_uses "$f")
  done

  if [[ "$found" -eq 0 ]]; then
    echo "no uses: entries found under: $*" >&2
    return 1
  fi

  if [[ "$bad" -gt 0 ]]; then
    echo "pinned-actions: ${bad} offending uses:" >&2
    return 1
  fi
  echo "pinned-actions: ${found} uses: entries OK"
  return 0
}

self_test() {
  local good="${ROOT}/scripts/testdata/pinned-actions/good"
  local bad="${ROOT}/scripts/testdata/pinned-actions/bad"
  local f out rc
  local failed=0

  echo "== good fixtures (must pass) =="
  if ! scan_paths "$good"; then
    echo "self-test: good fixtures must pass" >&2
    failed=1
  fi

  echo "== bad fixtures (each must fail with file:line) =="
  if [[ ! -d "$bad" ]]; then
    echo "missing $bad" >&2
    exit 1
  fi
  while IFS= read -r f; do
    echo "-- $f --"
    rc=0
    out="$(scan_paths "$f" 2>&1)" || rc=$?
    printf '%s\n' "$out"
    if [[ "$rc" -eq 0 ]]; then
      echo "self-test: expected failure for $f" >&2
      failed=1
      continue
    fi
    if ! grep -Eq "${f}:[0-9]+:" <<<"$out"; then
      echo "self-test: expected file:line in rejection of $f" >&2
      failed=1
    fi
  done < <(find "$bad" \( -name '*.yml' -o -name '*.yaml' \) -type f | sort)

  if [[ "$failed" -ne 0 ]]; then
    echo "self-test failed" >&2
    return 1
  fi
  echo "self-test passed"
  return 0
}

if [[ $# -gt 0 && "$1" == "--self-test" ]]; then
  shift
  if [[ $# -ne 0 ]]; then
    usage
  fi
  self_test
  exit $?
fi

if [[ $# -eq 0 ]]; then
  paths=()
  paths+=("${ROOT}/.github/workflows")
  if [[ -d "${ROOT}/.github/actions" ]]; then
    paths+=("${ROOT}/.github/actions")
  fi
  scan_paths "${paths[@]}"
else
  if [[ "$1" == "-h" || "$1" == "--help" ]]; then
    usage
  fi
  scan_paths "$@"
fi
