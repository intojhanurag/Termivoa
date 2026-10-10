# Issue247: desktop snapshot row arithmetic

The direct FrameClient API accepted StartRow=MaxInt64 with one row because
addition overflowed, then panicked while indexing. before.txt records the
real baseline failure with workspace/toolchain paths scrubbed. This is a
local API robustness defect; the stricter transport validator in #237 is a
separate issue, and no remote-exploit claim is made here.

Check the start against screen height, then compare count against the
remaining height without addition. Eight invalid-boundary cases require
ErrResyncRequired, assembly reset and no partial painting. Each subsequently
accepts a valid last-row snapshot, proving recovery instead of only rejection.

`go test -race ./internal/desktop ./internal/protocol -count=1` passes
(2.575s/5.001s), saved in after.txt. Go vet for both packages passes.
No protocol generation or valid frame/render behavior changes. Native Windows,
owner phone and required hosted checks remain pending; CI is intentionally
kept disabled by the owner. Frozen docs build is recorded below after execution.

Frozen site install and Astro29-page build passed with all internal links
valid. Independent adversarial review reran full desktop/protocol race tests
(3.165s/4.965s), found no runtime blocker, and caught the missing release-note
entry; it is now included under Next beta. Final docs rebuild after this
release-note correction passed (29 pages and all links).
