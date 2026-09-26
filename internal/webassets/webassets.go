// Package webassets holds the website embedded into the gateway. A normal
// build embeds a tiny placeholder; release builds copy web/dist into dist/
// (scripts/embed-web.sh) and build with -tags webdist.
package webassets

import (
	"embed"
	"io/fs"
)

// FS is the site root: index.html plus its assets.
var FS fs.FS = mustSub(files, root)

func mustSub(fsys embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(err)
	}
	return sub
}
