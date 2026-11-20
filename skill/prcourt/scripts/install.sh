#!/bin/sh
# Install or update the CLI from the latest stable GitHub release. No Go needed.
set -eu
repo=chrisdmacrae/prcourt
command -v gh >/dev/null 2>&1 || { echo 'Install GitHub CLI (https://cli.github.com), then rerun this script.' >&2; exit 1; }

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) echo 'Unsupported OS. On Windows, use install.ps1.' >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo 'Supported CPU architectures: x64 and arm64.' >&2; exit 1 ;;
esac

tag=$(gh api "repos/$repo/releases/latest" --jq .tag_name)
case "$tag" in v[0-9]*) ;; *) echo "Unexpected release tag: $tag" >&2; exit 1 ;; esac
installed=$(prcourt --version 2>/dev/null || true)
if [ "$installed" = "$tag" ]; then
  echo "prcourt $tag is up to date."
  exit 0
fi

bin="$HOME/.local/bin"
mkdir -p "$bin"
tmp=$(mktemp -d "$bin/.prcourt-XXXXXX")
trap 'rm -rf "$tmp"' EXIT HUP INT TERM
asset="prcourt_${os}_${arch}"
gh release download "$tag" --repo "$repo" --pattern "$asset" --pattern checksums.txt --dir "$tmp"
expected=$(awk -v asset="$asset" '$2 == asset {print $1}' "$tmp/checksums.txt")
if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "$tmp/$asset" | awk '{print $1}')
else
  actual=$(shasum -a 256 "$tmp/$asset" | awk '{print $1}')
fi
if [ -z "$expected" ] || [ "$expected" != "$actual" ]; then
  echo "Checksum verification failed for $asset; leaving the existing CLI unchanged." >&2
  exit 1
fi
chmod +x "$tmp/$asset"
if [ "$("$tmp/$asset" --version)" != "$tag" ]; then
  echo 'Downloaded CLI version does not match the release; leaving the existing CLI unchanged.' >&2
  exit 1
fi
mv -f "$tmp/$asset" "$bin/prcourt"
echo "Installed prcourt $tag at $bin/prcourt"
echo "Put $bin first in PATH to use this CLI."
