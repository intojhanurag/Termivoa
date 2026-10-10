# How does desktop attach assemble a snapshot?

The desktop FrameClient assembles normalized broker rows under a declared
height before painting a complete snapshot. AddSnapshotRows checks generation
and snapshot identity, nonempty rows, start>=0, start<height and count<=height-start
before any indexing. This subtraction-based comparison cannot wrap at MaxInt64.
Invalid chunks discard the pending assembly and return ErrResyncRequired;
there is no partially painted screen. Valid last-row boundaries still complete.

This API guard is independent of strict wire validation (#237). The protocol
shape and generated types do not change. Tests use direct calls to cover
otherwise transport-rejected values and then recover with a fresh valid
snapshot. Evidence: [issue247](evidence/issue-247/README.md).
