#!/usr/bin/env bash
# Fail if required README badges or the canonical llmentary-repos block are missing.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
README="${ROOT}/README.md"

if [[ ! -f "$README" ]]; then
  echo "missing README.md" >&2
  exit 1
fi

badges=(
  '[![CI](https://github.com/barzoj-co/llmentary-go/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/barzoj-co/llmentary-go/actions/workflows/ci.yml)'
  '[![codecov](https://codecov.io/gh/barzoj-co/llmentary-go/graph/badge.svg)](https://codecov.io/gh/barzoj-co/llmentary-go)'
  '[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/barzoj-co/llmentary-go/badge)](https://scorecard.dev/viewer/?uri=github.com/barzoj-co/llmentary-go)'
  '[![License: MIT](https://img.shields.io/github/license/barzoj-co/llmentary-go)](LICENSE)'
  '[![Go Reference](https://pkg.go.dev/badge/github.com/barzoj-co/llmentary-go.svg)](https://pkg.go.dev/github.com/barzoj-co/llmentary-go)'
  '[![Release](https://img.shields.io/github/v/release/barzoj-co/llmentary-go?sort=semver)](https://github.com/barzoj-co/llmentary-go/releases)'
)

missing=0
for b in "${badges[@]}"; do
  if ! grep -Fq "$b" "$README"; then
    echo "README.md missing badge: $b" >&2
    missing=1
  fi
done

canonical_block='<!-- llmentary-repos:start -->
## llmentary repositories

| Repository | What |
|---|---|
| [llmentary](https://github.com/barzoj-co/llmentary) | Recording proxy binary (Go) |
| [llmentary-go](https://github.com/barzoj-co/llmentary-go) | Go client for `go test` |
| [llmentary-python](https://github.com/barzoj-co/llmentary-python) | Python client and pytest plugin |
| [llmentary-js](https://github.com/barzoj-co/llmentary-js) | TypeScript/JavaScript client for Vitest and Jest |
<!-- llmentary-repos:end -->'

readme_content="$(<"$README")"
if [[ "$readme_content" != *"$canonical_block"* ]]; then
  echo "README.md missing or altered canonical llmentary-repos block" >&2
  missing=1
fi

license_section='## License

Licensed under MIT, see LICENSE'
if [[ "$readme_content" != *"$license_section" ]]; then
  echo "README.md License section must be the single line 'Licensed under MIT, see LICENSE'" >&2
  missing=1
fi

if [[ "$missing" -ne 0 ]]; then
  exit 1
fi

echo "README badges and llmentary-repos block OK"
