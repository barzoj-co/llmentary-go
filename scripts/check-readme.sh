#!/usr/bin/env bash
# Fail if required README badges or the canonical ltl-repos block are missing.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
README="${ROOT}/README.md"

if [[ ! -f "$README" ]]; then
  echo "missing README.md" >&2
  exit 1
fi

badges=(
  '[![CI](https://github.com/barzoj-co/llm-test-lab-go/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/barzoj-co/llm-test-lab-go/actions/workflows/ci.yml)'
  '[![codecov](https://codecov.io/gh/barzoj-co/llm-test-lab-go/graph/badge.svg)](https://codecov.io/gh/barzoj-co/llm-test-lab-go)'
  '[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/barzoj-co/llm-test-lab-go/badge)](https://scorecard.dev/viewer/?uri=github.com/barzoj-co/llm-test-lab-go)'
  '[![License: MIT](https://img.shields.io/github/license/barzoj-co/llm-test-lab-go)](LICENSE)'
  '[![Go Reference](https://pkg.go.dev/badge/github.com/barzoj-co/llm-test-lab-go.svg)](https://pkg.go.dev/github.com/barzoj-co/llm-test-lab-go)'
  '[![Release](https://img.shields.io/github/v/release/barzoj-co/llm-test-lab-go?sort=semver)](https://github.com/barzoj-co/llm-test-lab-go/releases)'
)

missing=0
for b in "${badges[@]}"; do
  if ! grep -Fq "$b" "$README"; then
    echo "README.md missing badge: $b" >&2
    missing=1
  fi
done

canonical_block='<!-- ltl-repos:start -->
## llm-test-lab repositories

| Repository | What |
|---|---|
| [llm-test-lab](https://github.com/barzoj-co/llm-test-lab) | Recording proxy binary (Go) |
| [llm-test-lab-go](https://github.com/barzoj-co/llm-test-lab-go) | Go client for `go test` |
| [llm-test-lab-python](https://github.com/barzoj-co/llm-test-lab-python) | Python client and pytest plugin |
| [llm-test-lab-js](https://github.com/barzoj-co/llm-test-lab-js) | TypeScript/JavaScript client for Vitest and Jest |
<!-- ltl-repos:end -->'

readme_content="$(<"$README")"
if [[ "$readme_content" != *"$canonical_block"* ]]; then
  echo "README.md missing or altered canonical ltl-repos block" >&2
  missing=1
fi

license_section='## License

Licensed under MIT, see LICENSE'
if [[ "$readme_content" != *"$license_section"* ]]; then
  echo "README.md License section must be the single line 'Licensed under MIT, see LICENSE'" >&2
  missing=1
fi

if [[ "$missing" -ne 0 ]]; then
  exit 1
fi

echo "README badges and ltl-repos block OK"
