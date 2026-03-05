package file

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type (
	ImageKind  int
	CopyStatus func(float64)
)

const (
	_ ImageKind = iota
	VMImage
	ContainerImage
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
	IsDir      bool     `json:"isDir"`

	// Internal use to aid in sorting
	dateTime time.Time
}

type Files []File

func (f Files) SortByName(asc bool) {
	sort.Slice(f, func(i, j int) bool {
		if asc {
			return strings.ToLower(f[i].Name) < strings.ToLower(f[j].Name)
		}

		return strings.ToLower(f[i].Name) > strings.ToLower(f[j].Name)
	})
}

func (f Files) SortByDate(asc bool) {
	sort.Slice(f, func(i, j int) bool {
		if asc {
			return f[i].dateTime.Before(f[j].dateTime)
		}

		return f[i].dateTime.After(f[j].dateTime)
	})
}

func (f Files) SortBySize(asc bool) {
	sort.Slice(f, func(i, j int) bool {
		if asc {
			return f[i].Size < f[j].Size
		}

		return f[i].Size > f[j].Size
	})
}

/*
func (f Files) SortByCategory(asc bool) {
	sort.Slice(f, func(i, j int) bool {
		if asc {
			return strings.ToLower(this[i].Category) < strings.ToLower(this[j].Category)
		}

		return strings.ToLower(this[i].Category) > strings.ToLower(this[j].Category)
	})
}
*/

func (f Files) SortBy(col string, asc bool) {
	switch col {
	case "name":
		f.SortByName(asc)
	case "date":
		f.SortByDate(asc)
	case "size":
		f.SortBySize(asc)
		// case "category":
		// this.SortByCategory(asc)
	}
}

func (f Files) Paginate(page, size int) Files {
	var (
		start = (page - 1) * size
		end   = start + size
	)

	if start >= len(f) {
		return Files{}
	}

	if end > len(f) {
		end = len(f)
	}

	return f[start:end]
}

// MakeFile creates an instance of our file object using a FileInfo and path.
func MakeFile(file fs.FileInfo, basePath string) File {
	f := File{} //nolint:exhaustruct // partial initialization
	f.Name = file.Name()
	f.Path = filepath.Join(basePath, file.Name())
	f.dateTime = file.ModTime()
	f.Date = file.ModTime().Format(time.RFC3339)
	f.Size = file.Size()
	f.IsDir = file.IsDir()

	return f
}
