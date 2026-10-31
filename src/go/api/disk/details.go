package disk

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"phenix/util/diskchain"
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
	// BrokenChain is set when qemu cannot open the image, as phenix sees the
	// files: a file in its backing chain is missing, or the chain loops or is
	// too deep. BackingImages then holds the part of the chain phenix read.
	BrokenChain bool `json:"brokenChain"`
	// ChainProblem says what breaks the chain in one sentence, or is "" when
	// BrokenChain is not set.
	ChainProblem string `json:"chainProblem"`
	// MissingBacking is the full path of the missing backing file, or "" for a
	// chain that loops, is too deep, or is not broken.
	MissingBacking string `json:"missingBacking"`
	// Format is the image's format as qemu-img names it, such as qcow2, qcow (version 1), raw or vmdk, or "" when it
	// is not known.
	Format string `json:"format"`
	// FormatVersion is the version of a qcow2 or qcow image from its header, or 0.
	FormatVersion int `json:"formatVersion"`
	// FormatIssue is why the image's format needs care, or nil.
	FormatIssue *FormatIssue `json:"formatIssue"`
}

// Refusal returns an error that says why qemu cannot open d read/write, or nil when its format issue, if any, is not
// an error.
func (d Details) Refusal() error {
	if d.FormatIssue == nil || d.FormatIssue.Level != IssueError {
		return nil
	}

	return errors.New(d.FormatIssue.Problem)
}

// setBreak records in d the break res, a cleaned walk of d's chain (see
// brokenChain), and the part of the chain before it.
func (d *Details) setBreak(res diskchain.Result) {
	d.BrokenChain = true
	d.ChainProblem = res.Sentence()

	if len(res.Members) > 1 {
		d.BackingImages = slices.Clone(res.Members[1:])
	}

	if res.Problem == diskchain.Missing {
		d.MissingBacking = res.At
	}
}

// setChain records in d what res, a cleaned walk of d's chain (see brokenChain), found: a break, or an image that
// qemu refuses, with the part of the chain before it.
func (d *Details) setChain(res diskchain.Result) {
	switch {
	case res.Broken():
		d.setBreak(res)
	case res.Refused():
		if len(d.BackingImages) == 0 && len(res.Members) > 1 {
			d.BackingImages = slices.Clone(res.Members[1:])
		}

		d.setRefusedBacking(res)
	}
}

// ExperimentUse names an experiment whose topology uses a disk, directly or
// as the backing image of a disk it uses.
type ExperimentUse struct {
	Name    string `json:"name"`
	Running bool   `json:"running"`
}
