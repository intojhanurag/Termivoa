# Issue #256 design checkpoint

2026-10-08; branch `fix/issue-256`, base `08f0332`; macOS arm64, Go 1.27.1.
ADR 0013/design only. **DO NOT MERGE without owner review.**

Confirmed with a temporary Go overlay: create 50 bytes of synthetic token JSON
in a temporary outside file, `os.Link` it to `github.json`, call existing
`loadAccount`/`deleteAccount`, then compare the outside file's bytes. The probe
fails: `outside target unchanged=false bytes=50`. See [baseline.txt](baseline.txt).
No owner data, token, network request or startup operation was used.

Actual command from this worktree:

```sh
go test -overlay <temporary-overlay.json> ./internal/github \
  -run '^TestAudit213HardlinkDeleteDoesNotMutateOtherName$' -count=1 -v
```

The coordinator retains the synthetic probe/overlay in the repository's Git
scratch area under `issue-sweep-2026-10-08/213-security-review/`; it maps a new
test-only package file, without changing shipped code. The historical test
name reflects where the inherited defect was discovered; #256 owns its fix.

Pending: private draft PR, implementation, permanent regression/race/vet,
platform compilation/native checks, rule22 public docs/frozen build,
independent security/feasibility review and explicit owner approval. CI denied;
no hosted checks, push, PR, merge or deployment by the implementing helper.
