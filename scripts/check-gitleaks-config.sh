#!/usr/bin/env bash
# Verify the committed gitleaks allowlist: placeholders pass, realistic keys fail.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONFIG="${ROOT}/.gitleaks.toml"

usage() {
  echo "usage: $0 --self-test" >&2
  exit 2
}

rand_alnum() {
  local n="$1"
  python3 -c "import secrets,string; print(''.join(secrets.choice(string.ascii_letters+string.digits) for _ in range(${n})))"
}

require_gitleaks() {
  if ! command -v gitleaks >/dev/null 2>&1; then
    echo "gitleaks is required on PATH" >&2
    exit 1
  fi
  if [[ ! -f "${CONFIG}" ]]; then
    echo "missing ${CONFIG}" >&2
    exit 1
  fi
}

scan_dir() {
  local dir="$1"
  gitleaks dir --config "${CONFIG}" --redact --no-banner --exit-code 1 "${dir}"
}

self_test() {
  require_gitleaks
  local rc out failed=0

  GITLEAKS_SELFTEST_DIR="$(mktemp -d)"
  tmp="${GITLEAKS_SELFTEST_DIR}"
  trap 'rm -rf "${GITLEAKS_SELFTEST_DIR}"' EXIT

  echo "== placeholders must not be reported =="
  mkdir -p "${tmp}/clean/e2e" "${tmp}/clean/docs"
  printf 'OPENAI_API_KEY=sk-...\n' >"${tmp}/clean/README.md"
  printf 'see sk-...\n' >"${tmp}/clean/docs/ci.md"
  printf 'const k = "sk-invalid-key-for-401-test"\n' >"${tmp}/clean/e2e/key.txt"
  rc=0
  out="$(scan_dir "${tmp}/clean" 2>&1)" || rc=$?
  printf '%s\n' "${out}"
  if [[ "${rc}" -ne 0 ]]; then
    echo "self-test: placeholders must exit 0" >&2
    failed=1
  fi

  echo "== realistic keys must be reported (including under allowlisted paths) =="
  local infix openai_key github_pat
  infix="$(printf 'OpenAI' | base64 | tr -d '\n=')"
  openai_key="sk-$(rand_alnum 20)${infix}$(rand_alnum 20)"
  github_pat="ghp_$(rand_alnum 36)"

  mkdir -p "${tmp}/dirty/e2e"
  printf 'KEY=%s\n' "${openai_key}" >"${tmp}/dirty/README.md"
  printf 'KEY=%s\n' "${github_pat}" >"${tmp}/dirty/e2e/note.txt"
  unset openai_key github_pat

  rc=0
  out="$(scan_dir "${tmp}/dirty" 2>&1)" || rc=$?
  printf '%s\n' "${out}"
  if [[ "${rc}" -eq 0 ]]; then
    echo "self-test: realistic keys under README.md and e2e/ must exit 1" >&2
    failed=1
  fi
  if ! grep -Eqi 'leaks found|leak' <<<"${out}"; then
    echo "self-test: expected a leak summary" >&2
    failed=1
  fi

  if [[ "${failed}" -ne 0 ]]; then
    echo "self-test failed" >&2
    return 1
  fi
  echo "self-test passed"
  return 0
}

if [[ $# -ne 1 || "$1" != "--self-test" ]]; then
  usage
fi
self_test
