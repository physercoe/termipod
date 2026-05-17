#!/usr/bin/env bash
# lint-templates.sh — CI guard for bundled agent templates.
#
# Runs the same auditBundledAgentTemplates check that server.New runs
# at hub start, but in a unit-test harness so PRs that break a template
# fail in CI before merge. Mirrors the hub-startup audit so the two
# can't drift: any time a contributor adds a template field that the
# spawn pipeline depends on, both surfaces see it.
#
# Exit codes:
#   0  — every bundled template passes the audit
#   1  — at least one template is broken; output names the offender
#   2  — toolchain/setup error (no Go available, etc.)
#
# Wire into CI alongside scripts/lint-docs.sh and scripts/lint-glossary.sh.
#
# Context: docs/discussions/validate-at-every-boundary.md §3 Layer 3
# (CI lint) — catches drift in PRs before main; complements the
# Layer 2 startup audit in hub/internal/server/template_audit.go.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
HUB_DIR="$(cd "$SCRIPT_DIR/.." && pwd)/hub"

if ! command -v go >/dev/null 2>&1; then
  if [[ -x /usr/local/go/bin/go ]]; then
    export PATH="/usr/local/go/bin:$PATH"
  else
    echo "lint-templates: go binary not found on PATH" >&2
    exit 2
  fi
fi

cd "$HUB_DIR"

# The audit is exposed as a unit test; running it as a single-test
# invocation keeps this script trivial and ensures the check uses the
# exact same code path as `go test ./...`.
output=$(go test ./internal/server/ \
  -run "TestAuditBundledAgentTemplates_AllBundledTemplatesValid" \
  -count=1 -timeout 30s 2>&1)
status=$?

if [[ $status -ne 0 ]]; then
  echo "$output" >&2
  echo "" >&2
  echo "lint-templates: at least one bundled agent template is broken." >&2
  echo "  Edit hub/templates/agents/*.yaml so every file has both" >&2
  echo "  a top-level 'template:' name and a non-empty 'backend.cmd'." >&2
  echo "  See docs/discussions/validate-at-every-boundary.md §3." >&2
  exit 1
fi

echo "lint-templates: bundled agent templates pass startup audit"
