#!/usr/bin/env bash
# lint-desktop-tokens.sh — a forward-only ratchet on desktop design-token drift.
#
# The desktop workbench (desktop/) has its OWN token layer, separate from the
# mobile app's Dart tokens (which lint-design-tokens.sh governs). Its generated
# primitives live in desktop/src/styles/tokens.css (--color-*, --font-size-*,
# --radius-*, --spacing-*); the SEMANTIC layer in partials/01-base-shell.css maps
# those into named aliases (--bg, --text, --accent, --raised, …). Component CSS
# and inline React styles should reference the SEMANTIC layer, never a raw hex or
# a raw --color-* primitive.
#
# The tree already carries grandfathered leaks, so — like the mobile ratchet — we
# DON'T hard-ban them: today's counts are pinned in a committed baseline and the
# build FAILS only when a category RISES above its baseline. Burn the backlog down
# opportunistically and ratchet the baseline with --update.
#
# Ratcheted categories (occurrence counts):
#   css_hex             raw #rgb/#rrggbb in partials/*.css (use a token). The
#                       token source (01-base-shell.css) + tokens.css are excluded.
#   css_primitive_var   var(--color-*) in partials/*.css — reaching past the
#                       semantic layer to a primitive. base-shell (the alias
#                       source) is excluded.
#   tsx_hex             raw hex in inline styles / literals in src/**/*.tsx.
#
# Hard check (must be ZERO, not ratcheted):
#   phantom_token       var(--name) where --name is defined nowhere in the CSS —
#                       a silently-load-bearing fallback. Allowlisted: runtime-
#                       injected properties set from JS.
#
# Usage:
#   scripts/lint-desktop-tokens.sh            # check (CI mode)
#   scripts/lint-desktop-tokens.sh --update   # regenerate the baseline
#
# Exit 0 clean; 1 on a category over baseline, a phantom token, or a stale/missing
# baseline in check mode. Baseline: scripts/desktop-token-baseline.txt.

set -u
cd "$(dirname "$0")/.."

BASELINE="scripts/desktop-token-baseline.txt"
MODE="check"
case "${1:-}" in
  --update) MODE="update" ;;
  -h|--help) sed -n '2,44p' "$0"; exit 0 ;;
  "") ;;
  *) echo "lint-desktop-tokens: unknown arg: $1" >&2; exit 2 ;;
esac

python3 - "$BASELINE" "$MODE" <<'PYEOF'
import os
import re
import sys

baseline_path, mode = sys.argv[1], sys.argv[2]

PARTIALS = "desktop/src/styles/partials"
STYLES = "desktop/src/styles"
SRC = "desktop/src"

# The token-definition layer legitimately holds raw hex + raw --color-* refs.
HEX_ALLOW = {"01-base-shell.css", "tokens.css"}
PRIMVAR_ALLOW = {"01-base-shell.css"}
# Custom properties set at runtime from JS (never defined in CSS) — not phantoms.
PHANTOM_ALLOW = {"--scale-factor"}

re_hex = re.compile(r"#[0-9a-fA-F]{3,8}\b")
re_primvar = re.compile(r"var\(\s*--color-[a-z0-9-]+")
re_var = re.compile(r"var\(\s*(--[a-z0-9-]+)")
re_def = re.compile(r"(--[a-z0-9-]+)\s*:")

CATS = ["css_hex", "css_primitive_var", "tsx_hex"]


def read(path):
    try:
        with open(path, encoding="utf-8") as fh:
            return fh.read()
    except OSError:
        return ""


def scan():
    counts = {c: 0 for c in CATS}
    defs, refs = set(), set()
    # CSS partials
    for fn in sorted(os.listdir(PARTIALS)):
        if not fn.endswith(".css"):
            continue
        text = read(os.path.join(PARTIALS, fn))
        if fn not in HEX_ALLOW:
            counts["css_hex"] += len(re_hex.findall(text))
        if fn not in PRIMVAR_ALLOW:
            counts["css_primitive_var"] += len(re_primvar.findall(text))
    # token definitions + references, across all CSS + tsx (for phantom detection)
    for dirpath, _dirs, files in os.walk(STYLES):
        for fn in files:
            if fn.endswith(".css"):
                text = read(os.path.join(dirpath, fn))
                defs.update(re_def.findall(text))
                refs.update(re_var.findall(text))
    for dirpath, _dirs, files in os.walk(SRC):
        for fn in files:
            if fn.endswith(".tsx"):
                text = read(os.path.join(dirpath, fn))
                counts["tsx_hex"] += len(re_hex.findall(text))
                refs.update(re_var.findall(text))
    phantom = sorted(r for r in refs if r not in defs and r not in PHANTOM_ALLOW)
    return counts, phantom


current, phantom = scan()

if mode == "update":
    with open(baseline_path, "w", encoding="utf-8") as fh:
        fh.write(
            "# desktop-token baseline (#318) — grandfathered off-token counts.\n"
            "# Regenerate with: scripts/lint-desktop-tokens.sh --update\n"
            "# One <category>\\t<count> per line. Counts may only DECREASE.\n"
        )
        for c in CATS:
            fh.write(f"{c}\t{current[c]}\n")
    print(f"lint-desktop-tokens: wrote baseline ({sum(current.values())} total)")
    if phantom:
        print("WARNING: phantom tokens present (fix these — not ratcheted):")
        for p in phantom:
            print(f"  {p}")
    sys.exit(0)

# check mode
if not os.path.exists(baseline_path):
    print(f"FAIL: baseline {baseline_path} missing — run "
          f"scripts/lint-desktop-tokens.sh --update", file=sys.stderr)
    sys.exit(1)

baseline = {}
with open(baseline_path, encoding="utf-8") as fh:
    for ln in fh:
        ln = ln.rstrip("\n")
        if not ln or ln.startswith("#") or "\t" not in ln:
            continue
        c, n = ln.split("\t", 1)
        baseline[c] = int(n)

failed = 0
ratcheted = []
for c in CATS:
    base = baseline.get(c, 0)
    cur = current[c]
    if cur > base:
        print(f"FAIL [{c}]: {cur} > baseline {base} (+{cur - base})")
        failed = 1
    elif cur < base:
        ratcheted.append((c, base, cur))

if phantom:
    failed = 1
    print("FAIL [phantom_token]: var(--name) referenced but defined nowhere:")
    for p in phantom:
        print(f"  {p}")

if failed:
    print()
    print("Desktop design-token drift (#318). Component CSS/TSX must use the "
          "semantic tokens in partials/01-base-shell.css, not a raw hex or a raw")
    print("--color-* primitive. A phantom token is a bug — define it in the "
          "semantic layer. Deliberate, reviewed exception? Regenerate the "
          "baseline: scripts/lint-desktop-tokens.sh --update")
    sys.exit(1)

print(f"lint-desktop-tokens: clean ({sum(current.values())} total off-token, "
      f"all categories <= baseline, no phantom tokens)")
for c, base, cur in ratcheted:
    print(f"  note: {c} dropped {base} -> {cur} — run --update to ratchet it down")
PYEOF
