package util

import (
	"html/template"
	"io"
	"net/http"

	assetfs "github.com/elazarl/go-bindata-assetfs"
)

type BinaryFileSystem struct {
	assets *assetfs.AssetFS
}

func NewBinaryFileSystem(assets *assetfs.AssetFS) *BinaryFileSystem {
	return &BinaryFileSystem{assets: assets}
}

func (this BinaryFileSystem) ServeFile(w http.ResponseWriter, r *http.Request, name string) {
	f, err := this.assets.Open(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	defer f.Close()

	d, err := f.Stat()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.ServeContent(w, r, d.Name(), d.ModTime(), f)
}

func (this BinaryFileSystem) ServeTemplate(w http.ResponseWriter, name string, data interface{}) {
	f, err := this.assets.Open(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	defer f.Close()

	body, err := io.ReadAll(f)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	tmpl := template.Must(template.New(name).Parse(string(body)))
	tmpl.Execute(w, data)
}
