#!/usr/bin/env bash
# lint-governed-actions.sh — keep ADR-030's three sources of truth in lockstep:
#
#   (R) the registered propose kinds, statically grepped from
#       hub/internal/server/*.go for `RegisterProposeKind(ProposeKind{`
#       calls + their literal `Kind: "<name>"` argument.
#   (P) the policy.yaml `kinds:` block — declared per (kind, tier) policy.
#       Discovered by glob; --policy <path> pins an explicit file.
#   (S) the safety constraints — kind names must be snake_case-with-dots
#       per the Kind field doc; any kind with `escalate_on_timeout: true`
#       must have a `default_tier` strictly below `principal` so the
#       Option 2' signal has somewhere to walk (ADR-030 §Amendments
#       2026-05-20).
#
# Three checks:
#   1. Kind-shape (S): each registered kind name matches the linter's
#      allowed pattern. Catches a typo at registration time before it
#      hits production.
#   2. Bidirectional consistency (R vs P): every registered kind has a
#      policy entry; every policy entry has a registered handler. FAIL
#      on either mismatch when a policy file is found. If no policy file
#      is found AND the registry is non-empty, emit a WARN — operator
#      should land their team policy.
#   3. Escalate-on-timeout sanity (S): warn loud when a kind opts into
#      timeout escalation but the default_tier is already `principal` —
#      there's no upper tier to walk to.
#
# Run from repo root:   scripts/lint-governed-actions.sh
# Pin a specific file:  scripts/lint-governed-actions.sh --policy path/to/policy.yaml
# Disable WARN-on-empty-policy: --no-warn-empty
#
# Exits 0 on clean, 1 on any check 1 or 2 failure. Check 3 emits WARN
# lines that don't fail the build.

set -u

cd "$(dirname "$0")/.."

REGISTRY_GLOB="hub/internal/server/*.go"
POLICY_FILE=""
WARN_EMPTY=1

while [[ $# -gt 0 ]]; do
  case "$1" in
    --policy)
      POLICY_FILE="${2:-}"; shift 2 ;;
    --no-warn-empty)
      WARN_EMPTY=0; shift ;;
    --registry-glob)
      REGISTRY_GLOB="${2:-}"; shift 2 ;;
    -h|--help)
      sed -n '2,32p' "$0"; exit 0 ;;
    *)
      echo "lint-governed-actions: unknown arg: $1" >&2
      exit 2 ;;
  esac
done

failed=0
warned=0

# --- Discover registered kinds (R) ---
#
# Match `RegisterProposeKind(ProposeKind{` blocks; pull the `Kind:
# "<name>"` literal that opens each block. Two newline shapes are
# accepted (single-line and multi-line struct literal), so we let
# python3 do the parsing — bash regex would be brittle for the
# multi-line case.
#
# Output of this step: one kind per line on stdout, alphabetically.

discover_registered() {
  python3 - <<'PYEOF' "$REGISTRY_GLOB"
import glob
import re
import sys

paths = []
for pat in sys.argv[1].split():
    paths.extend(glob.glob(pat))

# Match `RegisterProposeKind(ProposeKind{ ... Kind: "<name>" ... })`
# across newlines. Lazy on `.*?` so each call resolves independently.
call_re = re.compile(
    r"RegisterProposeKind\s*\(\s*ProposeKind\s*\{(?P<body>.*?)\}\s*\)",
    re.DOTALL,
)
kind_re = re.compile(r'Kind\s*:\s*"(?P<kind>[^"]+)"')

# strip_go_comments — remove single-line // comments and /* */ blocks
# so doc-comment EXAMPLES (e.g. `// RegisterProposeKind(ProposeKind{
# Kind: "foo" })` in a doc comment) don't register as real calls. The
# regex is intentionally crude — it does NOT understand string-literal
# context — but Go style discourages embedding `//` inside non-comment
# string literals, so the false-positive surface is minimal.
def strip_go_comments(src: str) -> str:
    src = re.sub(r"//[^\n]*", "", src)
    src = re.sub(r"/\*.*?\*/", "", src, flags=re.DOTALL)
    return src

seen = []
for p in sorted(paths):
    # Skip *_test.go — tests register fake kinds intentionally and
    # those shouldn't drive the policy lint.
    if p.endswith("_test.go"):
        continue
    try:
        src = open(p, encoding="utf-8").read()
    except OSError:
        continue
    src = strip_go_comments(src)
    for m in call_re.finditer(src):
        km = kind_re.search(m.group("body"))
        if km:
            seen.append(km.group("kind"))

for k in sorted(set(seen)):
    print(k)
PYEOF
}

mapfile -t registered < <(discover_registered)

# --- Discover policy file (P) ---
#
# Order:
#   1. --policy <path> if passed.
#   2. <dataRoot> not knowable at CI time; instead glob the repo for
#      any committed policy.yaml fixture (tests, examples). We
#      intentionally do NOT walk hidden dirs or vendor trees.

