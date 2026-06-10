#!/usr/bin/env bash
# lint-arb.sh — keep the gen-l10n ARB files honest (issue #138 tooling, WS-B).
#
# gen-l10n silently falls back to the template (en) when a zh key is missing,
# so a dropped translation ships as English with no build error. And a renamed
# placeholder in a zh string throws only at runtime. This gate makes both
# load-bearing, offline (no Flutter SDK):
#
#   1. valid JSON                — both app_en.arb and app_zh.arb parse.
#   2. key-set equality          — en and zh define the same resource keys.
#   3. placeholder consistency   — each key's {placeholder} set matches across
#                                  en/zh, and matches the @key.placeholders
#                                  metadata where declared.
#   4. orphan metadata           — every `@key` has a matching resource `key`.
#
# Pure bash + python3, like lint-design-tokens.sh / lint-vocab.sh.
# Exit 0 clean; 1 on any divergence.

set -u
cd "$(dirname "$0")/.."

python3 - lib/l10n/app_en.arb lib/l10n/app_zh.arb <<'PYEOF'
import json
import re
import sys

en_path, zh_path = sys.argv[1], sys.argv[2]

# Simple placeholder: `{name}` with name immediately closed — deliberately
# does NOT match ICU openers like `{count, plural, ...}` (comma) or plural
# arms like `{1 window}` (space), only true substitution points.
PH = re.compile(r"\{([A-Za-z_][A-Za-z0-9_]*)\}")


def load(path):
    with open(path, encoding="utf-8") as fh:
        return json.load(fh)


failed = 0


def fail(msg):
    global failed
    print(f"FAIL: {msg}")
    failed = 1


try:
    en = load(en_path)
except (OSError, json.JSONDecodeError) as e:
    print(f"FAIL: {en_path} is not valid JSON: {e}", file=sys.stderr)
    sys.exit(1)
try:
    zh = load(zh_path)
except (OSError, json.JSONDecodeError) as e:
    print(f"FAIL: {zh_path} is not valid JSON: {e}", file=sys.stderr)
    sys.exit(1)


def resource_keys(d):
    return {k for k in d if not k.startswith("@")}


en_keys = resource_keys(en)
zh_keys = resource_keys(zh)

# 2. key-set equality
for k in sorted(en_keys - zh_keys):
    fail(f"key '{k}' in app_en.arb but missing from app_zh.arb")
for k in sorted(zh_keys - en_keys):
    fail(f"key '{k}' in app_zh.arb but missing from app_en.arb (orphan zh)")

# 4. orphan metadata (in both files)
for label, d in (("app_en.arb", en), ("app_zh.arb", zh)):
    keys = resource_keys(d)
    for k in d:
        if k.startswith("@@"):
            continue
        if k.startswith("@") and k[1:] not in keys:
            fail(f"metadata '{k}' in {label} has no resource key '{k[1:]}'")

# 3. placeholder consistency for shared keys
for k in sorted(en_keys & zh_keys):
    ev, zv = en[k], zh[k]
    if not isinstance(ev, str) or not isinstance(zv, str):
        continue
    en_ph = set(PH.findall(ev))
    zh_ph = set(PH.findall(zv))
    if en_ph != zh_ph:
        fail(f"key '{k}' placeholder mismatch: en {sorted(en_ph)} "
             f"vs zh {sorted(zh_ph)}")
        continue
    # declared placeholders (template-side) must be referenced in both
    meta = en.get("@" + k)
    if isinstance(meta, dict):
        declared = set((meta.get("placeholders") or {}).keys())
        for missing in sorted(declared - en_ph):
            fail(f"key '{k}' declares placeholder '{missing}' but en value "
                 f"never uses {{{missing}}}")
        for missing in sorted(declared - zh_ph):
            fail(f"key '{k}' declares placeholder '{missing}' but zh value "
                 f"never uses {{{missing}}}")

if failed:
    print()
    print("ARB drift (#138). gen-l10n falls back to en silently, so missing "
          "zh keys / mismatched placeholders ship as bugs. Fix lib/l10n/"
          "app_{en,zh}.arb so the two stay in lockstep.")
    sys.exit(1)

print(f"lint-arb: clean ({len(en_keys)} resource keys, en/zh in lockstep)")
PYEOF
