// Package web embeds the built Vue app (web/dist) into the server binary.
// `make web` builds it; during development use `make dev-web` (Vite on :5173).
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

func Dist() fs.FS {
	sub, _ := fs.Sub(dist, "dist")
	return sub
}
