#!/usr/bin/env bash
# Fail if AGPL / proxy-license commentary appears outside this check.
# This file is allowed to mention the needles because it asserts their absence.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SELF="$(basename "${BASH_SOURCE[0]}")"

hits="$(
  grep -RInE -i --exclude-dir=.git --exclude="$SELF" \
    'agpl|does not change your application' "$ROOT" || true
)"

if [[ -n "$hits" ]]; then
  echo "AGPL/proxy-license commentary is not allowed in this MIT repository:" >&2
  echo "$hits" >&2
  exit 1
fi

echo "no AGPL/proxy-license commentary outside ${SELF}"
