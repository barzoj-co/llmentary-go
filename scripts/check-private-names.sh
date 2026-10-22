#!/usr/bin/env bash
# Fail if private project names appear outside this check.
# This file is allowed to mention the needles because it asserts their absence.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SELF="$(basename "${BASH_SOURCE[0]}")"
# healsens / livekit / chatservice (and livekit-demo) as contiguous identifiers.
PATTERN='healsens|livekit|chatservice'

scan() {
  local root="$1"
  local rc=0 hits=""

  if [[ ! -d "${root}" ]]; then
    echo "private-name scan: not a directory: ${root}" >&2
    return 2
  fi

  hits="$(grep -RInE -i --exclude-dir=.git --exclude="${SELF}" --exclude='*.swp' \
    "${PATTERN}" "${root}" 2>&1)" && rc=0 || rc=$?
  if [[ "${rc}" -gt 1 ]]; then
    printf '%s\n' "${hits}" >&2
    echo "private-name content scan failed (grep exit ${rc})" >&2
    return 2
  fi
  if [[ "${rc}" -eq 0 && -n "${hits}" ]]; then
    printf '%s\n' "${hits}"
  fi

  hits="$(
    (
      cd "${root}"
      find . -type d -name .git -prune -o \
        ! -name "${SELF}" ! -name '*.swp' -print
    ) | grep -iE "${PATTERN}"
  )" && rc=0 || rc=$?
  if [[ "${rc}" -gt 1 ]]; then
    echo "private-name path scan failed (grep exit ${rc})" >&2
    return 2
  fi
  if [[ "${rc}" -eq 0 && -n "${hits}" ]]; then
    printf '%s\n' "${hits}"
  fi
  return 0
}

self_test() {
  local tmp failed=0 hits
  tmp="$(mktemp -d)"
  trap 'rm -rf "'"${tmp}"'"' EXIT

  mkdir -p "${tmp}/ok" "${tmp}/bad" "${tmp}/badpath"
  printf '%s\n' 'session_id: chat-service-run1' 'name: voice-agent' 'peer: service-a' \
    >"${tmp}/ok/generic.txt"
  printf '%s\n' 'session_id: chatservice-run1' >"${tmp}/bad/legacy.txt"
  printf '%s\n' 'livekit-demo' >"${tmp}/bad/voice.txt"
  printf '%s\n' 'HealSens' >"${tmp}/bad/health.txt"
  printf '%s\n' 'ok' >"${tmp}/badpath/chatservice-run1.json"

  hits="$(scan "${tmp}/ok")"
  if [[ -n "${hits}" ]]; then
    echo "self-test: generic names must pass:" >&2
    echo "${hits}" >&2
    failed=1
  fi

  hits="$(scan "${tmp}/bad")"
  if [[ -z "${hits}" ]]; then
    echo "self-test: expected private names to be rejected" >&2
    failed=1
  fi
  for needle in chatservice livekit healsens; do
    if ! grep -Fiq "${needle}" <<<"${hits}"; then
      echo "self-test: expected hit for ${needle}" >&2
      failed=1
    fi
  done

  hits="$(scan "${tmp}/badpath")"
  if ! grep -Fq 'chatservice-run1.json' <<<"${hits}"; then
    echo "self-test: expected path-name hit for chatservice-run1.json" >&2
    echo "${hits}" >&2
    failed=1
  fi

  if hits="$(scan "${tmp}/missing" 2>/dev/null)"; then
    echo "self-test: missing directory must fail the scan" >&2
    failed=1
  fi

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

if ! hits="$(scan "${ROOT}")"; then
  exit 1
fi
if [[ -n "${hits}" ]]; then
  echo "private project names must be genericized (chat-service, voice-agent, service-a):" >&2
  echo "${hits}" >&2
  exit 1
fi

echo "no private project names outside ${SELF}"
