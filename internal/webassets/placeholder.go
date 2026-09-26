//go:build !webdist

package webassets

import "embed"

const root = "placeholder"

//go:embed placeholder
var files embed.FS
