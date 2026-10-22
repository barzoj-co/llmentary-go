#!/usr/bin/env bash
# Exit 0 when live e2e succeeded, or when it was skipped on a fork PR.
# Same-repo skipped/failed e2e still fails the gate.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT="${ROOT}/scripts/e2e-gate.sh"

FORK_SKIP_MSG="skipped (fork, no secrets)"

evaluate() {
  local result="${E2E_RESULT-}"
  local event="${EVENT_NAME-}"
  local is_fork="${IS_FORK-}"

  if [[ "${result}" == "success" ]]; then
    return 0
  fi

  if [[ "${is_fork}" == "true" && "${result}" == "skipped" ]]; then
    echo "${FORK_SKIP_MSG}"
    return 0
  fi

  if [[ "${result}" == "skipped" ]]; then
    echo "::error::live e2e did not run (event=${event}, result=skipped)"
    return 1
  fi

  echo "::error::live e2e did not succeed (result=${result})"
  return 1
}

run_case() {
  local result="$1"
  local event="$2"
  local is_fork="$3"
  local expect_rc="$4"
  local expect_msg="${5-}"
  local out rc

  rc=0
  out="$(
    E2E_RESULT="${result}" EVENT_NAME="${event}" IS_FORK="${is_fork}" \
      bash "${SCRIPT}" 2>&1
  )" || rc=$?

  printf -- '-- E2E_RESULT=%q EVENT_NAME=%q IS_FORK=%q (rc=%s) --\n' \
    "${result}" "${event}" "${is_fork}" "${rc}"
  printf '%s\n' "${out}"

  if [[ "${rc}" -ne "${expect_rc}" ]]; then
    echo "self-test: expected exit ${expect_rc}, got ${rc}" >&2
    return 1
  fi
  if [[ -n "${expect_msg}" ]] && ! grep -Fq "${expect_msg}" <<<"${out}"; then
    echo "self-test: expected message: ${expect_msg}" >&2
    return 1
  fi
  return 0
}

self_test() {
  local failed=0

  run_case success pull_request false 0 || failed=1
  run_case success pull_request true 0 || failed=1
  run_case skipped pull_request true 0 "${FORK_SKIP_MSG}" || failed=1
  run_case skipped pull_request false 1 \
    "live e2e did not run (event=pull_request, result=skipped)" || failed=1
  run_case skipped workflow_dispatch false 1 \
    "live e2e did not run (event=workflow_dispatch, result=skipped)" || failed=1
  run_case skipped push false 1 \
    "live e2e did not run (event=push, result=skipped)" || failed=1
  run_case failure pull_request false 1 \
    "live e2e did not succeed (result=failure)" || failed=1
  run_case failure pull_request true 1 \
    "live e2e did not succeed (result=failure)" || failed=1
  run_case cancelled pull_request false 1 \
    "live e2e did not succeed (result=cancelled)" || failed=1
  run_case cancelled pull_request true 1 \
    "live e2e did not succeed (result=cancelled)" || failed=1
  run_case "" pull_request false 1 \
    "live e2e did not succeed (result=)" || failed=1
  run_case unknown pull_request false 1 \
    "live e2e did not succeed (result=unknown)" || failed=1

  if [[ "${failed}" -ne 0 ]]; then
    echo "self-test failed" >&2
    return 1
  fi
  echo "self-test passed"
  return 0
}

if [[ "${1-}" == "--self-test" ]]; then
  if [[ $# -ne 1 ]]; then
    echo "usage: $0 [--self-test]" >&2
    exit 2
  fi
  self_test
  exit $?
fi

if [[ $# -ne 0 ]]; then
  echo "usage: $0 [--self-test]" >&2
  exit 2
fi

evaluate
