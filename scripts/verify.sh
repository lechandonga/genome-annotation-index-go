#!/usr/bin/env bash
# Local, dependency-free verification: vet, format check and the full test
# suite. Verbose test output shows BUSINESS groups with per-case PASS lines.
set -euo pipefail

cd "$(dirname "$0")/.."

echo "== go vet =="
go vet ./...

echo "== gofmt check =="
unformatted="$(gofmt -l .)"
if [[ -n "${unformatted}" ]]; then
  echo "gofmt needed on: ${unformatted}" >&2
  exit 1
fi

echo "== go test -v =="
go test -v ./...

echo "ALL CHECKS PASSED"
