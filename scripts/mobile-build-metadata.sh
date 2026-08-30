#!/usr/bin/env bash
# Validate a mobile release tag and expose consistent Flutter build metadata.

set -euo pipefail

tag=${1:?usage: mobile-build-metadata.sh <mobile-vTAG> <git-sha>}
sha=${2:?usage: mobile-build-metadata.sh <mobile-vTAG> <git-sha>}
: "${GITHUB_OUTPUT:?GITHUB_OUTPUT must name an output file}"

case "$tag" in
  mobile-v*) version=${tag#mobile-v} ;;
  *) echo "ERROR: release tag must start with mobile-v: $tag" >&2; exit 1 ;;
esac

core=${version%%-*}
if [[ ! "$core" =~ ^[0-9]{4}\.[0-9]{3,4}\.[0-9]{1,4}$ ]]; then
  echo "ERROR: invalid mobile CalVer: $version" >&2
  exit 1
fi

IFS='.' read -r cv_year cv_mmdd cv_hhmm <<< "$core"
month=$((10#$cv_mmdd / 100))
day=$((10#$cv_mmdd % 100))
hour=$((10#$cv_hhmm / 100))
minute=$((10#$cv_hhmm % 100))

if (( month < 1 || month > 12 || day < 1 || day > 31 || hour > 23 || minute > 59 )); then
  echo "ERROR: invalid date/time fields in mobile CalVer: $version" >&2
  exit 1
fi

# Minutes since 2020-01-01 UTC, computed with portable integer Julian-Day
# arithmetic so Linux and macOS produce the same Android/iOS build number.
a=$(( (14 - month) / 12 ))
y=$(( 10#$cv_year + 4800 - a ))
m=$(( month + 12 * a - 3 ))
jdn=$(( day + (153 * m + 2) / 5 + 365 * y + y / 4 - y / 100 + y / 400 - 32045 ))
build_number=$(( (jdn - 2458850) * 1440 + hour * 60 + minute ))

if (( build_number <= 0 || build_number >= 2100000000 )); then
  echo "ERROR: computed build number is out of range: $build_number" >&2
  exit 1
fi

{
  echo "version=$version"
  echo "build_number=$build_number"
  echo "git_ref=${tag}@${sha:0:7}"
} >> "$GITHUB_OUTPUT"
