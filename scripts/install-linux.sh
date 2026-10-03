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
  # The same help as `pterm setup` (rootHelp in cmd/pterm/lifecycle.go).
  if [ -n "${SUDO_USER:-}" ] && [ "$SUDO_USER" != root ]; then
    echo "Run this without sudo, as $SUDO_USER: Termivoa runs as your user, so that your phone gets your shell, not root's." >&2
    exit 1
  fi
  # Debian and Ubuntu name the administrators' group sudo; Fedora and others, wheel.
  group=sudo
  if ! getent group sudo >/dev/null 2>&1 && getent group wheel >/dev/null 2>&1; then
    group=wheel
  fi
  # The new user cannot read root's files: copy this script and the binary.
  files=$0
  if [ -n "${1:-}" ]; then
    files="$files $1"
  fi
  cat >&2 <<HELP
Run this as a normal user, not root: Termivoa runs as that user, so that your phone gets that user's shell, not root's.
A new server often has only root. Make a user with sudo, give it your SSH key and these files (replace <name>):
  adduser <name>
  usermod -aG $group <name>
  install -d -m 700 -o <name> -g <name> /home/<name>/.ssh && install -m 600 -o <name> -g <name> ~/.ssh/authorized_keys /home/<name>/.ssh/
  install -m 644 -o <name> -g <name> $files /home/<name>/
Then log in as that user (ssh <name>@<this-server>) and run the install there again.
HELP
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

# When $bin_dir is not on PATH yet, setup says so as its last line.
exec "$bin_dir/pterm" setup
