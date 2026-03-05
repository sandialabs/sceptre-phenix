package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed public
var publicFS embed.FS

func GetAssets() (http.FileSystem, error) {
	subFS, err := fs.Sub(publicFS, "public")
	if err != nil {
		return nil, err
	}
	return http.FS(subFS), nil
}
