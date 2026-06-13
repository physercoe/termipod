#!/usr/bin/env bash
# lint-hardcoded-strings.sh — a forward-only ratchet on un-localized UI text.
#
# Issue #138 keeps reopening because nothing fails the build when a screen
# ships a raw `Text('English')`. lint-arb.sh only checks ARB key lockstep;
# it cannot see a literal that never reached the ARB files. This gate, like
# lint-design-tokens.sh, grandfathers today's backlog into a committed
# baseline and FAILS only when a file's count of hardcoded UI strings RISES
# above its baseline. New screens must route text through AppLocalizations
# (+ the vocabulary axes for entity/role nouns); the existing backlog burns
# down per surface and each burn-down PR ratchets the baseline with --update.
#
# Heuristic (UI layer only — lib/screens/**, lib/widgets/**, excl _test/.g):
#   * a quoted literal handed to a text sink: Text(...), label:, labelText:,
#     hintText:, tooltip:, helperText:, semanticLabel:, Tab(text:), text:.
#   * a capitalized-prose literal returned as a label: `return 'Foo bar'` /
#     `=> 'Foo'` (catches switch/case label helpers — scope/status/category
#     labels that never appear directly in a Text() call).
# A literal counts only if it reads like prose: contains a letter, and either
# has a space or an interior uppercase or is a known UI word. Pure separators
# (' · '), wire values ('todo'), interpolation-only, and ALL_CAPS section
# headers handled by tuning + the baseline (false positives just get
# grandfathered; the gate's job is to stop NEW drift, not to be perfect).
#
# Usage:
#   scripts/lint-hardcoded-strings.sh            # check (CI mode)
#   scripts/lint-hardcoded-strings.sh --update   # regenerate the baseline
#   scripts/lint-hardcoded-strings.sh --list     # print every violation w/ file:line
#
# Exit 0 clean; 1 on a file over baseline (or a stale/missing baseline in
# check mode). Baseline: scripts/hardcoded-strings-baseline.txt.

set -u
cd "$(dirname "$0")/.."

BASELINE="scripts/hardcoded-strings-baseline.txt"
MODE="check"
case "${1:-}" in
  --update) MODE="update" ;;
  --list)   MODE="list" ;;
  -h|--help) sed -n '2,33p' "$0"; exit 0 ;;
  "") ;;
  *) echo "lint-hardcoded-strings: unknown arg: $1" >&2; exit 2 ;;
esac

python3 - "$BASELINE" "$MODE" <<'PYEOF'
import os
import re
import sys

baseline_path, mode = sys.argv[1], sys.argv[2]

ROOTS = ("lib/screens", "lib/widgets")

# Files that legitimately carry technical/diagnostic English not meant for
# preset theming (operator dashboards, raw-wire passthroughs). Excluded
# wholesale; revisit if a surface graduates into the localized set.
FILE_ALLOW = {
    "lib/widgets/transcript/telemetry_strip.dart",   # operator diagnostics (formatter i18n)
}

# Text sinks: a quoted literal passed to a widget that renders it.
SINK = re.compile(
    r"""(?:Text\(\s*|Tab\(\s*text:\s*|\b(?:label|labelText|hintText|helperText|tooltip|semanticLabel|text)\s*:\s*)"""
    r"""(['"])([^'"]{1,80})\1"""
)
# Label-returning helpers: `return 'Foo'` / `=> 'Foo'` with capitalized prose.
RET = re.compile(r"""(?:return|=>)\s+(['"])([A-Z][^'"]{0,60})\1""")

# A literal is "prose" (user-facing) when it has a letter and either a space,
# an interior capital (camel/Title), or sentence punctuation — but is not a
# bare wire token, separator, or interpolation fragment.
WIRE = re.compile(r"^[a-z][a-z0-9_]*$")          # todo, in_progress, scope_kind
SEP = re.compile(r"^[\W\d_]*$")                   # ' · ', '/', '—', '...'
HAS_LETTER = re.compile(r"[A-Za-z]")


