# Live-session decoder boundary audit

Date: 2026-09-21. Base: `f74f7620746ba1646e7a056886558f23dbbc795a`.

The existing `protocol.DecodeClient`, `DecodeHost`, and `DecodePayload[T]` are
the canonical live-session decoding API. IPC dispatch must reuse these after
the local handshake, not add a second message format or raw input channel.
No schema or generated-type change was needed for this audit.

## Demonstrated defects and fixes

- Unknown mouse buttons, including the empty string, were accepted for press,
  release, and move because the validator only excluded wheel buttons. The
  validator now checks the schema's complete button enum before event semantics.
- Invalid raw UTF-8 in text/paste JSON was changed to U+FFFD by `encoding/json`
  before the text validator could inspect it. Strict decoding now rejects the
  original malformed bytes. A deliberately supplied valid U+FFFD remains valid.
- Escaped lone UTF-16 surrogates (such as `\ud800`) were also silently replaced.
  Strict decoding now checks actual Unicode escapes before decoding. Valid
  surrogate pairs, literal escaped backslashes, combining text, and ZWJ emoji
  retain their exact meaning. The client-side scalar check remains required
  before the P5 browser input path sends a user action.

Regression tests failed on the base for both defects before the fixes. The
counter audit found no defect: canonical uint64 decimal strings are supported,
including values above JavaScript's safe integer limit, and signed, leading-zero,
fractional, exponent, and overflowing representations are rejected.

## Validation

- `go test ./internal/protocol -run 'TestDecodeClientRejectsUnknownMouseButtons|TestDecodeClientRejectsMalformedUTF8BeforeReplacement|TestDecimalCounterCanonicalBoundaries' -count=1`:
  failed on base for unknown mouse buttons and malformed UTF-8, as expected.
- `go test -race ./internal/protocol`: passed after fixes.
- `go vet ./internal/protocol`: passed.
- Live-session table covers all 13 post-hello client message types, wrong
  direction, missing/malformed/null/case-shadowed request/session/generation IDs,
  object shape/unknown raw-input fields, and valid Unicode/multiline boundaries.
- Mouse tests cover every defined kind/button combination and unknown enums.
- `go test ./internal/protocol -run 'TestDecodeClientRejectsUnpairedSurrogateEscapes|TestDecodeClientPreservesPairedAndLiteralUnicodeEscapes' -count=1`:
  rejected-surrogate regressions failed before the fix; paired/literal cases
  passed. The full race-enabled protocol suite and vet passed after the fix.

This is decoder evidence, not end-to-end IPC, native Windows, phone, transport
authentication, or release validation. Runtime lease ownership, input sequence
acceptance, snapshot readiness, canonical mouse grid dimensions, and history
revision consistency remain broker/actor checks after decoding.
