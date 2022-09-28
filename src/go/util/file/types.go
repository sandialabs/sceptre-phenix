package file

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type ImageKind int
type CopyStatus func(float64)

const (
	_ ImageKind = iota
	VM_IMAGE
	CONTAINER_IMAGE
)

type ImageDetails struct {
	Kind     ImageKind
	Name     string
	FullPath string
	Size     int
}

type File struct {
	Name       string   `json:"name"`
	Path       string   `json:"path"`
	Date       string   `json:"date"`
	Size       int64    `json:"size"`
	Categories []string `json:"categories"`
	PlainText  bool     `json:"plainText"`
	IsDir	   bool     `json:"isDir"`

	// Internal use to aid in sorting
	dateTime time.Time
}

type Files []File

func (this Files) SortByName(asc bool) {
	sort.Slice(this, func(i, j int) bool {
		if asc {
			return strings.ToLower(this[i].Name) < strings.ToLower(this[j].Name)
		}

		return strings.ToLower(this[i].Name) > strings.ToLower(this[j].Name)
	})
}

func (this Files) SortByDate(asc bool) {
	sort.Slice(this, func(i, j int) bool {
		if asc {
			return this[i].dateTime.Before(this[j].dateTime)
		}

		return this[i].dateTime.After(this[j].dateTime)
	})
}

func (this Files) SortBySize(asc bool) {
	sort.Slice(this, func(i, j int) bool {
		if asc {
			return this[i].Size < this[j].Size
		}

		return this[i].Size > this[j].Size
	})
}

/*
func (this Files) SortByCategory(asc bool) {
	sort.Slice(this, func(i, j int) bool {
		if asc {
			return strings.ToLower(this[i].Category) < strings.ToLower(this[j].Category)
		}

		return strings.ToLower(this[i].Category) > strings.ToLower(this[j].Category)
	})
}
*/

func (this Files) SortBy(col string, asc bool) {
	switch col {
	case "name":
		this.SortByName(asc)
	case "date":
		this.SortByDate(asc)
	case "size":
		this.SortBySize(asc)
		// case "category":
		// this.SortByCategory(asc)
	}
}

func (this Files) Paginate(page, size int) Files {
	var (
		start = (page - 1) * size
		end   = start + size
	)

	if start >= len(this) {
		return Files{}
	}

	if end > len(this) {
		end = len(this)
	}

	return this[start:end]
}

// create an instance of our file object using a FileInfo and path
func MakeFile(file fs.FileInfo, basePath string) (File) {
	f := File{}
	f.Name = file.Name()
	f.Path = filepath.Join(basePath, file.Name())
	f.dateTime = file.ModTime()
	f.Date = file.ModTime().Format(time.RFC3339)
	f.Size = file.Size()
	f.IsDir = file.IsDir()

	return f
}
