#!/usr/bin/env bash
# lint-docs.sh — enforce docs/doc-spec.md §3 status block + naming spec.
#
# Four checks:
#   1. Status block — every doc in docs/ (except archive/, screens/, logo/)
#      has the 5-line block at the top: Type / Status / Audience /
#      Last verified vs code / (optional Supersedes / optional Freshness).
#   2. Resolved discussions — discussions/ docs with Status: Resolved
#      must link to a decisions/NNN-*.md or plans/*.md in their first
#      30 lines.
#   3. Cross-references — every [text](path.md) link resolves to an
#      existing file (relative to the file containing the link).
#   4. Stale-doc gate — docs whose `Last verified vs code:` stamp is
#      more than STALE_DAYS days behind the current pubspec.yaml
#      version are graded by their `Freshness:` field (per doc-spec §6.1):
#        contract  → FAIL (CI blocks)
#        rolling   → WARN (non-failing — current behaviour)
#        snapshot  → skipped
#      Missing field uses the per-primitive default from doc-spec
#      §6.1; the resolver lives in this script.
#
#      Versions are date-based CalVer YYYY.MMDD.HHMM (UTC build time) as
#      of 2026.722.219; earlier releases used sequential v1.0.x. Both the
#      current version and each doc stamp are converted to a Julian day
#      number (pure integer arithmetic — no `date`, so it's portable and
#      deterministic) and staleness is the day delta. Legacy v1.0.x stamps
#      predate the CalVer era and have no place on the day axis, so they
#      are GRANDFATHERED (counted, never failed) until the doc is next
#      re-verified and re-stamped to CalVer.
#
# Run from repo root:   scripts/lint-docs.sh
# CI usage:             added as a step in .github/workflows/ci.yml
#
# Exits 0 on clean, 1 on any error-level failure (1-3 above, or
# contract-tier drift in 4). Rolling-tier stale warnings print but
# never fail.

# How many days a doc's last-verified CalVer stamp may lag the current
# pubspec version before it earns a WARN line. 30 ≈ "more than a month
# behind." Tune up during heavy refactors.
STALE_DAYS=${STALE_DAYS:-30}

# Plain shell — set -e and pipefail interact badly with grep -q
# (which legitimately exits 1 on no-match) inside if conditions and
# pipelines, producing non-deterministic failures here. We track
# exit state explicitly via the $failed counter.
set -u

cd "$(dirname "$0")/.."

failed=0
checked=0

