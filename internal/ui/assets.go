package ui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var embeddedDist embed.FS

// Assets returns the filesystem rooted inside the dist directory.
func Assets() (fs.FS, error) {
	return fs.Sub(embeddedDist, "dist")
}
