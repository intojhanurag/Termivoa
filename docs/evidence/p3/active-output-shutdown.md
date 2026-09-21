# Active-output shutdown drain and join

Date: 2026-09-21. Base: `ffe28ff`, plus the finite-output fixture from
`fc4349b` (local cherry-pick `86ca61c`). Apple Silicon macOS 26.5.2 (25F84),
Go 1.27.1. Only disposable, broker-owned synthetic child processes were used.

## Reproduced defect

The finite-output probe exposed seven interrupted cleanup failures. Independent
native reproduction captured a three-way wait: the session actor synchronously
waited in `process.Terminate`, the PTY reader waited to enqueue output in its
bounded queue, and the child reaper waited for process teardown. On this Mac,
terminal teardown could not finish until the actor resumed draining output.
The three-second termination deadline then returned `context deadline exceeded`
and `operation not permitted`. The actor resumed only after that failure.

The durable `TestNativeSessionShutdownDrainsActiveOutput` runs a continuously
writing child. It waits for at least 200 canonical history rows, then separately
exercises `Close` and explicit `Terminate`. The child cannot naturally complete
before shutdown. A Go overlay using the unchanged pre-fix `session.go` failed
both cases in 6.928s total with those same errors. No process unrelated to the
fixture was sampled, signaled, or killed.

The deterministic one-slot-queue regression independently failed all three
paths (`Close`, explicit termination, terminal failure): the actor could not
parse the four ordered output chunks required for the synthetic process to
finish. This distinguishes a runtime wait/drain cycle from the finite probe's
separate five-second test-helper mistake.

## Fix and invariants

The actor starts one bounded termination worker and keeps interpreting PTY
output while the worker waits. `Close`, explicit termination, and lost-session
cleanup reuse that attempt and its original result. Its three-second timeout
and the existing output-drain grace bounds are unchanged. No raw output is
discarded, no queue is enlarged, and no disk spool is introduced.

An accepted explicit termination receives the worker's actual result, even if
its original request context is canceled. Repeated termination cannot succeed
or create another attempt while the session is exiting. Repeated `Close` observes
the same attempt; successful closure waits for cleanup. Process/terminal closure
waits for the worker, process
Wait, and ordered PTY EOF; a late termination cleanup error does not rewrite an
already-established natural exit status. `Close` preserves the original error
identity together with `ErrSessionShutdown`.

Review also identified three adjacent ordering edges, all reproduced before the
correction:

- Explicit/natural exit now immediately revokes the controller and drops accepted
  but not-yet-encoded inbox actions. Bytes already encoded/written remain in
  flight and are not claimed retractable.
- If the actor's reply and `Done` become ready together, the caller preserves the
  buffered authoritative reply instead of randomly returning `ErrSessionClosed`.
- The request-only fast path now applies only while running. Once exiting, the
  existing balanced select services both requests and output: a continuously
  replenished, bounded Status queue cannot starve the teardown drain. Its
  deterministic regression failed before this condition changed, then passed.

Independent read-only review found no remaining issue in the ownership, draining,
completion publication, lease/input boundary, or reply ordering. Its original
five-repeat race review passed (6.372s); ten repetitions of the two follow-up
regressions passed (1.323s).

## Checks run

```sh
go test -race ./internal/broker \
  -run '^TestSession(Termination|LateTermination|Shutdown|ReadFailure|CloseReturns|RejectsRequest)' \
  -count=3 -timeout=60s
go test -race ./internal/broker \
  -run '^TestSession(TerminationDrains|ClosedCall|TerminationBoundary|TerminationKeeps|LateTermination)' \
  -count=5 -timeout=60s
go test -race ./internal/broker ./internal/ptyhost ./internal/terminal \
  -count=1 -timeout=120s
go test -race -tags=stressprobe ./tests/integration \
  -run '^TestNativeSessionShutdownDrainsActiveOutput$' -count=3 -timeout=90s -v
go test -race -tags=stressprobe ./tests/integration \
  -run '^TestNative(SessionShutdownDrainsActiveOutput|OutputFloodPreservesCanonicalStateWithoutConsumingViewer)$' \
  -count=1 -timeout=90s -v
go vet ./internal/broker
go vet -tags=stressprobe ./tests/integration
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -c -o <temporary>/broker.test.exe ./internal/broker
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -c -tags=stressprobe \
  -o <temporary>/integration.test.exe ./tests/integration
git diff --check
```

The first focused three-repeat run passed (5.414s); the five-repeat follow-up
passed (1.670s). Five repetitions including the final queued-Status regression
also passed (1.642s). The first full affected race run passed: broker 14.579s,
PTY host 3.493s, terminal 2.096s. Native continuous-output shutdown passed all six
cases (2.149s package time; individual drain/join 8–11ms). The exact original
finite-output revision-200 reproduction also passed after the fix (257ms clean
drain/join). These elapsed times describe these fixtures, not a product latency
or performance claim. The repeat after the input/reply fixes also passed:
broker 12.110s, PTY host 2.942s, terminal 2.028s; six native active-output cases
2.038s total. Vet and both Windows compilation commands passed.

Final runtime verification, including the queued-Status fix, passed: broker
12.209s, PTY host 3.298s, terminal 2.159s. The combined native finite-output and
active-output shutdown run passed all four cases (13.630s package time); its two
shutdown drain/joins took 8.2–8.6ms. Final vet, both Windows compile-only checks,
format/diff checks, and staged Gitleaks v8.30.1 scanning passed.

## Limits

Native Windows execution is pending; Windows test binaries were compiled, not
run. This is shutdown correctness evidence, not the ten-minute three-session
stress test, 24-hour soak, resource budget, real-phone gate, or a release claim.
An OS termination failure still reports failure and does not pretend the process
is gone; resources stay owned until the actual process and pumps can be joined.
