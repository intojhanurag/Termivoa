#!/bin/sh
# Install pterm for the current Linux user in ~/.local/bin, then run
# `pterm setup`, which asks before it changes anything. No sudo: Termivoa
# runs as your user. Only two optional steps that setup explains
# need an administrator: `loginctl enable-linger` (if your system refuses it)
# and `sudo tailscale set --operator=$USER`; you run those yourself.
#
# Usage:
#   scripts/install-linux.sh                      # from a source checkout: build, then install
#   scripts/install-linux.sh ./pterm-linux-amd64  # install a binary you built or copied
set -eu

if [ "$(uname -s)" != Linux ]; then
  echo "This script installs on Linux. On macOS and Windows see docs/install.md." >&2
  exit 1
fi
if [ "$(id -u)" = 0 ]; then
  echo "Run this as your normal user, not root: Termivoa runs per user." >&2
  exit 1
fi
case $(uname -m) in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) echo "Unsupported CPU $(uname -m); Termivoa builds for x86_64 and arm64." >&2; exit 1 ;;
esac

source=${1:-}
if [ -z "$source" ]; then
  repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
  source="$repo_dir/dist/linux/pterm-linux-$arch"
  if [ ! -f "$source" ]; then
    echo "Building $source (needs Go, Node 24 and pnpm)..."
    "$repo_dir/packaging/linux/build.sh" "$arch"
  fi
fi
if [ ! -f "$source" ]; then
  echo "No pterm binary at $source" >&2
  exit 1
fi

bin_dir="$HOME/.local/bin"
mkdir -p "$bin_dir"
# Ubuntu gives a new user umask 002, so these would be group-writable; pterm
# refuses to keep its data under a folder that another user could write.
chmod go-w "$HOME/.local" "$bin_dir"
# Replace atomically: a running broker keeps its old file until it restarts.
install -m 0755 "$source" "$bin_dir/.pterm.new"
mv -f "$bin_dir/.pterm.new" "$bin_dir/pterm"
echo "Installed $bin_dir/pterm"
case ":$PATH:" in
  *":$bin_dir:"*) ;;
  *) echo "Note: $bin_dir is not on your PATH yet. Ubuntu adds it at your next login; until then use $bin_dir/pterm." ;;
esac

exec "$bin_dir/pterm" setup
