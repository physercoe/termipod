#!/usr/bin/env bash
# Classify a GitHub Actions diff into the repository's independently gated lanes.
#
# Required environment:
#   GITHUB_OUTPUT  Actions output file (or a writable file in local tests)
# Optional environment:
#   BASE_SHA       Commit before the change
#   HEAD_SHA       Commit after the change (defaults to HEAD)
#   FULL_SCAN      true for scheduled/manual full scans

set -euo pipefail

: "${GITHUB_OUTPUT:?GITHUB_OUTPUT must name an output file}"

hub=false
mobile=false
desktop=false
actions=false

if [[ "${FULL_SCAN:-false}" == "true" ]]; then
  hub=true
  mobile=true
  desktop=true
  actions=true
else
  head_sha="${HEAD_SHA:-HEAD}"
  base_sha="${BASE_SHA:-}"

  if [[ -n "$base_sha" && ! "$base_sha" =~ ^0+$ ]] && git cat-file -e "${base_sha}^{commit}" 2>/dev/null; then
    # A PR base SHA can advance after the branch forked. Diff from the merge
    # base so unrelated commits newly landed on main do not over-trigger lanes.
    merge_base=$(git merge-base "$base_sha" "$head_sha")
    changed_files=$(git diff --name-only "$merge_base" "$head_sha")
  else
    changed_files=$(git diff-tree --root --no-commit-id --name-only -r "$head_sha")
  fi

  while IFS= read -r path; do
    [[ -n "$path" ]] || continue

    case "$path" in
      hub/*)
        hub=true
        ;;
      lib/*|test/*|android/*|ios/*|pubspec.yaml|pubspec.lock|analysis_options.yaml|l10n.yaml|scripts/mobile-build-metadata.sh)
        mobile=true
        ;;
      desktop/*)
        desktop=true
        ;;
      design-tokens/*)
        mobile=true
        desktop=true
        ;;
      Makefile)
        # make bump updates both the Flutter version and Hub build metadata.
        hub=true
        mobile=true
        ;;
      scripts/lint-desktop-tokens.sh|scripts/desktop-token-baseline.txt)
        desktop=true
        ;;
    esac

    case "$path" in
      .github/workflows/ci.yml|.github/workflows/codeql.yml|scripts/ci-changed-paths.sh)
        # Changes to the classifier or its primary consumers exercise every lane.
        hub=true
        mobile=true
        desktop=true
        actions=true
        ;;
      .github/dependabot.yml|scripts/lint-github-actions.sh)
        actions=true
        ;;
      .github/workflows/release.yml)
        mobile=true
        actions=true
        ;;
      .github/workflows/release-server.yml)
        hub=true
        actions=true
        ;;
      .github/workflows/desktop.yml|.github/workflows/desktop-electron-release.yml)
        desktop=true
        actions=true
        ;;
      .github/workflows/*)
        actions=true
        ;;
    esac
  done <<< "$changed_files"
fi

{
  echo "hub=$hub"
  echo "mobile=$mobile"
  echo "desktop=$desktop"
  echo "actions=$actions"
} >> "$GITHUB_OUTPUT"
