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

type Details struct {
	Kind          Kind     `json:"kind"`
	Name          string   `json:"name"`
	FullPath      string   `json:"fullPath"`
	Size          string   `json:"size"`
	VirtualSize   string   `json:"virtualSize"`
	Experiment    *string  `json:"experiment"`
	BackingImages []string `json:"backingImages"`
	InUse         bool     `json:"inUse"`
}
