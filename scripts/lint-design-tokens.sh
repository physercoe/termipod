#!/usr/bin/env bash
# lint-design-tokens.sh — a forward-only ratchet on design-system drift.
#
# Enforces ADR-047 (design-system enforcement) the way lint-legacy-markers.sh
# enforces compatibility debt: the tree already carries hundreds of off-token
# values, so we DON'T hard-ban them — we grandfather today's counts into a
# committed baseline and FAIL the build only when a category's count RISES
# above its baseline. New code must reach for the tokens
# (lib/theme/tokens.dart) / DesignColors / AppChip; the existing backlog burns
# down opportunistically (WS6), and each burn-down PR ratchets the baseline
# down with --update.
#
# Categories counted across lib/**.dart (occurrence counts, not line counts):
#   private_chip_class    `class _Foo(Chip|Pill)` — collapse into AppChip (D-7)
#   raw_material_color    Colors.(grey|red|green|orange|amber) — use DesignColors (D-5)
#   stray_hex_color       Color(0x........) outside the palette allowlist (D-5)
#   off_scale_radius      BorderRadius.circular(N), N not on the M3 scale (D-3)
#   off_scale_font_size   fontSize: N, N not on the type scale (D-4)
#   off_grid_edge_inset   EdgeInsets.* numeric arg off the 4px grid (D-2)
#   box_shadow            boxShadow — use tonal elevation (D-8)
#
# Palette files legitimately hold raw hex (the token definitions + the ANSI /
# terminal colour tables) and are excluded from stray_hex_color only.
#
# Usage:
#   scripts/lint-design-tokens.sh            # check (CI mode)
#   scripts/lint-design-tokens.sh --update   # regenerate the baseline
#
# Exit 0 clean; 1 on a category over baseline (or a stale/missing baseline in
# check mode). Baseline: scripts/design-token-baseline.txt.

set -u
cd "$(dirname "$0")/.."

BASELINE="scripts/design-token-baseline.txt"
MODE="check"
case "${1:-}" in
  --update) MODE="update" ;;
  -h|--help) sed -n '2,40p' "$0"; exit 0 ;;
  "") ;;
  *) echo "lint-design-tokens: unknown arg: $1" >&2; exit 2 ;;
esac

python3 - "$BASELINE" "$MODE" <<'PYEOF'
import os
import re
import sys

baseline_path, mode = sys.argv[1], sys.argv[2]

# On-scale value sets (ADR-047 D-2/D-3/D-4).
RADIUS_OK = {4, 8, 12, 16, 999}            # M3 shape scale + stadium sentinel
FONT_OK = {11, 12, 13, 14, 16, 18, 20}     # 6-step type scale
GRID_OK = {0, 2, 4, 8, 12, 16, 24, 32}     # 4px spacing grid (+ 0, +2 hairline)

# Files that legitimately define raw hex colours (the token source + the
# ANSI/terminal palettes). Excluded from stray_hex_color only.
HEX_ALLOW = {
    "lib/theme/design_colors.dart",
    "lib/theme/terminal_colors.dart",
    "lib/services/terminal/ansi_parser.dart",
    "lib/screens/terminal/widgets/ansi_text_view.dart",
    "lib/screens/terminal/terminal_screen.dart",
}

re_chip = re.compile(r"class\s+_[A-Za-z0-9]*(?:Chip|Pill)\b")
re_color = re.compile(r"\bColors\.(?:grey|red|green|orange|amber)\b")
re_hex = re.compile(r"\bColor\(0x[0-9a-fA-F]{6,8}\)")
re_radius = re.compile(r"BorderRadius\.circular\(\s*(\d+(?:\.\d+)?)\s*\)")
re_font = re.compile(r"fontSize:\s*(\d+(?:\.\d+)?)")
re_edge = re.compile(r"EdgeInsets\.(?:all|symmetric|fromLTRB|only)\(([^)]*)\)")
re_num = re.compile(r"(?<![\w.])(\d+(?:\.\d+)?)")
re_box = re.compile(r"\bboxShadow\b")

CATS = [
    "private_chip_class",
    "raw_material_color",
    "stray_hex_color",
    "off_scale_radius",
    "off_scale_font_size",
    "off_grid_edge_inset",
    "box_shadow",
]


def on(values, n):
    """Is numeric string n on the given value set (integer-valued)."""
    try:
        f = float(n)
    except ValueError:
        return True  # unparseable → don't flag
    return f != int(f) or int(f) in values


def count_file(path, text, counts):
    counts["private_chip_class"] += len(re_chip.findall(text))
    counts["raw_material_color"] += len(re_color.findall(text))
    counts["box_shadow"] += len(re_box.findall(text))
    if path not in HEX_ALLOW:
        counts["stray_hex_color"] += len(re_hex.findall(text))
    for n in re_radius.findall(text):
        if not on(RADIUS_OK, n):
            counts["off_scale_radius"] += 1
    for n in re_font.findall(text):
        if not on(FONT_OK, n):
            counts["off_scale_font_size"] += 1
    for args in re_edge.findall(text):
        for n in re_num.findall(args):
            if not on(GRID_OK, n):
                counts["off_grid_edge_inset"] += 1


def scan():
    counts = {c: 0 for c in CATS}
    for dirpath, _dirs, files in os.walk("lib"):
        for fn in files:
            if not fn.endswith(".dart"):
                continue
            path = os.path.join(dirpath, fn)
            try:
                with open(path, encoding="utf-8") as fh:
                    text = fh.read()
            except OSError:
                continue
            count_file(path, text, counts)
    return counts


current = scan()

if mode == "update":
    with open(baseline_path, "w", encoding="utf-8") as fh:
        fh.write(
            "# design-token baseline (ADR-047) — grandfathered off-token counts.\n"
            "# Regenerate with: scripts/lint-design-tokens.sh --update\n"
            "# One <category>\\t<count> per line. Counts may only DECREASE —\n"
            "# lowering them as the backlog burns down (WS6) is the point.\n"
        )
        for c in CATS:
            fh.write(f"{c}\t{current[c]}\n")
    print(f"lint-design-tokens: wrote baseline ({sum(current.values())} total)")
    sys.exit(0)

# check mode
if not os.path.exists(baseline_path):
    print(f"FAIL: baseline {baseline_path} missing — run "
          f"scripts/lint-design-tokens.sh --update", file=sys.stderr)
    sys.exit(1)

baseline = {}
with open(baseline_path, encoding="utf-8") as fh:
    for ln in fh:
        ln = ln.rstrip("\n")
        if not ln or ln.startswith("#"):
            continue
        if "\t" in ln:
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

if failed:
    print()
    print("A design-token category rose above its baseline (ADR-047). New code "
          "must use lib/theme/tokens.dart / DesignColors / AppChip instead of")
    print("an ad-hoc literal. If this is a deliberate, reviewed exception, "
          "regenerate the baseline: scripts/lint-design-tokens.sh --update")
    sys.exit(1)

print(f"lint-design-tokens: clean ({sum(current.values())} total off-token, "
      f"all categories <= baseline)")
for c, base, cur in ratcheted:
    print(f"  note: {c} dropped {base} -> {cur} — run --update to ratchet it down")
PYEOF
