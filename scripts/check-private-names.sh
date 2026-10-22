#!/usr/bin/env bash
# Fail if a [a-z0-9]+ token hashes to a banned SHA-256 digest.
# Tokens are lowercased; hyphens split, so chat-service / voice-agent / service-a pass.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# SHA-256 hex of each banned lowercase identifier (no plaintext needles).
BANNED_HASHES="$(cat <<'EOF'
bfa1db5d28c58cd1a6707cec516613801655fc2e5bbd8341b5d70094c50f5e25
25bf6a1d9829fb0e7b6a814b440a73c5963ba01c08cd106e0f54079bb2313824
5d97e25fcda8238697824d87f4622148514bfea35ac58540a886dceb9f788e56
EOF
)"

scan() {
  local root="$1"
  local extra="${2-}"
  PRIVATE_NAME_ROOT="${root}" \
    PRIVATE_NAME_BANNED="${BANNED_HASHES}" \
    PRIVATE_NAME_EXTRA="${extra}" \
    python3 - <<'PY'
from __future__ import annotations

import hashlib
import os
import re
import sys

TOKEN = re.compile(r"[A-Za-z0-9]+")
HEX64 = re.compile(r"^[0-9a-f]{64}$")


def die(msg: str, code: int = 2) -> None:
    print(msg, file=sys.stderr)
    raise SystemExit(code)


def load_hashes(raw: str) -> set[str]:
    out: set[str] = set()
    for line in raw.splitlines():
        h = line.strip().lower()
        if not h:
            continue
        if not HEX64.match(h):
            die(f"private-name scan: invalid digest {h!r}")
        out.add(h)
    return out


root = os.environ["PRIVATE_NAME_ROOT"]
banned = load_hashes(os.environ.get("PRIVATE_NAME_BANNED", ""))
banned |= load_hashes(os.environ.get("PRIVATE_NAME_EXTRA", ""))
if not banned:
    die("private-name scan: no banned digests")

if not os.path.isdir(root):
    die(f"private-name scan: not a directory: {root}")


def digest(token: str) -> str:
    return hashlib.sha256(token.encode("ascii")).hexdigest()


def tokens(text: str) -> list[str]:
    return [m.group(0).lower() for m in TOKEN.finditer(text)]


hits: list[str] = []
try:
    for dirpath, dirnames, filenames in os.walk(root, onerror=lambda e: die(str(e))):
        dirnames[:] = [d for d in dirnames if d != ".git"]
        rel_dir = os.path.relpath(dirpath, root)
        for name in dirnames + filenames:
            if name == ".git":
                continue
            rel = name if rel_dir == "." else os.path.join(rel_dir, name)
            for tok in tokens(rel.replace("\\", "/")):
                if digest(tok) in banned:
                    hits.append(rel)
                    break
        for name in filenames:
            path = os.path.join(dirpath, name)
            rel = name if rel_dir == "." else os.path.join(rel_dir, name)
            try:
                data = open(path, "rb").read()
            except OSError as e:
                die(f"private-name content scan failed: {e}")
            try:
                text = data.decode("utf-8")
            except UnicodeDecodeError:
                text = data.decode("utf-8", "replace")
            for i, line in enumerate(text.splitlines(), 1):
                for tok in tokens(line):
                    if digest(tok) in banned:
                        hits.append(f"{rel}:{i}")
                        break
except OSError as e:
    die(f"private-name path scan failed: {e}")

if hits:
    print("\n".join(hits))
PY
}

digest_of() {
  python3 -c 'import hashlib,sys; print(hashlib.sha256(sys.argv[1].encode("ascii")).hexdigest())' "$1"
}

self_test() {
  local tmp failed=0 hits extra
  tmp="$(mktemp -d)"
  trap 'rm -rf "'"${tmp}"'"' EXIT

  extra="$(digest_of 'bannedfixturetoken')"

  mkdir -p "${tmp}/ok" "${tmp}/bad" "${tmp}/badpath"
  printf '%s\n' 'session_id: chat-service-run1' 'name: voice-agent' 'peer: service-a' \
    >"${tmp}/ok/generic.txt"
  printf '%s\n' 'session_id: bannedfixturetoken-run1' >"${tmp}/bad/legacy.txt"
  printf '%s\n' 'BannedFixtureToken-demo' >"${tmp}/bad/voice.txt"
  printf '%s\n' 'ok' >"${tmp}/badpath/bannedfixturetoken-run1.json"
  mkdir -p "${tmp}/selfname"
  printf '%s\n' 'bannedfixturetoken' >"${tmp}/selfname/check-private-names.sh"

  hits="$(scan "${tmp}/ok" "${extra}")"
  if [[ -n "${hits}" ]]; then
    echo "self-test: generic names must pass:" >&2
    echo "${hits}" >&2
    failed=1
  fi

  hits="$(scan "${tmp}/bad" "${extra}")"
  if [[ -z "${hits}" ]]; then
    echo "self-test: expected hashed-token content hits" >&2
    failed=1
  fi
  if ! grep -Fq 'legacy.txt:1' <<<"${hits}"; then
    echo "self-test: expected content hit in legacy.txt" >&2
    echo "${hits}" >&2
    failed=1
  fi
  if ! grep -Fq 'voice.txt:1' <<<"${hits}"; then
    echo "self-test: expected content hit in voice.txt" >&2
    echo "${hits}" >&2
    failed=1
  fi

  hits="$(scan "${tmp}/badpath" "${extra}")"
  if ! grep -Fq 'bannedfixturetoken-run1.json' <<<"${hits}"; then
    echo "self-test: expected path-name hit" >&2
    echo "${hits}" >&2
    failed=1
  fi

  hits="$(scan "${tmp}/selfname" "${extra}")"
  if ! grep -Fq 'check-private-names.sh:1' <<<"${hits}"; then
    echo "self-test: expected hit inside same-basename file" >&2
    echo "${hits}" >&2
    failed=1
  fi

  mkdir -p "${tmp}/swap"
  printf '%s\n' 'bannedfixturetoken' >"${tmp}/swap/notes.swp"
  hits="$(scan "${tmp}/swap" "${extra}")"
  if ! grep -Fq 'notes.swp:1' <<<"${hits}"; then
    echo "self-test: expected hit inside .swp file" >&2
    echo "${hits}" >&2
    failed=1
  fi

  if hits="$(scan "${tmp}/missing" "${extra}" 2>/dev/null)"; then
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

echo "no private project names"
