#!/bin/sh
# Owner only (docs/chat-view.md, question 7). Do not run in CI or from an
# agent: it starts the real Claude Code with your own login, which costs
# credits, in a throwaway folder, and reads only the one transcript of that
# run from your Claude folder.
#
# It writes a sanitized fixture candidate (structure only: keys, types,
# tool names, flags, numbers; every other text becomes "<text N bytes>",
# IDs become fake IDs, the folder becomes /work/demo) to
#   docs/evidence/chat-view/capture-<claude version>.jsonl
# Review that file before you commit it. The raw transcript is never copied
# into the repository and the throwaway folder is deleted at the end.
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
claude_dir=${CLAUDE_CONFIG_DIR:-$HOME/.claude}
command -v claude >/dev/null 2>&1 || { echo "claude is not on PATH" >&2; exit 1; }
command -v uuidgen >/dev/null 2>&1 || { echo "uuidgen is not on PATH" >&2; exit 1; }

printf 'This runs Claude Code once (it costs credits). Continue? [y/N] '
read -r answer
case $answer in y|Y|yes|YES) ;; *) echo "Cancelled."; exit 1 ;; esac

work=$(mktemp -d "${TMPDIR:-/tmp}/termivoa-chat-capture.XXXXXX")
trap 'rm -rf "$work"' EXIT
mkdir "$work/demo"
cd "$work/demo"
# Resolve the folder the way the agent sees it (macOS /tmp is /private/tmp).
project=$(pwd -P)
cat > add.go <<'GO'
package demo

// Add returns the sum of a and b.
func Add(a, b int) int { return a - b }
GO
printf 'module example.com/demo\n\ngo 1.22\n' > go.mod

session=$(uuidgen | tr 'A-Z' 'a-z')
version=$(claude --version 2>/dev/null | awk '{print $1}')
# A short scripted run: a prompt, a Read, an Edit, a Bash call, a final text.
claude -p --session-id "$session" \
  --allowedTools "Read" "Edit" "Bash(go vet:*)" \
  "Read add.go, fix the bug in Add, then run go vet ./... and answer in one sentence." >/dev/null

folder=$(printf '%s' "$project" | sed 's/[^A-Za-z0-9]/-/g')
transcript="$claude_dir/projects/$folder/$session.jsonl"
if [ ! -f "$transcript" ]; then
  echo "The transcript of this run was not found; nothing was copied." >&2
  exit 1
fi
out="$repo_dir/docs/evidence/chat-view/capture-${version:-unknown}.jsonl"
mkdir -p "$(dirname "$out")"
(cd "$repo_dir" && go run ./scripts/chatcapture) < "$transcript" > "$out"
echo "Wrote $out ($(wc -l < "$out" | tr -d ' ') lines). Review it before you commit it."
