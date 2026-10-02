package disk

import (
	"encoding/json"
	"strings"
)

type Kind uint8

const (
	UNKNOWN Kind = 1 << iota
	VMImage
	ContainerImage
	ISOImage
)

var knownImageExtensions = []string{".qcow2", ".qc2", "_rootfs.tgz", ".hdd", ".iso"} //nolint:gochecknoglobals // global constant

func (k Kind) MarshalJSON() ([]byte, error) {
	return json.Marshal(k.String())
}

func (k Kind) String() string {
	switch k {
	case VMImage:
		return "VM"
	case ContainerImage:
		return "Container"
	case ISOImage:
		return "ISO"
	case UNKNOWN:
		fallthrough
	default:
		return "Unknown"
	}
}

func StringToKind(kind string) Kind {
	switch strings.ToLower(kind) {
	case "vm":
		return VMImage
	case "iso":
		return ISOImage
	case "container":
		return ContainerImage
	default:
		return UNKNOWN
	}
}

// Details describes a disk image, which is identified by its full path.
type Details struct {
	Kind Kind `json:"kind"`
	// Name is the image's file name, in whatever folder it is: the name roles
	// are checked against.
	Name string `json:"name"`
	// FullPath is the image's cleaned absolute path.
	FullPath string `json:"fullPath"`
	// RelativePath is FullPath within the minimega files directory, or ""
	// when the image is outside it.
	RelativePath string `json:"relativePath"`
	// OutsideFilesDir is set for an image outside the minimega files
	// directory, which minimega does not copy to other cluster nodes.
	OutsideFilesDir bool `json:"outsideFilesDir"`
	// ReadOnly is set when the disk actions refuse the image: it is outside
	// the minimega files directory, or in a folder the listing leaves out (see
	// Resolve).
	ReadOnly    bool            `json:"readOnly"`
	Size        string          `json:"size"`
	VirtualSize string          `json:"virtualSize"`
	Experiments []ExperimentUse `json:"experiments"`
	// BackingImages are the full paths of the images this one is backed by,
	// nearest first.
	BackingImages []string `json:"backingImages"`
	InUse         bool     `json:"inUse"`
}

// ExperimentUse names an experiment whose topology uses a disk, directly or
// as the backing image of a disk it uses.
type ExperimentUse struct {
	Name    string `json:"name"`
	Running bool   `json:"running"`
}
