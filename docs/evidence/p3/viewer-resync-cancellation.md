# Actor-ordered viewer resync cancellation

Date: 2026-09-21. Base: `5c39c43`. macOS 26.5.2 (25F84), Apple Silicon,
Go 1.27.1, Node 24.11.0, pnpm 10.33.0. This record includes the combined
verification that exposed the defect, not just subsequent passing reruns.

## Original combined verification

A fresh checkout installed the exact lockfile using `pnpm install
--frozen-lockfile` and ran Node 24 `./scripts/check`. At the same time, a separate
`go test -race ./... -count=1 -timeout=120s` checked the root Go packages.

The repository check passed: protocol generation had no drift; Go formatting,
vet, ordinary package tests, all three patched dependency suites, TypeScript
checking, all 47 tests in six web test files, and the production Vite build
passed. The web tests took 269 ms and the Vite build 362 ms. This is development
validation, not evidence of complete phone UX or release readiness.

The simultaneous race run **failed** with this actual output:

```text
--- FAIL: TestRegistryRetiresRealExitedSession (8.15s)
    registry_test.go:1338: real session was not retired: <nil>
--- FAIL: TestViewerResyncInterruptsSnapshotAndWrongAckForcesFreshSnapshot (2.06s)
    viewer_test.go:184: never received snapshot.begin occurrence 3
FAIL github.com/intojhanurag/phone-terminal/internal/broker 24.311s
```

The remaining root packages passed that race run, including native integration
in 15.064 s. These failures were not converted into skips or hidden by a larger
deadline. Both named tests passed a focused race rerun with `-count=10` in
6.484 s (registry 0.36–0.57 s, viewer 0.09–0.12 s). A second broad race run,
without `scripts/check` overlap but alongside the small vt/xpty race suites,
also passed all packages (broker 13.981 s; integration 6.525 s). Those reruns
alone did not establish the reason for either original failure.

## Demonstrated viewer defect

Both `ViewerSubscription.Resync` and a mismatched `SnapshotApplied` used to ask
the session actor to reset the viewer, then call an unscoped `cancelDelivery`
after the actor replied. Reset already notified the worker. If the caller was
descheduled across that reply, the worker could install the replacement's
cancel function first; the old request would then cancel the new valid send.
The worker saw the current delivery epoch, treated cancellation as its error,
and disconnected the viewer rather than delivering the replacement snapshot.

A temporary deterministic diagnostic scheduled those two existing public-method
steps separately: reset through the actor, wait until the replacement sink
starts, then resume the old caller's cancellation. All three race runs failed:

```text
late resync continuation killed valid replacement: context canceled
```

This faithfully models a possible caller preemption; it does not claim that
the temporary test invoked the complete public method without a scheduling
split. An additional 1,000-request public-method stress test alone passed on
the old implementation, emphasizing why a deterministic boundary test matters.
The temporary diagnostic was removed after reproducing the defect.

## Fix and permanent regressions

Cancellation now happens inside `resetViewer`, before the actor notifies a
replacement. Public callers no longer cancel a possibly newer send after
receiving their replies.

Independent review identified a second scheduling boundary: the worker may
reserve an old plan, pause before registering its cancel function, then resume
after the actor resets it. Moving active-send cancellation into the actor alone
does not close that gap. Existing snapshot epoch checks happen after rows have
been sent, and a patch has no pre-send epoch check. A deterministic fixture
reserved a snapshot or patch, reset it, then resumed cancellation registration.
Both cases failed against the first fix with:

```text
old reserved delivery acquired an uncanceled context after reset
```

The complete fix records a minimum valid delivery epoch under the subscription
mutex when the actor resets. Worker registration checks that same bound and
cancels/rejects an obsolete reservation before calling the stream encoder. The
fixture now also requires a new replacement reservation to register without
cancellation. Snapshot restart uses the same reset implementation. No new
timeout, public protocol shape, test hook, unbounded queue, or cancellation
retry was added.

The actor acquires the subscription mutex only long enough to advance the epoch
bound and take the cancel function, releases it, then invokes cancellation. The
worker's setter and budget critical sections do not wait on an actor call.
Network writes and drained callbacks remain outside the actor; the lock ordering introduces no
mutex-held wait for that actor.

The permanent regression gates the old snapshot rows, requests either explicit
resync or a wrong acknowledgement through the actor boundary, and requires the
replacement to start without a delayed public caller doing cleanup. On the
unchanged base both cases failed deterministically after 2.02 s, never receiving
the second `snapshot.begin`. With the fix they pass, acknowledge the replacement,
explicitly acquire control, and close cleanly. A second test exercises 1,000
public resync/wrong-ack requests without losing the viewer.

Focused command:

```sh
go test -race ./internal/broker -run '^TestViewer(ActorResetCancelsOldDeliveryBeforePublishingReplacement|RepeatedPublicResyncDoesNotCancelItsReplacement|ResetRejectsLateDeliveryRegistration|ResyncInterruptsSnapshotAndWrongAckForcesFreshSnapshot)$' -count=10 -timeout=90s -v
```

Result: PASS, 4.331 s. This includes the originally failing viewer test. The
earlier active-send-only fix had passed its focused run in 4.273 s; the
late-registration fixture demonstrated why that passing subset was insufficient.

Final complete-fix verification:

- `go test -race ./internal/broker ./internal/liveclient ./internal/desktop
  -count=1 -timeout=90s`: PASS (12.996 s / 2.085 s / 3.142 s).
- `go test -race ./tests/integration -run '^TestLiveClientNative' -count=3
  -timeout=45s`: PASS, 8.200 s, on native Apple Silicon macOS.
- `go vet ./internal/broker`: PASS.
- Windows x64 `CGO_ENABLED=0 go test -c ./internal/broker`: PASS compilation;
  the executable was not run on Windows.
- `git diff --check`: PASS.
- Gitleaks `v8.30.1` scanned the staged diff with no leaks found; staged paths
  were manually reviewed for secrets, private transcripts, and generated files.

## Other completed checks at the base checkpoint

- Windows x64 `CGO_ENABLED=0` compilation passed for `cmd/pterm` and the
  integration, desktop, and CLI test executables. They were not run on Windows.
- `go test -race github.com/charmbracelet/ultraviolet/... -count=1 -timeout=60s`
  passed all five packages. Separate nested vt and xpty race suites passed in
  1.553 s and 4.521 s respectively.
- Gitleaks `v8.30.1` scanned 119 commits, approximately 5.08 MB, with no leaks
  found. This is a scanner result, not a full manual publication-history audit.
- `pnpm audit --audit-level high` reported no known vulnerabilities.
- The first unqualified `go run` of govulncheck `v1.8.0` selected Go 1.26.8 and
  failed loading Go 1.27 code (`package requires newer Go version go1.27`). That
  run was **not a successful vulnerability scan**. Repeating with explicit
  `GOTOOLCHAIN=go1.27.1` completed and reported no vulnerabilities found.

The separate registry-retirement timeout remains under independent
investigation; this viewer fix does not claim to resolve it. Native Windows
execution, the public attach command's outer-TTY harness, physical phones,
authentication/private HTTPS, soak/performance targets, signed installers, and
release readiness remain separate gates. No account, startup configuration,
Tailscale setting, publication, or repository visibility was changed.
