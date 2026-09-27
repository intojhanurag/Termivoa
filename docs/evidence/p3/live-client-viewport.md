# Desktop viewport validation before paint

Date: 2026-09-21. Mac arm64, macOS 26.5.2, Go 1.27.1.

The desktop client previously had no pre-paint hook for checking physical terminal
dimensions. A controller on another device can resize the canonical screen after
the observer's initial management lookup, and the observer's own window can shrink
while a snapshot is being transferred. Letting terminal cursor addressing clamp
those positions would corrupt the displayed grid.

`liveclient.Config.ValidateScreen` is an optional local paint-worker callback.
The desktop command can inspect the current physical dimensions before every
complete snapshot or patch is painted. A rejected frame is not written or
acknowledged. Ordinary callback errors stop only this viewer; a canonical session
is never resized implicitly by an observer. The callback must return promptly and
must not paint or call back into the client. No network protocol changes.

Deterministic tests cover rejected snapshots, a window shrinking between snapshot
begin/end, a rejected patch preserving the last complete drawing, and exact-fit
snapshot/patch success. Existing tests cover the nil-hook synthetic display.
Local callback failures remain fatal even if they wrap the frame assembler's
resync sentinel: requesting another snapshot cannot fix an unrepresentable local
viewport. A regression proves this path sends neither an acknowledgement nor a
resync request.

Actual verification:

- `go test -race ./internal/liveclient -count=1 -timeout=60s`: passed.
- `go vet ./internal/liveclient`: passed.

This is an integration boundary, not a native interactive resize pass. The public
desktop command must supply the physical-size check and demonstrate its behavior.
Controller shrink/resnapshot coordination and real terminal rendering remain
separate integration evidence. A physical resize can still happen after the size
check; this does not provide an atomic transaction with the OS window manager.
