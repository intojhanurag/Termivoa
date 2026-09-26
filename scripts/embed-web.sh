#!/bin/sh
# Copy the built website into the gateway embed directory. Run after
# `pnpm build:web`, then build with `go build -tags webdist ./cmd/pterm`.
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo_dir"

if [ ! -f web/dist/index.html ]; then
  echo "web/dist/index.html is missing; run 'pnpm build:web' first." >&2
  exit 1
fi
rm -rf internal/webassets/dist
cp -R web/dist internal/webassets/dist
echo "Embedded web/dist into internal/webassets/dist; build with -tags webdist."
