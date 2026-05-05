#!/usr/bin/env bash
# lint-openapi.sh — validate docs/reference/openapi.yaml against the
# OpenAPI 3.x schema. Per docs/plans/doc-uplift.md P2.8 §5.8 acceptance.
#
# Run from repo root:   scripts/lint-openapi.sh
# CI usage:             added as a step in .github/workflows/ci.yml
#
# Exits 0 on clean; non-zero on validation failure.

set -u

cd "$(dirname "$0")/.."

SPEC=docs/reference/openapi.yaml

if [ ! -f "$SPEC" ]; then
  echo "FAIL: $SPEC missing"
  exit 1
fi

if ! command -v python3 >/dev/null 2>&1; then
  echo "FAIL: python3 not on PATH"
  exit 1
fi

if ! python3 -c 'import openapi_spec_validator' 2>/dev/null; then
  echo "Installing openapi-spec-validator (one-time)..."
  pip3 install --quiet --user openapi-spec-validator || {
    echo "FAIL: cannot install openapi-spec-validator"
    exit 1
  }
fi

python3 - "$SPEC" <<'PY'
import sys
import yaml

from openapi_spec_validator import validate_spec
from openapi_spec_validator.readers import read_from_filename

spec_path = sys.argv[1]
spec, _ = read_from_filename(spec_path)

try:
    validate_spec(spec)
except Exception as exc:
    print(f"FAIL: {spec_path} did not validate")
    print(exc)
    sys.exit(1)

paths = len(spec.get("paths", {}))
schemas = len(spec.get("components", {}).get("schemas", {}))
print(f"OK: {spec_path} validates against OpenAPI {spec.get('openapi')}; "
      f"{paths} paths, {schemas} schemas")
PY