if [[ -z "$POLICY_FILE" ]]; then
  candidates=()
  while IFS= read -r f; do
    [[ -n "$f" ]] && candidates+=("$f")
  done < <(find . -name "policy.yaml" -not -path "*/node_modules/*" \
             -not -path "*/vendor/*" -not -path "./.git/*" 2>/dev/null | sort)
  if [[ ${#candidates[@]} -ge 1 ]]; then
    POLICY_FILE="${candidates[0]}"
    if [[ ${#candidates[@]} -gt 1 ]]; then
      echo "lint-governed-actions: multiple policy.yaml found; linting against ${POLICY_FILE}"
      echo "  others: ${candidates[*]:1}"
      echo "  rerun with --policy <path> to pin a different one"
    fi
  fi
fi

# --- Check 1: Kind-shape ---
#
# Allowed: lowercase letters / digits / underscore, with one or more
# dot-separators (e.g. `deliverable.set_state`). Disallowed: dashes,
# uppercase, leading/trailing/double dots, leading underscore.
allowed_kind_re='^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$'

for kind in "${registered[@]}"; do
  if [[ ! "$kind" =~ $allowed_kind_re ]]; then
    echo "FAIL [bad-kind-shape]: registered kind '${kind}' violates pattern ${allowed_kind_re}"
    failed=1
  fi
done

# --- Discover declared kinds from policy file (P) ---
#
# When a policy file is found, parse its `kinds:` block. We use
# python3 (yaml lib) for the same robustness reason as discover_registered.

declared=()
escalate_on_timeout_principal=()

if [[ -n "$POLICY_FILE" && -f "$POLICY_FILE" ]]; then
  while IFS=$'\t' read -r kind default_tier escalate_on_timeout; do
    [[ -z "$kind" ]] && continue
    declared+=("$kind")
    if [[ "$escalate_on_timeout" == "True" && "$default_tier" == "principal" ]]; then
      escalate_on_timeout_principal+=("$kind")
    fi
  done < <(python3 - "$POLICY_FILE" <<'PYEOF'
import sys
try:
    import yaml
except ImportError:
    sys.stderr.write("lint-governed-actions: python3 yaml not installed; skipping kinds-block parse\n")
    sys.exit(0)
try:
    doc = yaml.safe_load(open(sys.argv[1], encoding="utf-8")) or {}
except yaml.YAMLError as e:
    sys.stderr.write(f"lint-governed-actions: failed to parse {sys.argv[1]}: {e}\n")
    sys.exit(2)
kinds = doc.get("kinds")
if not isinstance(kinds, dict):
    sys.exit(0)
for name, body in sorted(kinds.items()):
    if not isinstance(body, dict):
        sys.stderr.write(f"lint-governed-actions: kinds.{name} is not a mapping; skipping\n")
        continue
    default_tier = body.get("default_tier", "")
    escalate_on_timeout = bool(body.get("escalate_on_timeout", False))
    sys.stdout.write(f"{name}\t{default_tier}\t{escalate_on_timeout}\n")
PYEOF
)
fi

# --- Check 2: Bidirectional consistency (R vs P) ---

if [[ -n "$POLICY_FILE" ]]; then
  # Registered but not declared.
  for kind in "${registered[@]}"; do
    found=0
    for d in "${declared[@]}"; do
      [[ "$d" == "$kind" ]] && { found=1; break; }
    done
    if [[ $found -eq 0 ]]; then
      echo "FAIL [registered-no-policy]: kind '${kind}' is registered in code but has no entry in ${POLICY_FILE}"
      failed=1
    fi
  done
  # Declared but not registered.
  for d in "${declared[@]}"; do
    found=0
    for kind in "${registered[@]}"; do
      [[ "$kind" == "$d" ]] && { found=1; break; }
    done
    if [[ $found -eq 0 ]]; then
      echo "FAIL [policy-no-handler]: kind '${d}' is declared in ${POLICY_FILE} but no RegisterProposeKind call covers it"
      failed=1
    fi
  done
elif [[ ${#registered[@]} -gt 0 && $WARN_EMPTY -eq 1 ]]; then
  echo "WARN [no-policy]: ${#registered[@]} kind(s) registered but no policy.yaml found in repo"
  echo "  pass --policy <path> to lint against the operator's file, or --no-warn-empty to silence"
  warned=$((warned + 1))
fi

# --- Check 3: escalate_on_timeout sanity ---

for k in "${escalate_on_timeout_principal[@]}"; do
  echo "WARN [escalate-walks-nowhere]: kind '${k}' sets escalate_on_timeout: true but default_tier is 'principal'"
  echo "  the timeout signal has no upper tier to walk to; either lower default_tier or drop the flag"
  warned=$((warned + 1))
done

# --- Summary ---

if [[ -n "$POLICY_FILE" ]]; then
  echo "lint-governed-actions: registry=${#registered[@]} declared=${#declared[@]} policy=${POLICY_FILE}"
else
  echo "lint-governed-actions: registry=${#registered[@]} (no policy.yaml discovered)"
fi

if [[ $warned -gt 0 ]]; then
  echo "lint-governed-actions: ${warned} warning(s) (non-blocking)"
fi

if [[ $failed -ne 0 ]]; then
  echo "lint-governed-actions: failed"
  exit 1
fi

echo "lint-governed-actions: clean"
exit 0
