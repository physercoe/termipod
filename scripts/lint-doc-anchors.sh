#!/usr/bin/env bash
# lint-doc-anchors.sh — verify the `<!-- verify ... -->` claim
# markers embedded in docs against current code. Per docs/doc-spec.md §6.2.
#
# Four marker kinds (MVP):
#
#   <!-- verify file <path> -->
#       Checks <path> exists. Use for migration files, fixed config
#       files, anything where the existence of a specific filename
#       is the load-bearing fact in the prose.
#
#   <!-- verify no-file <glob> -->
#       Checks NO files match <glob>. Use to assert a slot is free
#       (e.g. "migration 0045 is the next unused slot").
#
#   <!-- verify symbol <file> <name> -->
#       Checks <name> appears in <file> as a word-boundary match.
#       Use for function names, struct fields, method names —
#       symbols that survive line-shuffling. Drops the line-ref
#       brittleness of `file:line` prose by anchoring to the symbol
#       itself.
#
#   <!-- verify glob <pattern> <count> -->
#       Checks <count> files match <pattern>. Use for "there are N
#       bundled steward templates" claims that drift when the count
#       changes.
#
# The markers are HTML comments — invisible in rendered Markdown,
# visible in source. Line numbers in prose stay as navigation hints;
# the authoritative check is the symbol presence.
#
# Run from repo root:   scripts/lint-doc-anchors.sh
# CI usage:             added as a step in .github/workflows/ci.yml
#
# Exits 0 on clean, 1 on any broken anchor.

set -u

cd "$(dirname "$0")/.."

# Implementation in python3 — the regex extraction + per-kind
# dispatch is fiddlier than is comfortable in pure shell.
# The script reads every *.md under docs/ (except archive/, which
# is frozen by convention and may legitimately cite dead refs).

python3 - <<'PYEOF'
import glob
import os
import re
import sys

ROOT = "."

# Walk docs/ for markdown files. Mirrors lint-docs.sh's exclusion
# list — archive/ is frozen; screens/ and logo/ are non-prose
# artefact directories.
EXCLUDED_PREFIXES = ("docs/archive/", "docs/screens/", "docs/logo/")

# Marker regex. Tightened to ONLY match one of the four legal kinds
# so placeholder syntax in prose ("`<!-- verify KIND ARGS -->`",
# "`<!-- verify ... -->`") doesn't false-positive — those tokens
# aren't legal kinds and so don't match the alternation.
# Permissive on whitespace inside the comment so authors can format
# multi-arg markers with breathing room:
#   <!-- verify symbol hub/.../foo.go bar -->
#   <!--verify symbol hub/.../foo.go bar-->
# Both shapes parse identically.
LEGAL_KINDS = {"file", "no-file", "symbol", "glob"}
_KINDS_ALT = "|".join(re.escape(k) for k in sorted(LEGAL_KINDS, key=len, reverse=True))
MARKER_RE = re.compile(
    r"<!--\s*verify\s+(" + _KINDS_ALT + r")(.*?)-->",
    re.DOTALL,
)

# Per-kind required arg count (positional, whitespace-split).
ARG_COUNTS = {
    "file":     1,
    "no-file":  1,
    "symbol":   2,
    "glob":     2,
}

failed = 0
checked = 0
docs_with_anchors = 0


def doc_paths():
    for dirpath, _, filenames in os.walk(os.path.join(ROOT, "docs")):
        # Strip the leading "./" os.walk introduces so prefix checks
        # against EXCLUDED_PREFIXES work on the canonical form
        # `docs/foo/bar.md`.
        rel_dir = os.path.relpath(dirpath, ROOT)
        if rel_dir == ".":
            rel_dir = ""
        for fn in filenames:
            if not fn.endswith(".md"):
                continue
            path = os.path.join(rel_dir, fn) if rel_dir else fn
            if any(path.startswith(p) for p in EXCLUDED_PREFIXES):
                continue
            yield path


def check_file(path):
    return os.path.isfile(path)


def check_no_file(pattern):
    # glob.glob returns [] when nothing matches, which is the
    # success condition for `no-file`.
    return len(glob.glob(pattern)) == 0


def check_symbol(file_path, name):
    if not os.path.isfile(file_path):
        return False, f"file not found: {file_path}"
    # Word-boundary match — catches function names, struct fields,
    # method names, const idents. Compile per-call (cheap; we're
    # not in a tight loop).
    pat = re.compile(r"\b" + re.escape(name) + r"\b")
    try:
        with open(file_path, "r", encoding="utf-8", errors="replace") as fh:
            for line in fh:
                if pat.search(line):
                    return True, ""
    except OSError as e:
        return False, f"read error: {e}"
    return False, f"symbol not found: {name!r}"


def check_glob(pattern, expected_count_str):
    try:
        expected = int(expected_count_str)
    except ValueError:
        return False, f"count is not an integer: {expected_count_str!r}"
    actual = len(glob.glob(pattern))
    if actual != expected:
        return False, f"glob matched {actual} files, expected {expected}"
    return True, ""


for doc in sorted(doc_paths()):
    try:
        with open(doc, "r", encoding="utf-8", errors="replace") as fh:
            body = fh.read()
    except OSError as e:
        print(f"FAIL [read]: {doc} — {e}")
        failed = 1
        continue

    matches = list(MARKER_RE.finditer(body))
    if not matches:
        continue
    docs_with_anchors += 1

    for m in matches:
        kind = m.group(1)
        tail = m.group(2).strip()
        args = tail.split() if tail else []
        checked += 1

        # `kind not in LEGAL_KINDS` is unreachable here — the regex
        # alternation enforces it. Kept the LEGAL_KINDS set as the
        # single source of truth referenced by the regex builder
        # above so a new kind only needs adding in one place.
        assert kind in LEGAL_KINDS, kind  # belt + suspenders

        expected_args = ARG_COUNTS[kind]
        if len(args) != expected_args:
            print(f"FAIL [bad-args]: {doc} — `verify {kind}` expects {expected_args} args, got {len(args)}: {args!r}")
            failed = 1
            continue

        ok = False
        detail = ""
        try:
            if kind == "file":
                ok = check_file(args[0])
                if not ok:
                    detail = f"file does not exist: {args[0]}"
            elif kind == "no-file":
                ok = check_no_file(args[0])
                if not ok:
                    matched = glob.glob(args[0])
                    detail = f"glob matched {len(matched)} file(s) (expected 0): {matched[:3]}"
            elif kind == "symbol":
                ok, detail = check_symbol(args[0], args[1])
            elif kind == "glob":
                ok, detail = check_glob(args[0], args[1])
        except Exception as e:
            ok = False
            detail = f"unexpected error: {type(e).__name__}: {e}"

        if not ok:
            print(f"FAIL [broken-anchor]: {doc} — `verify {kind} {' '.join(args)}` → {detail}")
            failed = 1

if failed:
    print("")
    print(f"Doc anchor lint failed. See docs/doc-spec.md §6.2 for the marker contract.")
    sys.exit(1)

if docs_with_anchors == 0:
    print(f"OK: 0 docs carry verify-anchor markers yet (Tier 2 adoption pending; see docs/discussions/doc-freshness-maintenance.md §4)")
else:
    print(f"OK: {checked} anchor(s) across {docs_with_anchors} doc(s) verified")
sys.exit(0)
PYEOF
