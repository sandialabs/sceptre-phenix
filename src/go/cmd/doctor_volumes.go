package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"phenix/api/disk"
	"phenix/util/partition"
)

const (
	// volumeReadBlock is the unit in which doctor reads the disk of an image for the state of a file system.
	volumeReadBlock = 64 << 10
	// volumeReadMax bounds the bytes that doctor reads from one image for the states of its file systems.
	volumeReadMax = 8 * volumeReadBlock

	// Phrases in the details of rows, which the hint looks for.
	ntfsDirtyPhrase = partition.ReasonNTFSDirty
	ntfsLogPhrase   = "Windows did not shut down fully"
	extErrorsPhrase = partition.ReasonExtHasErrors
)

var errVolumeReadLimit = fmt.Errorf("doctor read the limit of %d KiB of the image", volumeReadMax/kib)

// imageBlocks reads the disk that an image holds, in blocks of volumeReadBlock bytes that it keeps, and at most
// volumeReadMax bytes in all. read reads n bytes at off of the disk.
type imageBlocks struct {
	size   int64
	read   func(off int64, n int) ([]byte, error)
	blocks map[int64][]byte
	left   int64
	dir    string // the folder of the qemu-img output, or ""
}

// newImageBlocks reads the disk of the image at path that found describes: a raw image with readAt, and a qcow2 or
// qcow image through its backing chain with `qemu-img dd -U`, as disk.InspectPath does for the first 16 MiB.
func newImageBlocks(ctx context.Context, in *doctorInput, path, qemuImg string, found disk.Inspection) *imageBlocks {
	b := &imageBlocks{
		size: max(found.VirtualBytes, found.FileBytes), read: nil, blocks: make(map[int64][]byte), left: volumeReadMax,
		dir: "",
	}

	if found.Qcow == nil {
		b.read = func(off int64, n int) ([]byte, error) { return in.env.readAt(path, off, n) }

		return b
	}

	format := "qcow2"
	if found.Qcow.Compat == "" {
		format = "qcow"
	}

	b.size = found.VirtualBytes
	b.read = func(off int64, n int) ([]byte, error) { return b.qemuRead(ctx, qemuImg, path, format, off, n) }

	return b
}

// close removes the folder of the qemu-img output.
func (b *imageBlocks) close() {
	if b.dir != "" {
		_ = os.RemoveAll(b.dir)
	}
}

func (b *imageBlocks) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, fmt.Errorf("reading the disk image at offset %d", off)
	}

	if off >= b.size {
		return 0, io.EOF
	}

	end := min(off+int64(len(p)), b.size)
	n := 0

	for pos := off; pos < end; {
		block, err := b.block(pos / volumeReadBlock)
		if err != nil {
			return n, err
		}

		i := pos % volumeReadBlock
		if i >= int64(len(block)) {
			return n, io.ErrUnexpectedEOF
		}

		c := copy(p[n:end-off], block[i:])
		n += c
		pos += int64(c)
	}

	if n < len(p) {
		return n, io.EOF
	}

	return n, nil
}

// block returns block i of the disk, and reads it when it does not have it.
func (b *imageBlocks) block(i int64) ([]byte, error) {
	if data, ok := b.blocks[i]; ok {
		return data, nil
	}

	start := i * volumeReadBlock
	length := min(volumeReadBlock, b.size-start)

	if length > b.left {
		return nil, errVolumeReadLimit
	}

	b.left -= length

	data, err := b.read(start, int(length))
	if err != nil {
		return nil, err
	}

	if int64(len(data)) != length {
		return nil, fmt.Errorf("read %d bytes of the disk image at %d, not %d", len(data), start, length)
	}

	b.blocks[i] = data

	return data, nil
}

// qemuRead reads n bytes at off of the disk of the image at path, of format, with one qemu-img run: a raw node with an
// offset and a size over the format node limits the read. It opens the image read-only and shares it (-U).
func (b *imageBlocks) qemuRead(ctx context.Context, qemuImg, path, format string, off int64, n int) ([]byte, error) {
	if b.dir == "" {
		dir, err := os.MkdirTemp("", "phenix-doctor-")
		if err != nil {
			return nil, fmt.Errorf("creating a folder for qemu-img: %w", err)
		}

		b.dir = dir
	}

	out := filepath.Join(b.dir, "read-"+strconv.FormatInt(off, 10))
	opts := fmt.Sprintf("driver=raw,offset=%d,size=%d,file.driver=%s,file.file.driver=file,file.file.filename=%s",
		off, n, format, strings.ReplaceAll(path, ",", ",,"))

	cmd := exec.CommandContext(ctx, qemuImg, //nolint:gosec // G204 the arguments are not run by a shell
		"dd", "-U", "--image-opts", "-O", "raw", "bs="+strconv.Itoa(n), "count=1", "if="+opts, "of="+out)
	cmd.WaitDelay = commandWaitDelay

	if output, err := cmd.CombinedOutput(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("qemu-img dd: %w", ctxErr)
		}

		if msg := strings.TrimSpace(string(output)); msg != "" {
			return nil, errors.New(msg)
		}

		return nil, fmt.Errorf("qemu-img dd: %w", err)
	}

	defer func() { _ = os.Remove(out) }()

	data, err := os.ReadFile(out)
	if err != nil {
		return nil, fmt.Errorf("reading the qemu-img dd output: %w", err)
	}

	return data, nil
}

// volumeProblem reads the state of the NTFS or ext file system in partition number of the disk that image holds, at
// path, and says why minimega cannot write files to it, or why doctor cannot tell, as a note.
func (in *doctorInput) volumeProblem(ctx context.Context, image *injectImage, path string, number int, qemuImg string) (
	string, string,
) {
	i := slices.IndexFunc(image.found.Partitions, func(p disk.Partition) bool { return p.Number == number })
	if i < 0 || image.found.Partitions[i].Filesystem == nil {
		return "", ""
	}

	p := image.found.Partitions[i]

	var (
		check  func(io.ReaderAt, int64) (partition.VolumeState, error)
		name   = p.Filesystem.Type
		result string
	)

	switch name {
	case partition.FSNTFS:
		check, name = partition.CheckNTFS, "NTFS"
		result = "so the mount at a start fails (wrong fs type, bad superblock) or gives a read-only file system"
	case partition.FSExt2, partition.FSExt3, partition.FSExt4:
		check = partition.CheckExt
		result = "so Linux can make it read-only while minimega writes the files"
	default:
		return "", ""
	}

	if image.blocks == nil {
		image.blocks = newImageBlocks(ctx, in, path, qemuImg, image.found)
	}

	state, err := check(io.NewSectionReader(image.blocks, p.Start, p.Size), p.Size)

	switch {
	case ctx.Err() != nil:
		return "", fmt.Sprintf("doctor stopped reading images after %s", in.timeout)
	case err != nil:
		return "", fmt.Sprintf("doctor cannot read the state of its %s file system: %v", name, err)
	case state.Reason != "":
		return fmt.Sprintf("partition %d holds %s, but %s, %s", number, name, state.Reason, result), ""
	}

	return "", ""
}
