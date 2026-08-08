package web

import (
	"embed"
	"io/fs"
	"net/http"
)

// the all: prefix is required: without it embed skips files starting with
// '_' or '.', and Vite emits shared chunks named like _baseIsEqual-<hash>.js
//
//go:embed all:public
var publicFS embed.FS

func GetAssets() (http.FileSystem, error) {
	subFS, err := fs.Sub(publicFS, "public")
	if err != nil {
		return nil, err
	}
	return http.FS(subFS), nil
}
