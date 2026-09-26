# Native desktop shrink and grow regression

Date: 2026-09-21. Apple Silicon macOS 26.5.2 (25F84), Go 1.27.1.
This extends the completed native outer-PTY attachment fixture documented in
`desktop-outer-pty.md`. Only disposable terminals, broker state, and synthetic
child receipts are used.

## Failure on the first controlled-resize implementation

With runtime checkpoint `8eecc74`, the real executable successfully shrank an
outer 120x40 terminal to 60x18, changed canonical dimensions to 60x17, applied a
complete snapshot, displayed Control, and accepted the exact synthetic `SHRUNK`
input receipt. Immediately enlarging the same outer terminal back to 120x40
then left the canonical grid at 60x17 indefinitely. Three native race runs failed:

```text
canonical size = 60x17, want 120x39
```

The first run reached the overall 35-second context deadline, preventing useful
live-state capture and also reporting guardian cleanup deadlines. A scoped
read-only check afterward found no remaining fixture guardian/attach processes.
The second run used a shorter five-second dimension-phase deadline and captured
the live child at 60x17 plus the desktop's unchanged Control footer, then cleaned
up normally. A third run, including full canonical-cell/cursor comparisons
before and after shrinking, reproduced the same grow failure in 9.51 s (package
10.248 s). No deadline was increased to obtain a pass.

Source review identified the stale cache: the paint worker can initiate a
controlled shrink before the coordinator's periodic resize poll updates its
last-requested size. A quick grow back to the old cached size then appeared to
need no resize even though the canonical grid was still small. This is an
actual executable regression, not a Unicode paste failure.

The permanent sequence includes shrink, a wrapped exact receipt, immediate
grow, another receipt, then controller takeover, signal cleanup, and reconnect.
The outer display's entire canonical row region and cursor are compared with
the broker after initial input and both size transitions; the local status row
is intentionally outside that canonical region.

Runtime follow-up validation is pending; this record does not mark the new
shrink/grow gate passed. Native Windows execution and real terminal/phone UX
remain separate gates.

## Startup handoff failure on the follow-up

The next runtime checkpoint, `ea6e3cd` (local cherry-pick `a307fe8`), removed
the stale resize cache and serialized snapshot paint/ack with resize/resync.
It also added a separate startup decoder and a final native input discard.
Independent source review found no blocker and the focused startup/resize
race tests passed ten repetitions (desktop 1.544 s, CLI 3.138 s, liveclient
2.906 s). Those checks did not establish the native visible-readiness boundary.

The unchanged full executable command then ran five repetitions:

```sh
go test -race ./tests/integration -run '^TestPtermDesktopAttachNativeOuterTTY$' -count=5 -timeout=90s -v
```

It failed overall in 75.480 s: three runs timed out at the first `CONTROL`
receipt, one completed the entire expanded fixture in 8.80 s, and one reached
the final observer reconnect but lost the immediate Ctrl+] detach and hit the
overall context deadline (39.43 s including cleanup). The failed input runs
had canonical 120x39 dimensions and a visible final-row Control footer, but
both canonical and outer state still contained only the synthetic boot/grid
screen, with no first receipt. The observer failure was at the final detach,
not a shell termination or Unicode failure.

Source inspection identified another real handoff issue: `Connect` advertised
Control/Read-only before the startup decoder was canceled/joined and native
input was flushed. A user reacting to that visible status could therefore have
fresh input discarded. The test was not delayed to hide that interval. The
runtime owner is fixing the advertisement/input-acceptance boundary; this
native gate remains pending until that fix is committed and retested.

`go vet ./tests/integration`, `git diff --check`, and a redacted Gitleaks scan
of the integration sources passed. These checks do not override the native
failures above.
