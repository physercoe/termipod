#!/usr/bin/env bash
# lint-vocab.sh — completeness gate for the vocabulary-preset packs (ADR-048).
#
# The runtime resolves role-bound terms from `kVocabPacks` in
# lib/services/vocab/vocab_packs.dart, keyed by (preset, language). Resolution
# is fallback-tolerant, so a MISSING term degrades silently to tech/en instead
# of throwing — which means a gap would ship unnoticed. This lint makes the
# invariant load-bearing: every (preset × language) pack must define EVERY
# axis in lib/services/vocab/vocab_axis.dart.
#
# It is the local gate (no Flutter SDK needed); vocab_pack_test.dart proves the
# same invariant in CI with full type access. Pure bash + python3, like
# lint-design-tokens.sh.
#
# Exit 0 clean; 1 on any missing axis / missing pack / unknown axis key.

set -u
cd "$(dirname "$0")/.."

AXIS_FILE="lib/services/vocab/vocab_axis.dart"
PRESET_FILE="lib/services/vocab/vocab_preset.dart"
PACKS_FILE="lib/services/vocab/vocab_packs.dart"

python3 - "$AXIS_FILE" "$PRESET_FILE" "$PACKS_FILE" <<'PYEOF'
import re
import sys

axis_file, preset_file, packs_file = sys.argv[1], sys.argv[2], sys.argv[3]


def read(path):
    with open(path, encoding="utf-8") as fh:
        return fh.read()


# --- canonical axis set: enum members of VocabAxis ---
axis_text = read(axis_file)
m = re.search(r"enum\s+VocabAxis\s*\{(.*?)\}", axis_text, re.DOTALL)
if not m:
    print("FAIL: could not find `enum VocabAxis { ... }`", file=sys.stderr)
    sys.exit(1)
# Members look like `roleSteward('role.steward'),` — the quoted id arg
# distinguishes them from the `const VocabAxis(this.id)` constructor.
axes = set(re.findall(r"([a-zA-Z][a-zA-Z0-9]*)\s*\(\s*'[^']*'\s*\)", m.group(1)))
if not axes:
    print("FAIL: no axes parsed from VocabAxis", file=sys.stderr)
    sys.exit(1)

# --- canonical preset set: enum members of VocabPreset ---
preset_text = read(preset_file)
pm = re.search(r"enum\s+VocabPreset\s*\{(.*?)\n\}", preset_text, re.DOTALL)
presets = (set(re.findall(r"([a-zA-Z][a-zA-Z0-9]*)\s*\(\s*'[^']*'\s*\)",
                          pm.group(1))) if pm else set())
if not presets:
    print("FAIL: no presets parsed from VocabPreset", file=sys.stderr)
    sys.exit(1)

LANGS = ["en", "zh"]
expected_packs = {(p, l) for p in presets for l in LANGS}

# --- packs: split by the `// === pack: <preset> / <lang> ===` markers ---
packs_text = read(packs_file)
marker = re.compile(r"//\s*===\s*pack:\s*(\w+)\s*/\s*(\w+)\s*===")
hits = list(marker.finditer(packs_text))
if not hits:
    print("FAIL: no `// === pack: <preset> / <lang> ===` markers found",
          file=sys.stderr)
    sys.exit(1)

seen_packs = set()
failed = 0
for i, h in enumerate(hits):
    preset, lang = h.group(1), h.group(2)
    start = h.end()
    end = hits[i + 1].start() if i + 1 < len(hits) else len(packs_text)
    block = packs_text[start:end]
    keys = set(re.findall(r"VocabAxis\.([a-zA-Z][a-zA-Z0-9]*)\s*:", block))
    seen_packs.add((preset, lang))

    unknown = keys - axes
    if unknown:
        print(f"FAIL [{preset}/{lang}]: unknown axis key(s): "
              f"{', '.join(sorted(unknown))}")
        failed = 1
    missing = axes - keys
    if missing:
        print(f"FAIL [{preset}/{lang}]: missing axis term(s): "
              f"{', '.join(sorted(missing))}")
        failed = 1

absent = expected_packs - seen_packs
for preset, lang in sorted(absent):
    print(f"FAIL: no pack block for {preset}/{lang}")
    failed = 1

extra = seen_packs - expected_packs
for preset, lang in sorted(extra):
    print(f"FAIL: pack block {preset}/{lang} is not a known (preset, language)")
    failed = 1

if failed:
    print()
    print("Every (preset × language) pack must define every VocabAxis "
          "(ADR-048). Add the missing term(s) to lib/services/vocab/"
          "vocab_packs.dart, or remove the stray key.")
    sys.exit(1)

print(f"lint-vocab: clean ({len(axes)} axes × {len(expected_packs)} packs "
      f"all complete)")
PYEOF
