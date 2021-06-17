package file

import (
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

type ExperimentFile struct {
	Name     string
	Date     string
	Size     int
	Category string

	// Internal use to aid in sorting
	DateTime time.Time
}

type ExperimentFiles []ExperimentFile

func (this ExperimentFiles) SortByName(asc bool) {
	sort.Slice(this, func(i, j int) bool {
		if asc {
			return strings.ToLower(this[i].Name) < strings.ToLower(this[j].Name)
		}

		return strings.ToLower(this[i].Name) > strings.ToLower(this[j].Name)
	})
}

func (this ExperimentFiles) SortByDate(asc bool) {
	sort.Slice(this, func(i, j int) bool {
		if asc {
			return this[i].DateTime.Before(this[j].DateTime)
		}

		return this[i].DateTime.After(this[j].DateTime)
	})
}

func (this ExperimentFiles) SortBySize(asc bool) {
	sort.Slice(this, func(i, j int) bool {
		if asc {
			return this[i].Size < this[j].Size
		}

		return this[i].Size > this[j].Size
	})
}

func (this ExperimentFiles) SortByCategory(asc bool) {
	sort.Slice(this, func(i, j int) bool {
		if asc {
			return strings.ToLower(this[i].Category) < strings.ToLower(this[j].Category)
		}

		return strings.ToLower(this[i].Category) > strings.ToLower(this[j].Category)
	})
}

func (this ExperimentFiles) SortBy(col string, asc bool) {
	switch col {
	case "name":
		this.SortByName(asc)
	case "date":
		this.SortByDate(asc)
	case "size":
		this.SortBySize(asc)
	case "category":
		this.SortByCategory(asc)
	}
}

func (this ExperimentFiles) Paginate(page, size int) ExperimentFiles {
	var (
		start = (page - 1) * size
		end   = start + size
	)

	if start >= len(this) {
		return ExperimentFiles{}
	}

	if end > len(this) {
		end = len(this)
	}

	return this[start:end]
}
