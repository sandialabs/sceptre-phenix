package file

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"phenix/util/mm"
)

// captureMagicBytes is how many first bytes of a file tell its capture format: a pcapng section header block gives its
// byte-order magic at offset 8.
const captureMagicBytes = 12

// The magics of classic pcap (microsecond and nanosecond, in each byte order), and pcapng's section header block type
// and byte-order magic.
const (
	pcapMicro        = 0xA1B2C3D4
	pcapMicroSwapped = 0xD4C3B2A1
	pcapNano         = 0xA1B23C4D
	pcapNanoSwapped  = 0x4D3CB2A1
	pcapngBlockType  = 0x0A0D0D0A
	pcapngByteOrder  = 0x1A2B3C4D
)

// captureFormatOf returns the capture format that the first bytes b of a file show: "pcap" for one of the four classic
// pcap magics in either byte order, "pcapng" for a section header block, else "".
func captureFormatOf(b []byte) string {
	if len(b) >= 4 { //nolint:mnd // the length of a magic
		switch binary.BigEndian.Uint32(b) {
		case pcapMicro, pcapMicroSwapped, pcapNano, pcapNanoSwapped:
			return mm.CaptureFormatPcap
		}
	}

	if len(b) >= captureMagicBytes && binary.BigEndian.Uint32(b) == pcapngBlockType {
		order := b[8:captureMagicBytes]
		if binary.BigEndian.Uint32(order) == pcapngByteOrder || binary.LittleEndian.Uint32(order) == pcapngByteOrder {
			return mm.CaptureFormatPcapng
		}
	}

	return ""
}

// sniffedName reports whether the capture format of a file named clean is read from its first bytes: a name that ends
// in .pcap, .pcapng or .cap, or that has no extension.
func sniffedName(clean string) bool {
	switch strings.ToLower(path.Ext(path.Base(clean))) {
	case "", ".pcap", ".pcapng", ".cap":
		return true
	}

	return false
}

// addCaptureFormats sets the capture format of each file of experiment exp that the headnode has as a regular file
// with a sniffed name (see sniffedName), from at most its first captureMagicBytes bytes. It opens no file through a
// symbolic link, and none out of the experiment's files directory. It stops with an error once ctx is done.
func addCaptureFormats(ctx context.Context, exp string, files Files) error {
	// a headnode without the directory has no file to read
	root, err := os.OpenRoot(mm.GetMMFullPath(filepath.Join(exp, "files")))
	if err != nil {
		return nil
	}

	defer root.Close()

	for i := range files {
		if files[i].IsDir || !sniffedName(files[i].Path) {
			continue
		}

		if err := ctx.Err(); err != nil {
			return fmt.Errorf("reading the capture formats of experiment %s: %w", exp, err)
		}

		files[i].CaptureFormat = rootCaptureFormat(root, files[i].Path)
	}

	return nil
}

// rootCaptureFormat is the capture format of the regular file clean of root, "" for another kind of file or a path
// through a symbolic link.
func rootCaptureFormat(root *os.Root, clean string) string {
	if info, err := root.Lstat(clean); err != nil || !info.Mode().IsRegular() {
		return ""
	}

	for dir := path.Dir(clean); dir != "."; dir = path.Dir(dir) {
		if info, err := root.Lstat(dir); err != nil || !info.IsDir() {
			return ""
		}
	}

	f, err := root.Open(clean)
	if err != nil {
		return ""
	}

	defer f.Close()

	return openCaptureFormat(f)
}

// LocalCaptureFormat is the capture format of the headnode file local, as the experiment file listing gives it: "pcap",
// "pcapng", or "" for a file that is not a capture or not a regular file.
func LocalCaptureFormat(local string) string {
	if info, err := os.Lstat(local); err != nil || !info.Mode().IsRegular() {
		return ""
	}

	f, err := os.Open(local)
	if err != nil {
		return ""
	}

	defer f.Close()

	return openCaptureFormat(f)
}

// openCaptureFormat is the capture format of the open file f, "" unless it is a regular file.
func openCaptureFormat(f *os.File) string {
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		return ""
	}

	b := make([]byte, captureMagicBytes)
	n, _ := io.ReadFull(f, b)

	return captureFormatOf(b[:n])
}
