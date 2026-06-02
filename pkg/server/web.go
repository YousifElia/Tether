package server

import (
	"embed"
	"io/fs"
)

//go:embed web
var webFS embed.FS

// staticFS returns the embedded web/ directory rooted so that index.html is "/".
func staticFS() fs.FS {
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		panic(err) // the embed path is a compile-time constant
	}
	return sub
}
