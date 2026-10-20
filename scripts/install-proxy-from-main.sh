#!/usr/bin/env bash
# Build barzoj-co/llmentary from main into DEST/llmentary.
# Used by CI so client tests exercise current control-API endpoints.
# Do not print tokens.
set -euo pipefail

DEST="${1:-}"
if [[ -z "${DEST}" ]]; then
  echo "usage: $0 dest-dir" >&2
  exit 2
fi

TOKEN="${LLMTESTLAB_REPO_TOKEN:-${LLMENTARY_GITHUB_TOKEN:-${GH_TOKEN:-${GITHUB_TOKEN:-}}}}"
if [[ -z "${TOKEN}" ]]; then
  echo "need LLMTESTLAB_REPO_TOKEN (or GH_TOKEN) to clone barzoj-co/llmentary@main" >&2
  exit 1
fi

mkdir -p "${DEST}"
DEST="$(cd "${DEST}" && pwd)"
TMP="$(mktemp -d)"
cleanup() { rm -rf "${TMP}"; }
trap cleanup EXIT

# Pin to the DOG-1 merge on proxy main (path prefix /s/{session}/).
PROXY_SHA="${LLMENTARY_PROXY_SHA:-91388e2e399295f564519ce37ffc5e034311116f}"

# gh uses GH_TOKEN; keep the clone URL token-free.
GH_TOKEN="${TOKEN}" gh repo clone barzoj-co/llmentary "${TMP}/src" -- --depth 1 --branch main
git -C "${TMP}/src" fetch --depth 1 origin "${PROXY_SHA}"
git -C "${TMP}/src" checkout --detach "${PROXY_SHA}"

echo "proxy ${PROXY_SHA} $(git -C "${TMP}/src" rev-parse --short HEAD)"
(cd "${TMP}/src" && go build -o "${DEST}/llmentary" ./cmd/llmentary)
chmod +x "${DEST}/llmentary"
