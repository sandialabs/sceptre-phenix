package util

import (
	"html/template"
	"io"
	"net/http"
)

type BinaryFileSystem struct {
	assets http.FileSystem
}

func NewBinaryFileSystem(assets http.FileSystem) *BinaryFileSystem {
	return &BinaryFileSystem{assets: assets}
}

func (b BinaryFileSystem) ServeTemplate(w http.ResponseWriter, name string, data any) {
	f, err := b.assets.Open(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	defer func() { _ = f.Close() }()

	body, err := io.ReadAll(f)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	tmpl := template.Must(template.New(name).Parse(string(body)))
	_ = tmpl.Execute(w, data)
}
