#!/usr/bin/env bash
# lint-legacy-markers.sh — a forward-only ratchet on compatibility debt.
#
# Inverts the tech-debt review's "compatibility ledger" idea (WS1.5 of
# docs/plans/internal-techdebt-cleanup.md): instead of cataloguing debt to
# carry, we FAIL the build when a NEW `legacy` / `deprecated` / `alias` marker
# appears in a comment — unless that comment names a removal target (so a
# deliberate, documented deprecation is still allowed).
#
# Why a ratchet and not a hard ban: the tree already carries ~hundreds of these
# words, most as legitimate *descriptive* prose ("the legacy team-less path",
# a permanent fallback — not debt to remove). Forcing those to all carry a
# removal target would be noise. So the existing set is grandfathered into a
# baseline; the linter only fails on additions beyond it.
#
# Scope: comments in hub/**.go (excluding _test.go — test fixtures legitimately
# reference retired shapes) and lib/**.dart. Identity is keyed by
# (relpath, normalized-comment-text), NOT line number, so unrelated edits that
# shift lines don't churn the baseline.
#
# A new marker is EXEMPT (allowed without a baseline edit) when its comment
# NAMES A REMOVAL TARGET — it contains a removal verb (retire/remove/delete/
# drop/sunset/kill/purge, any inflection) or an issue/ticket ref (#123). That
# is the signal that the debt is tracked, not silently accruing.
#
# Usage:
#   scripts/lint-legacy-markers.sh            # check (CI mode)
#   scripts/lint-legacy-markers.sh --update   # regenerate the baseline
#
# Exit 0 clean; 1 on a new un-targeted marker (or a stale/missing baseline in
# check mode). The baseline lives at scripts/legacy-marker-baseline.txt.

set -u
cd "$(dirname "$0")/.."

BASELINE="scripts/legacy-marker-baseline.txt"
MODE="check"
case "${1:-}" in
  --update) MODE="update" ;;
  -h|--help) sed -n '2,33p' "$0"; exit 0 ;;
  "") ;;
  *) echo "lint-legacy-markers: unknown arg: $1" >&2; exit 2 ;;
esac

python3 - "$BASELINE" "$MODE" <<'PYEOF'
import os
import re
import sys

baseline_path, mode = sys.argv[1], sys.argv[2]

# Marker words — whole-word, case-insensitive. `alias`/`aliases` both match.
marker_re = re.compile(r"\b(legacy|deprecated|alias(?:es)?)\b", re.IGNORECASE)
# A removal target named in the same comment: a removal verb (any inflection)
# or an issue/ticket reference. This is what makes a NEW marker allowed.
target_re = re.compile(
    r"(retir|remov|delet|\bdrop(?:s|ped|ping)?\b|sunset|\bkill(?:s|ed|ing)?\b|purg|#\d+)",
    re.IGNORECASE,
)


def comment_of(line, ext):
    """Return the comment text of a source line, or None if it has none.
    Go/Dart both use //; Dart doc-comments use ///. We do not parse string
    literals (Go/Dart style keeps // out of non-comment strings in practice),
    matching the crude-but-sufficient approach in lint-governed-actions.sh."""
    idx = line.find("//")
    if idx < 0:
        return None
    return line[idx + 2:]


def norm(text):
    return " ".join(text.strip().lower().split())


def scan():
    """Yield (relpath, normtext, names_target) for every marker comment."""
    roots = [("hub", ".go"), ("lib", ".dart")]
    for root, ext in roots:
        for dirpath, _dirs, files in os.walk(root):
            for fn in files:
                if not fn.endswith(ext):
                    continue
                if ext == ".go" and fn.endswith("_test.go"):
                    continue
                path = os.path.join(dirpath, fn)
                try:
                    with open(path, encoding="utf-8") as fh:
                        lines = fh.readlines()
                except OSError:
                    continue
                for line in lines:
                    c = comment_of(line, ext)
                    if c is None or not marker_re.search(c):
                        continue
                    yield (path, norm(c), bool(target_re.search(c)))


current = {}  # (relpath, normtext) -> names_target
for relpath, normtext, names_target in scan():
    current[(relpath, normtext)] = names_target

if mode == "update":
    lines = sorted(f"{p}\t{t}" for (p, t) in current)
    with open(baseline_path, "w", encoding="utf-8") as fh:
        fh.write(
            "# legacy-marker baseline — grandfathered compatibility markers.\n"
            "# Regenerate with: scripts/lint-legacy-markers.sh --update\n"
            "# One <relpath>\\t<normalized-comment> per line. Shrinking this\n"
            "# file (removing retired markers) is the point; growing it should\n"
            "# be rare and deliberate.\n"
        )
        for ln in lines:
            fh.write(ln + "\n")
    print(f"lint-legacy-markers: wrote baseline with {len(lines)} entr(ies)")
    sys.exit(0)

# check mode
if not os.path.exists(baseline_path):
    print(f"FAIL: baseline {baseline_path} missing — run "
          f"scripts/lint-legacy-markers.sh --update", file=sys.stderr)
    sys.exit(1)

baseline = set()
with open(baseline_path, encoding="utf-8") as fh:
    for ln in fh:
        ln = ln.rstrip("\n")
        if not ln or ln.startswith("#"):
            continue
        if "\t" in ln:
            p, t = ln.split("\t", 1)
            baseline.add((p, t))

failed = 0
new_untargeted = []
for key, names_target in sorted(current.items()):
    if key in baseline:
        continue
    if names_target:
        continue  # new but documents a removal target — allowed
    new_untargeted.append(key)

for relpath, normtext in new_untargeted:
    snippet = normtext if len(normtext) <= 100 else normtext[:97] + "..."
    print(f"FAIL [new-untargeted-marker]: {relpath}: \"{snippet}\"")
    failed = 1

if failed:
    print()
    print("A new legacy/deprecated/alias marker appeared without naming a "
          "removal target.")
    print("Either name the removal target in the comment (a removal verb like "
          "'retire/remove/delete', or an issue ref like #123),")
    print("or — if this is permanent descriptive prose — grandfather it with: "
          "scripts/lint-legacy-markers.sh --update")
    sys.exit(1)

# Opportunistic ratchet-down hint (non-fatal): baseline entries that no longer
# exist can be pruned so the debt count visibly shrinks.
stale = len(baseline - set(current.keys()))
print(f"lint-legacy-markers: clean (baseline={len(baseline)}, "
      f"current={len(current)})")
if stale:
    print(f"  note: {stale} baseline entr(ies) no longer present — run "
          f"--update to prune and ratchet the count down")
PYEOF
