# Static phone assets

The Go gateway serves the embedded phone build. Build-time Node tools are
not a shipped runtime. Updated 2026-10-08 for #223.

## How are assets compressed?

The Vite `precompressAssets` plugin runs at `closeBundle`, after Vite's final
preload-token rewrites. It gzip-compresses regular public JS, MJS, CSS and SVG
files under `assets/`, at least 1 KiB, only if compression makes them smaller.
It does not compress source maps, dotfiles, the app document, service workers,
manifests or authenticated data. The generated sidecars are gitignored build
output, copied by existing embed/packaging flows. The service worker keeps
only original URLs; browser fetch supplies decoded bytes as usual.

## How does the gateway select a representation?

The gateway verifies the original regular asset and may read its `.gz`
sidecar when gzip is acceptable. It never compresses at request time or
retains a compression cache. Content type uses the original name, length
uses the selected bytes, and `Vary` includes `Accept-Encoding` and `Range`.
Immutable cache lifetime stays one year. A SHA-256 ETag identifies the exact
selected bytes, so raw and gzip validators differ. `ServeContent` handles
GET, HEAD and conditional requests.

Range requests use raw bytes when identity is acceptable. This preserves
single and multipart ranges without falsely marking multipart as gzip.
If identity is forbidden, a supported encoded representation is returned
in full, ignoring the optional Range header. Unsupported accepted encodings
produce 406 with no-store. Gzip sidecars cannot be requested directly; maps,
dotfiles and push-scope paths retain their existing exclusions. Encoding
selection never touches API responses, WebSockets or cookie-dependent HTML.

## What proves the serving path?

Tests cover negotiation (including q=0 and wildcards), decoded equality,
HEAD lengths, representation ETags/304, byte ranges, multipart ranges,
unacceptable encodings and excluded paths. A real Vite bundle regression
verifies gzip bytes equal final emitted files and sidecar URLs stay out of
the service-worker manifest; its offline Wall test still passes. A
`webdist` production handler regression verifies the embedded TerminalView
asset and reports actual transferred bytes. Evidence is in
[issue-223](evidence/issue-223/README.md).
