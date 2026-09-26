//go:build webdist

package webassets

import "embed"

const root = "dist"

// dist is a gitignored copy of web/dist made by scripts/embed-web.sh.
//
//go:embed all:dist
var files embed.FS
