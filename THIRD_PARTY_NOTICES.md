# Third-party notices

This file is a preparatory dependency inventory for private development. Direct and transitive versions are locked. Unsigned development artifacts can be built with the scripts under `packaging/`. Unsigned beta binaries (`v0.1.0-beta.1` to `v0.1.0-beta.3`) were shared only with invited testers; the full notice audit is pending. Before a release candidate is assembled, generate an SBOM from the locked dependency manifests, inspect transitive dependencies, preserve required upstream `LICENSE` and `NOTICE` material, and replace this preparatory list with the exact shipped inventory.

## Direct dependencies (runtime and build)

| Component | Locked version | Upstream license to verify against locked source |
| --- | --- | --- |
| `github.com/coder/websocket` | v1.8.15 | ISC |
| `github.com/charmbracelet/x/xpty` | v0.1.4 plus repository patch; see `docs/dependency-patches.md` | MIT |
| `github.com/charmbracelet/x/vt` | v0.0.0-20260920004010-53e2afe73ae5 plus repository patch; see `docs/dependency-patches.md` | MIT |
| `github.com/charmbracelet/ultraviolet` | v0.0.0-20260303162955-0b88c25f3fff plus repository patch; see `docs/dependency-patches.md` | MIT |
| `github.com/charmbracelet/x/ansi` | v0.11.7 plus repository patch; see `docs/dependency-patches.md` | MIT |
| `modernc.org/sqlite` | v1.59.0 | BSD-3-Clause; locked source also carries SQLite's public-domain dedication and the bundled sqlite-vec MIT notice |
| `github.com/Microsoft/go-winio` | v0.6.2 | MIT |
| `github.com/charmbracelet/x/term` | v0.2.2 | MIT |
| `github.com/charmbracelet/x/windows` | v0.2.2 | MIT |
| `github.com/creack/pty` | v1.1.24 | MIT |
| `golang.org/x/sys` | v0.48.0 | BSD-3-Clause |
| `rsc.io/qr` | v0.2.0 | BSD-3-Clause (Copyright 2009 The Go Authors) |
| React and React DOM | 19.3.0 | MIT |
| Vite | 8.3.0 | MIT |
| `@xterm/xterm` | 6.0.0 | MIT |
| `@xterm/addon-unicode-graphemes` | 0.4.0 | MIT |
| `@tanstack/react-virtual` | 3.14.13 | MIT |
| Ajv | 8.20.0 | MIT |
| `geist` (Geist and Geist Mono variable fonts, bundled into the web app) | 1.7.2 | SIL Open Font License 1.1 (Copyright 2023 Vercel, in collaboration with basement.studio); text in `third_party/geist/LICENSE`; the two woff2 files are in `web/src/styles/fonts/` (SHA-256 `a369fcf5…` Geist, `fba8f577…` Geist Mono) |
| `qr` (QR decoder, `qr/decode.js` only) | 0.7.0 | MIT OR Apache-2.0 (Copyright Paul Miller; contains code inspired by ZXing, Apache-2.0, Copyright ZXing authors) |
| quicktype | 26.0.0 | Apache-2.0 |

## Website fonts (termivoa.com only, not in `pterm`)

| Font | License | License text |
| --- | --- | --- |
| Newsreader | SIL Open Font License 1.1 | `site/public/fonts/OFL-newsreader.txt` |
| Caveat | SIL Open Font License 1.1 | `site/public/fonts/OFL-caveat.txt` |
| JetBrains Mono | SIL Open Font License 1.1 | `site/public/fonts/OFL-jetbrainsmono.txt` |

The herdr logo on the website (`site/public/assets/herdr.png`) belongs to the
herdr project and is used only to link to its guide.

## Notes

The patched `ansi`, `xpty`, `vt`, and `ultraviolet` sources and their upstream MIT license texts are
retained under `third_party/`. Development-only tools, GitHub Actions,
packaging tools, fonts/icons, generated code notices, and all transitive
dependencies must be added after exact versions are locked. Except for the four
explicit local replacements, a name in this file does not mean the component
is currently vendored, linked, or shipped.

Apache-2.0 applies only to original Termivoa code. It does not replace any third-party license.