is_excluded() {
  case "$1" in
    docs/archive/*|docs/screens/*|docs/logo/*) return 0 ;;
    *) return 1 ;;
  esac
}

# --- Check 1: status block ---

while IFS= read -r f; do
  is_excluded "$f" && continue
  checked=$((checked + 1))

  # Required fields. Look in the first 20 lines so we don't false-match
  # a body that happens to contain "Type:" later.
  head_block=$(head -20 "$f")

  for field in "Type" "Status" "Audience" "Last verified vs code"; do
    if ! echo "$head_block" | grep -qE "^> \*\*$field:\*\*"; then
      echo "FAIL [status-block]: $f — missing '$field' field in top 20 lines"
      failed=1
    fi
  done
done < <(find docs -name "*.md" -type f | sort)

# --- Check 2: resolved discussions link to ADRs ---

while IFS= read -r f; do
  case "$f" in docs/discussions/*) ;; *) continue ;; esac

  head_block=$(head -30 "$f")
  if echo "$head_block" | grep -qE "^> \*\*Status:\*\* Resolved"; then
    # A resolved discussion must link to either a decision (an ADR
    # captured the outcome) or a plan (a wedge consumed the finding
    # and shipped). Either is a durable resolution; only "Resolved"
    # with no forward pointer is an audit gap.
    if ! echo "$head_block" | grep -qE "(decisions/[0-9]{3}-|plans/)"; then
      echo "FAIL [resolved-link]: $f — Status: Resolved but no link to decisions/NNN-*.md or plans/*.md in top 30 lines"
      failed=1
    fi
  fi
done < <(find docs/discussions -name "*.md" -type f 2>/dev/null | sort)

# --- Check 3: cross-references ---
# Scan markdown links of the form [text](path.md) or [text](path.md#anchor)
# and verify each path resolves relative to the file's location.

# We're permissive about external links (http://, https://, mailto:),
# anchors-only (#section), and absolute paths starting with /.

while IFS= read -r f; do
  is_excluded "$f" && continue

  base_dir=$(dirname "$f")
  while IFS= read -r raw_link; do
    # Strip optional anchor and surrounding whitespace.
    link=${raw_link%%#*}
    link=${link%% *}
    link=${link## *}

    # Skip externals + anchor-only + absolute.
    case "$link" in
      ""|http*|mailto:*|/*) continue ;;
    esac

    # Only check .md targets — other extensions (svg, png, html) are out
    # of scope for this linter.
    case "$link" in
      *.md) ;;
      *) continue ;;
    esac

    target="$base_dir/$link"
    # Resolve .. and . segments via realpath where possible; fall back
    # to a literal stat for portability.
    if command -v realpath >/dev/null 2>&1; then
      resolved=$(realpath -m "$target" 2>/dev/null || echo "$target")
    else
      resolved="$target"
    fi

    if [ ! -f "$resolved" ]; then
      echo "FAIL [broken-link]: $f → $link (looked for $resolved)"
      failed=1
    fi
  done < <(grep -oE '\[[^]]+\]\([^)]+\)' "$f" | sed -E 's/.*\(([^)]+)\)/\1/')
done < <(find docs -name "*.md" -type f | sort)

# --- Check 4: stale-doc gate (failing for `contract`, warning for `rolling`) ---
# Parse the current pubspec version once. The format is
# `version: 2026.722.219-alpha+3447499` (CalVer) or the legacy
# `1.0.316-alpha+10316`; ver_to_jdn() converts either to a comparable day.
#
# Per doc-spec §6.1, behaviour gates on the doc's `Freshness:` field
# (or its per-primitive default when omitted):
#   contract → FAIL CI on drift > STALE_DAYS
#   rolling  → WARN (non-failing — pre-§6.1 default behaviour)
#   snapshot → skip drift check entirely
# resolve_freshness() below derives the default from the doc's
# directory + Status line when Freshness is absent.

# ver_to_jdn <string> — echoes a Julian day number for a CalVer
# YYYY.MMDD.HHMM version (day granularity; time dropped), the literal
# "legacy" for an old sub-2000 semver (v1.0.x / 0.3.x), or "" when the
# string carries no X.Y.Z version token. Tolerates a leading `v`, a
# `-suffix`, and surrounding words (e.g. "desktop 2026.722.211").
ver_to_jdn() {
  local raw="$1" core y rest mmdd mm dd a yy m jdn
  core=$(printf '%s' "$raw" | grep -oE 'v?[0-9]+\.[0-9]+\.[0-9]+' | head -1)
  core=${core#v}
  [ -z "$core" ] && { echo ""; return; }
  y=${core%%.*}; rest=${core#*.}; mmdd=${rest%%.*}
  # CalVer years are 4-digit (>= 2000); anything lower is the legacy scheme.
  if [ "$y" -lt 2000 ]; then echo "legacy"; return; fi
  mm=$(( 10#$mmdd / 100 )); dd=$(( 10#$mmdd % 100 ))
  if [ "$mm" -lt 1 ] || [ "$mm" -gt 12 ] || [ "$dd" -lt 1 ] || [ "$dd" -gt 31 ]; then echo ""; return; fi
  # Julian Day Number, proleptic Gregorian — pure integer arithmetic.
  a=$(( (14 - mm) / 12 )); yy=$(( y + 4800 - a )); m=$(( mm + 12 * a - 3 ))
  jdn=$(( dd + (153 * m + 2) / 5 + 365 * yy + yy / 4 - yy / 100 + yy / 400 - 32045 ))
  echo "$jdn"
}

stale_warnings=0
stale_failures=0
legacy_stamps=0
current_ver=""
current_jdn=""
if [ -f pubspec.yaml ]; then
  current_ver=$(grep -E "^version:" pubspec.yaml | head -1 | sed -E 's/^version:[[:space:]]*//; s/\+.*//')
  current_jdn=$(ver_to_jdn "$current_ver")
fi

# resolve_freshness <file> <status-line>
# Echoes one of: contract|rolling|snapshot|default-contract|default-snapshot
# - If the file has an explicit `> **Freshness:**` line in the first
#   20 lines, returns that value (one of contract|rolling|snapshot).
# - Otherwise derives a DEFAULT from primitive directory + Status word
#   per the doc-spec §6.1 default table. Defaults are tagged
#   `default-*` so the caller can distinguish "author committed to
#   contract" (strict, FAIL on drift) from "default would be
#   contract" (soft, WARN until author explicitly opts in).
#
# This phased gating preserves the pre-§6.1 CI behaviour: only docs
# whose authors have explicitly written `Freshness: contract` get
# the strict FAIL treatment. The unmarked backlog stays at WARN,
# matching the pre-§6.1 default. As authors touch each doc and add
# the explicit field, the strict tier grows incrementally without a
# big-bang CI break.
resolve_freshness() {
  local f="$1"
  local status_word="$2"

  # Explicit override from the file's own status block — highest precedence.
  local explicit
  explicit=$(head -20 "$f" | grep -E "^> \*\*Freshness:\*\*" | head -1 | \
    sed -E 's/.*Freshness:\*\*[[:space:]]*([a-z]+).*/\1/')
  case "$explicit" in
    contract|rolling|snapshot) echo "$explicit"; return ;;
  esac

  # Defaults by directory + Status. Mirrors the table in doc-spec §6.1.
  # Tagged `default-*` so the caller can differentiate "explicitly
  # contract" (strict) from "would default to contract" (soft).
  case "$f" in
    docs/decisions/*)
      case "$status_word" in
        Proposed) echo "default-contract" ;;
        Superseded|Deprecated) echo "default-snapshot" ;;
        *) echo "rolling" ;;  # Accepted (and anything else) — append-only
      esac
      ;;
    docs/plans/*)
      case "$status_word" in
        Done|Deferred|Cancelled) echo "default-snapshot" ;;
        *) echo "default-contract" ;;  # Proposed | In flight | … — active work
      esac
      ;;
    docs/discussions/*) echo "default-snapshot" ;;
    docs/reference/*|docs/tutorials/*) echo "default-contract" ;;
    docs/how-to/*|docs/spine/*) echo "rolling" ;;
    *) echo "rolling" ;;  # roadmap / README / doc-spec / top-level
  esac
}

if [ -n "$current_jdn" ] && [ "$current_jdn" != "legacy" ]; then
  while IFS= read -r f; do
    is_excluded "$f" && continue

    head_block=$(head -20 "$f")
    lv_line=$(echo "$head_block" | grep -E "^> \*\*Last verified vs code:\*\*" | head -1)
    [ -z "$lv_line" ] && continue

    doc_jdn=$(ver_to_jdn "$lv_line")

    # No version token (e.g. "pre-rebrand"): skip silently, as before.
    [ -z "$doc_jdn" ] && continue
    # Legacy v1.0.x / 0.x stamp: can't be placed on the day axis. Count it
    # (surfaced in the summary) and grandfather it — mass-flagging the
    # whole pre-CalVer backlog at the transition would be noise, and would
    # spuriously FAIL any contract-tier doc still on an old stamp.
    if [ "$doc_jdn" = "legacy" ]; then
      legacy_stamps=$((legacy_stamps + 1))
      continue
    fi

    doc_ver=$(printf '%s' "$lv_line" | grep -oE 'v?[0-9]+\.[0-9]+\.[0-9]+' | head -1)
    diff=$((current_jdn - doc_jdn))
    # A future stamp (negative diff) or within the window is fine.
    if [ "$diff" -le "$STALE_DAYS" ]; then
      continue
    fi

    # Extract the Status word (first token after `Status:` ignoring
    # leading whitespace + parenthesised dates). Used for the
    # per-primitive default resolver below.
    status_word=$(echo "$head_block" | \
      grep -E "^> \*\*Status:\*\*" | head -1 | \
      sed -E 's/.*Status:\*\*[[:space:]]*([A-Za-z]+).*/\1/')

    freshness=$(resolve_freshness "$f" "$status_word")

    case "$freshness" in
      snapshot|default-snapshot)
        # Snapshot docs are correct at their stamp time; later drift
        # is expected and not a defect. Skip silently.
        ;;
      contract)
        # Author has explicitly committed this doc to the contract
        # tier — strict drift = FAIL.
        echo "FAIL [stale-contract]: $f — Last verified vs $doc_ver ($diff days behind current $current_ver). Re-verify against current code and bump the stamp, or downgrade 'Freshness: contract' to rolling if drift is acceptable."
        stale_failures=$((stale_failures + 1))
        failed=1
        ;;
      default-contract|rolling|*)
        # Either rolling-tier (explicit or default) or default-contract
        # (the doc-spec recommends contract but the author hasn't
        # explicitly opted in yet). Both surface as WARN, preserving
        # pre-§6.1 behaviour for the unmarked backlog. Adding an
        # explicit `Freshness: contract` line escalates a doc to
        # the strict tier.
        echo "WARN [stale-doc]: $f — Last verified vs $doc_ver ($diff days behind current $current_ver)"
        stale_warnings=$((stale_warnings + 1))
        ;;
    esac
  done < <(find docs -name "*.md" -type f | sort)
fi

# --- Summary ---

if [ "$failed" -eq 0 ]; then
  msg="OK: $checked docs pass status-block + resolved-link + cross-reference checks"
  if [ "$stale_warnings" -gt 0 ]; then
    msg="$msg (with $stale_warnings stale-doc warning(s) above — non-failing rolling-tier)"
  fi
  echo "$msg"
  if [ "$legacy_stamps" -gt 0 ]; then
    echo "NOTE: $legacy_stamps doc(s) still carry a pre-CalVer (v1.0.x) 'Last verified' stamp — grandfathered from the staleness gate; re-stamp to YYYY.MMDD.HHMM when next verified."
  fi
  exit 0
else
  echo ""
  if [ "$stale_failures" -gt 0 ]; then
    echo "Doc lint failed ($stale_failures contract-tier stale-doc failure(s) above)."
  else
    echo "Doc lint failed."
  fi
  echo "See docs/doc-spec.md §3 + §6.1 for the contract."
  exit 1
fi
