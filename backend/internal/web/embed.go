// Package web embeds the built React dashboard so a single Go binary serves
// both the API and the UI — one process, one URL, no CORS in production.
//
// The real files land in dist/ via scripts/build.sh (or the Dockerfile); the
// committed placeholder keeps plain `go build ./...` working without Node.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var embedded embed.FS

// Dist returns the UI file tree.
func Dist() fs.FS {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		// Unreachable: the directory is part of the binary.
		panic(err)
	}
	return sub
}
