#!/bin/sh
# Installs the latest dockdoctor release for this machine.
set -eu

repo="supremeinferno/dockdoctor"

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) echo "dockdoctor: unsupported OS $(uname -s); download a binary from https://github.com/$repo/releases" >&2; exit 1 ;;
esac

case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) echo "dockdoctor: unsupported CPU $(uname -m)" >&2; exit 1 ;;
esac

url="https://github.com/$repo/releases/latest/download/dockdoctor_${os}_${arch}"

dir=/usr/local/bin
if [ ! -w "$dir" ]; then
  dir="$HOME/.local/bin"
  mkdir -p "$dir"
fi

tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT
echo "Downloading $url"
curl -fsSL "$url" -o "$tmp"
chmod +x "$tmp"
mv "$tmp" "$dir/dockdoctor"
trap - EXIT

echo "Installed $dir/dockdoctor"
case ":$PATH:" in
  *":$dir:"*) ;;
  *) echo "Add $dir to your PATH to run it as 'dockdoctor'." ;;
esac
