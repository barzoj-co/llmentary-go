#!/usr/bin/env bash
# Build barzoj-co/llmentary (pinned SHA) into DEST/llmentary.
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
  echo "need LLMTESTLAB_REPO_TOKEN (or GH_TOKEN) to download barzoj-co/llmentary" >&2
  exit 1
fi

mkdir -p "${DEST}"
DEST="$(cd "${DEST}" && pwd)"
TMP="$(mktemp -d)"
cleanup() { rm -rf "${TMP}"; }
trap cleanup EXIT

# Pin to the DOG-1 merge on proxy main (path prefix /s/{session}/).
PROXY_SHA="${LLMENTARY_PROXY_SHA:-91388e2e399295f564519ce37ffc5e034311116f}"

# gh uses GH_TOKEN for the API. A git clone of the private repo over HTTPS
# has no credentials (fatal: could not read Username). The tarball endpoint
# authenticates as gh and never puts the token in a remote URL.
export GH_TOKEN="${TOKEN}"
export GH_PROMPT_DISABLED=1
gh api \
  -H "Accept: application/vnd.github+json" \
  "repos/barzoj-co/llmentary/tarball/${PROXY_SHA}" > "${TMP}/src.tgz"
mkdir -p "${TMP}/src"
tar -xzf "${TMP}/src.tgz" -C "${TMP}/src" --strip-components=1
rm -f "${TMP}/src.tgz"

echo "proxy ${PROXY_SHA}"
test -f "${TMP}/src/go.mod"
(cd "${TMP}/src" && go build -o "${DEST}/llmentary" ./cmd/llmentary)
chmod +x "${DEST}/llmentary"
