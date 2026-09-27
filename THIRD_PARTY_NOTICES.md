# Third-party notices

This file is a preparatory dependency inventory for private development. Direct and transitive versions are locked. Unsigned development artifacts can be built with the scripts under `packaging/`, but no release artifact has been distributed and the full notice audit is pending. Before a release candidate is assembled, generate an SBOM from the locked dependency manifests, inspect transitive dependencies, preserve required upstream `LICENSE` and `NOTICE` material, and replace this preparatory list with the exact shipped inventory.

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
| `golang.org/x/sys` | v0.47.0 | BSD-3-Clause |
| `rsc.io/qr` | v0.2.0 | BSD-3-Clause (Copyright 2009 The Go Authors) |
| React and React DOM | 19.3.0 | MIT |
| Vite | 8.3.0 | MIT |
| `@xterm/xterm` | 6.0.0 | MIT |
| `@xterm/addon-webgl` | 0.19.0 | MIT |
| `@xterm/addon-unicode-graphemes` | 0.4.0 | MIT |
| `@tanstack/react-virtual` | 3.14.13 | MIT |
| Ajv | 8.20.0 | MIT |
| quicktype | 26.0.0 | Apache-2.0 |

The patched `ansi`, `xpty`, `vt`, and `ultraviolet` sources and their upstream MIT license texts are
retained under `third_party/`. Development-only tools, GitHub Actions,
packaging tools, fonts/icons, generated code notices, and all transitive
dependencies must be added after exact versions are locked. Except for the four
explicit local replacements, a name in this file does not mean the component
is currently vendored, linked, or shipped.

Apache-2.0 applies only to original Phone Terminal code. It does not replace any third-party license.
