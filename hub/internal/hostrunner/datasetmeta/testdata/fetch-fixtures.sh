#!/usr/bin/env bash
# Refetch the LeRobot metadata fixtures from the Hugging Face Hub.
#
# These are REAL `meta/` trees, not hand-built ones. Hand-built fixtures agree
# with whatever the author assumed the format was, so they cannot catch the
# thing that actually breaks readers: a field the exporter really emits in a
# shape you did not predict (`names: null` on scalar features here; the gpt2
# `n_inner: null` bug in the archgraph round). Every commit below is pinned, so
# a rerun either reproduces the bytes in git or fails loudly.
#
# Usage:  bash fetch-fixtures.sh        # from this directory
#
# Fixture roster and what each one is here to prove:
#
#   nyu_rot_dataset @ v2.1 + v3.0  — the SAME dataset published in both
#     generations. 14 episodes of *varying* length (40/30/30/40…), so v3.0's
#     dataset_from_index/to_index offsets are non-uniform and an off-by-one in
#     the offset walk cannot hide. 12 distinct task strings (task dedup/cap),
#     a `bool` feature (next.done), and robot_type "unknown". Small: ~84 KB
#     for both generations together.
#
#   svla_so101_pickplace @ v3.0    — meta/info.json ONLY, for the feature-
#     extraction path. nyu_rot has a single camera, so it cannot catch a reader
#     that only ever returns the first video stream. This one has two
#     (observation.images.up / .side), av1-coded, plus a 6-DoF action space
#     with named joints. 3.4 KB buys a defect class the primary fixture is
#     structurally blind to.
#
#   taccap-g1 @ main               — meta/tasks.parquet ONLY, 1.7 KB. The
#     curated lerobot/* datasets were exported by a pandas path that leaves the
#     task string in the "__index_level_0__" index column; this recently
#     recorded community dataset names it "task". Both spellings are live in
#     the wild, and the curated fixtures alone would have let us hardcode the
#     pandas artifact and break on every freshly recorded dataset.
#
#   nyu_rot data/ (W2)             — the per-episode parquets for episodes 0
#     and 1 in v2.1, and v3.0's single 440-row file holding all fourteen. The
#     SAME recorded numbers written by two exporters into two layouts, which is
#     what lets the series reader be checked across generations instead of
#     against itself. ~23 KB.
set -euo pipefail
cd "$(dirname "$0")"

fetch() { # <repo> <commit> <path-under-meta> <dest>
  fetch_raw "$1" "$2" "meta/$3" "$4"
}

fetch_raw() { # <repo> <commit> <path-from-repo-root> <dest>
  mkdir -p "$(dirname "$4")"
  curl -fsSL --max-time 120 \
    "https://huggingface.co/datasets/$1/resolve/$2/$3" -o "$4"
}

NYU=lerobot/nyu_rot_dataset
NYU_V21=44b85508a0d832ef31bea65071855e217dc275bf
NYU_V30=72e8343e26cf1e18730e5c7111afc044abc34ef2

for f in info.json episodes.jsonl tasks.jsonl episodes_stats.jsonl; do
  fetch "$NYU" "$NYU_V21" "$f" "lerobot/nyu_rot_dataset/v2.1/meta/$f"
done

for f in info.json stats.json tasks.parquet; do
  fetch "$NYU" "$NYU_V30" "$f" "lerobot/nyu_rot_dataset/v3.0/meta/$f"
done
fetch "$NYU" "$NYU_V30" "episodes/chunk-000/file-000.parquet" \
  "lerobot/nyu_rot_dataset/v3.0/meta/episodes/chunk-000/file-000.parquet"

# Data files (W2 — the series reader). v2.1 keeps one parquet per episode;
# v3.0 packs every episode into one file located through the meta/episodes
# offsets, which is the pair the cross-generation test needs.
for i in 000000 000001; do
  fetch_raw "$NYU" "$NYU_V21" "data/chunk-000/episode_$i.parquet" \
    "lerobot/nyu_rot_dataset/v2.1/data/chunk-000/episode_$i.parquet"
done
fetch_raw "$NYU" "$NYU_V30" "data/chunk-000/file-000.parquet" \
  "lerobot/nyu_rot_dataset/v3.0/data/chunk-000/file-000.parquet"

SVLA=lerobot/svla_so101_pickplace
SVLA_V30=f641879e22172be7e8161d5e6c1503c2d2feb657
fetch "$SVLA" "$SVLA_V30" "info.json" \
  "lerobot/svla_so101_pickplace/v3.0/meta/info.json"

# The community dataset began returning 401 to anonymous requests on
# 2026-07-29, having been readable the day before: a third-party repo can be
# gated (or deleted) at any time, and a pinned commit does not protect against
# that. Its 1.7 KB is committed, so the fixture is intact and the tests are
# unaffected — only a REFETCH needs credentials now. Non-fatal on purpose: one
# upstream access change must not stop the other fixtures from refreshing.
# Set HF_TOKEN (and accept the repo's terms) to refetch this one.
TACCAP=xueyuquan/taccap-g1-eraze-the-blackboard-0727
TACCAP_SHA=15e997d8c6f38d213932882e3709850b4037acea
if ! fetch "$TACCAP" "$TACCAP_SHA" "tasks.parquet" \
  "taccap-g1/v3.0/meta/tasks.parquet"; then
  echo "WARN: $TACCAP is not readable anonymously; keeping the committed copy" >&2
  git checkout -- taccap-g1/v3.0/meta/tasks.parquet 2>/dev/null || true
fi

echo "fixtures refreshed:"
find . -name '*.json' -o -name '*.jsonl' -o -name '*.parquet' \
  | sort | xargs -r stat -c '  %8s  %n'
