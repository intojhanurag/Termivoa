#!/bin/sh
# Build UNSIGNED Linux pterm binaries with the embedded website into
# dist/linux: pterm-linux-amd64 and pterm-linux-arm64 (or the architectures
# given as arguments), plus SHA256SUMS. Needs Go, Node 24 and pnpm on the
# build machine only; the binaries need nothing on the host.
# Usage: packaging/linux/build.sh [amd64] [arm64]
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$repo_dir"

version=${PTERM_VERSION:-0.0.0-dev}
if ! printf '%s' "$version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$'; then
  echo "PTERM_VERSION must look like 1.2.3 or 1.2.3-rc.1" >&2
  exit 1
fi
build_id="$version+$(git rev-parse --short=12 HEAD)"
git diff --quiet HEAD -- || build_id="$build_id.dirty"

pnpm build:web
./scripts/embed-web.sh

mkdir -p dist/linux
for arch in ${*:-amd64 arm64}; do
  case $arch in
    amd64 | arm64) ;;
    *) echo "unsupported architecture: $arch (use amd64 or arm64)" >&2; exit 1 ;;
  esac
  CGO_ENABLED=0 GOOS=linux GOARCH=$arch \
    go build -trimpath -tags webdist -ldflags "-X main.buildID=$build_id" -o "dist/linux/pterm-linux-$arch" ./cmd/pterm
  echo "Built dist/linux/pterm-linux-$arch ($build_id)"
done
cd dist/linux
if command -v sha256sum >/dev/null 2>&1; then
  sha256sum pterm-linux-* >SHA256SUMS
else
  shasum -a 256 pterm-linux-* >SHA256SUMS
fi
cat SHA256SUMS