def is_prose(s: str) -> bool:
    if not HAS_LETTER.search(s):
        return False
    if SEP.match(s):
        return False
    if WIRE.match(s):                            # raw wire value
        return False
    if s.startswith("\\$") or s.startswith("$"):  # pure interpolation
        return False
    # prose-ish: a space, OR a Title/Capitalized word, OR an interior capital
    if " " in s:
        return True
    if s[:1].isupper():
        return True
    return False


def iter_files():
    for root in ROOTS:
        for dirpath, _dirs, files in os.walk(root):
            for fn in files:
                if not fn.endswith(".dart"):
                    continue
                if fn.endswith("_test.dart") or fn.endswith(".g.dart"):
                    continue
                p = os.path.join(dirpath, fn)
                if p in FILE_ALLOW:
                    continue
                yield p


def violations(path):
    out = []
    with open(path, encoding="utf-8") as fh:
        for i, line in enumerate(fh, 1):
            stripped = line.lstrip()
            if stripped.startswith("//") or stripped.startswith("///") or stripped.startswith("*"):
                continue
            seen = set()
            for rx in (SINK, RET):
                for m in rx.finditer(line):
                    s = m.group(2)
                    if s in seen:
                        continue
                    if is_prose(s):
                        seen.add(s)
                        out.append((i, s))
    return out


counts = {}
detail = {}
for p in iter_files():
    v = violations(p)
    if v:
        counts[p] = len(v)
        detail[p] = v

if mode == "list":
    for p in sorted(detail):
        for ln, s in detail[p]:
            print(f"{p}:{ln}: {s}")
    total = sum(counts.values())
    print(f"\n# {total} hardcoded UI strings across {len(counts)} files", file=sys.stderr)
    sys.exit(0)

if mode == "update":
    with open(baseline_path, "w", encoding="utf-8") as fh:
        fh.write("# lint-hardcoded-strings baseline — per-file count of hardcoded UI\n")
        fh.write("# strings (issue #138). Forward-only ratchet: a file may not RISE\n")
        fh.write("# above its count. Burn a surface down, then --update to lower it.\n")
        fh.write("# Regenerate: scripts/lint-hardcoded-strings.sh --update\n")
        for p in sorted(counts):
            fh.write(f"{counts[p]} {p}\n")
    total = sum(counts.values())
    print(f"lint-hardcoded-strings: baseline written — {total} strings across {len(counts)} files")
    sys.exit(0)

# check mode
base = {}
if os.path.exists(baseline_path):
    with open(baseline_path, encoding="utf-8") as fh:
        for line in fh:
            line = line.strip()
            if not line or line.startswith("#"):
                continue
            n, _, p = line.partition(" ")
            base[p] = int(n)
else:
    print(f"lint-hardcoded-strings: missing baseline {baseline_path}; run --update", file=sys.stderr)
    sys.exit(1)

failed = 0
for p in sorted(counts):
    cur = counts[p]
    allowed = base.get(p, 0)
    if cur > allowed:
        failed = 1
        print(f"lint-hardcoded-strings: {p}: {cur} hardcoded UI strings (baseline {allowed})")
        for ln, s in detail[p]:
            print(f"    {p}:{ln}: {s!r}")
# A file that dropped to zero or below baseline is fine; flag stale baseline
# entries (file improved) only as info, not failure.
if failed:
    print("\nNew hardcoded UI text must go through AppLocalizations (+ vocab axes for", file=sys.stderr)
    print("entity/role nouns). If a string is deliberately technical, add the file to", file=sys.stderr)
    print("FILE_ALLOW. After burning a surface down, run --update to ratchet the baseline.", file=sys.stderr)
    sys.exit(1)

total = sum(counts.values())
btotal = sum(base.values())
print(f"lint-hardcoded-strings: clean ({total} grandfathered strings, baseline {btotal}; no file over)")
sys.exit(0)
PYEOF