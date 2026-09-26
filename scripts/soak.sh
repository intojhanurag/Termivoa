#!/bin/sh
# P7 soak probes (build tag `soak`). Tunables, all optional:
#   PTERM_SOAK_DURATION  busy-session run length      (default 2m)
#   PTERM_SOAK_RATE      child output bytes/s/session (default 1048576)
#   PTERM_SOAK_RECONNECT viewer/gateway replace every (default 20s)
#   PTERM_SOAK_STALL     stalled-viewer window        (default 20s)
# Extra arguments go to `go test`, e.g. scripts/soak.sh -run StalledViewer
set -eu
repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo_dir"
GOTOOLCHAIN=go1.27.1 exec go test -tags soak -count=1 -v -timeout 60m ./tests/soak/ "$@"
