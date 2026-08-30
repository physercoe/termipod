#!/usr/bin/env bash
# Run a checksum-pinned actionlint without adding a repository tool dependency.

set -euo pipefail

version=1.7.12
os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$arch" in
  x86_64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) echo "ERROR: unsupported actionlint architecture: $arch" >&2; exit 1 ;;
esac

case "${os}_${arch}" in
  darwin_amd64) checksum=5b44c3bc2255115c9b69e30efc0fecdf498fdb63c5d58e17084fd5f16324c644 ;;
  darwin_arm64) checksum=aba9ced2dee8d27fecca3dc7feb1a7f9a52caefa1eb46f3271ea66b6e0e6953f ;;
  linux_amd64) checksum=8aca8db96f1b94770f1b0d72b6dddcb1ebb8123cb3712530b08cc387b349a3d8 ;;
  linux_arm64) checksum=325e971b6ba9bfa504672e29be93c24981eeb1c07576d730e9f7c8805afff0c6 ;;
  *) echo "ERROR: unsupported actionlint platform: ${os}_${arch}" >&2; exit 1 ;;
esac

tmp_dir=$(mktemp -d)
trap 'rm -rf "$tmp_dir"' EXIT
archive="$tmp_dir/actionlint.tar.gz"
curl -fsSL \
  "https://github.com/rhysd/actionlint/releases/download/v${version}/actionlint_${version}_${os}_${arch}.tar.gz" \
  -o "$archive"
if command -v sha256sum >/dev/null 2>&1; then
  echo "$checksum  $archive" | sha256sum -c -
else
  echo "$checksum  $archive" | shasum -a 256 -c -
fi
tar -xzf "$archive" -C "$tmp_dir" actionlint
"$tmp_dir/actionlint" .github/workflows/*.yml
