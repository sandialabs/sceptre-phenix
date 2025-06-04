package disk

import (
	"encoding/json"
	"strings"
)

type Kind uint8

const (
	UNKNOWN Kind = 1 << iota
	VM_IMAGE
	CONTAINER_IMAGE
	ISO_IMAGE
)

var knownImageExtensions = []string{".qcow2", ".qc2", "_rootfs.tgz", ".hdd", ".iso"}

func (k Kind) MarshalJSON() ([]byte, error) {
	return json.Marshal(k.String())
}

func (k Kind) String() string {
	switch k {
	case VM_IMAGE:
		return "VM"
	case CONTAINER_IMAGE:
		return "Container"
	case ISO_IMAGE:
		return "ISO"
	default:
		return "Unknown"

	}
}

func StringToKind(kind string) Kind {
	switch strings.ToLower(kind) {
	case "vm":
		return VM_IMAGE
	case "iso":
		return ISO_IMAGE
	case "container":
		return CONTAINER_IMAGE
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
