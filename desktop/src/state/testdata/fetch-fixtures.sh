#!/usr/bin/env bash
# Refetch the robot-description fixtures the URDF reader is tested against.
#
# These are the REAL description files the manifest points at, at the exact
# commits `state/robotManifest.ts` pins — not hand-written miniatures. A
# hand-written URDF agrees with whatever the author assumed the format was,
# which is precisely the class of bug a parser has to survive. Two of the three
# below already carry a surprise the author did not predict:
#
#   so100.urdf          — TheRobotStudio/SO-ARM100, Apache-2.0. Six revolute
#     joints whose names (shoulder_pan … gripper) are EXACTLY the channel names
#     LeRobot writes for this arm, which is what makes name-matching the primary
#     strategy rather than a nicety.
#
#   so101_new_calib.urdf — the same repo, the successor arm. Same joint names,
#     but declared in the file gripper-FIRST, i.e. reversed from the order the
#     motor table reports them in. That is the fixture that proves file order is
#     not channel order, and therefore that a positional fallback needs the
#     manifest's declared jointOrder to be safe.
#
#   panda.urdf          — Gepetto/example-robot-data, BSD-3-Clause. A second
#     vendor with a different naming convention (panda_joint1…7), an `xmlns:xacro`
#     attribute on the root of an otherwise plain URDF, and both `fixed` and
#     `prismatic` joints — none of which the SO-ARM files exercise at all.
#
# Usage:  bash fetch-fixtures.sh        # from this directory
#
# Every commit is pinned, so a rerun either reproduces the bytes in git or fails
# loudly. When you change a pin here, change it in `state/robotManifest.ts` too:
# the whole point of the fixture is that it is the file the app will fetch.

set -euo pipefail

SO_ARM_SHA=fda892cba81032c46c40976a48c9ceadbf40a9ca
ERD_SHA=6249cab1cdffa4fadb9a53dda964a50d79c5eaaf

fetch() {
  local url="$1" out="$2"
  echo "  $out"
  curl -fsSL --max-time 60 -o "$out" "$url"
}

fetch "https://raw.githubusercontent.com/TheRobotStudio/SO-ARM100/${SO_ARM_SHA}/Simulation/SO100/so100.urdf" so100.urdf
fetch "https://raw.githubusercontent.com/TheRobotStudio/SO-ARM100/${SO_ARM_SHA}/Simulation/SO101/so101_new_calib.urdf" so101_new_calib.urdf
fetch "https://raw.githubusercontent.com/Gepetto/example-robot-data/${ERD_SHA}/robots/panda_description/urdf/panda.urdf" panda.urdf

echo "done — 3 descriptions"
