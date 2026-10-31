package disk

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// readBlock is the unit in which a qemuReader reads and keeps the disk.
	readBlock = 64 << 10

	// maxDiskReadMiB bounds the MiB of a disk that one inspection reads.
	maxDiskReadMiB = 16
	maxDiskRead    = maxDiskReadMiB << 20
)

// errReadLimit is returned once an inspection has read maxDiskRead bytes.
var errReadLimit = fmt.Errorf("phenix read the limit of %d MiB", maxDiskReadMiB)

// readBudget is how many more bytes an inspection may read.
type readBudget struct {
	left int64
}

func (b *readBudget) take(n int64) error {
	if n > b.left {
		return errReadLimit
	}

	b.left -= n

	return nil
}

// fileReader reads a raw image directly, within a budget, until ctx is done.
type fileReader struct {
	ctx    context.Context //nolint:containedctx // the inspection's deadline
	f      *os.File
	budget *readBudget
}

func (r fileReader) ReadAt(p []byte, off int64) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, fmt.Errorf("reading the disk image: %w", err)
	}

	if err := r.budget.take(int64(len(p))); err != nil {
		return 0, err
	}

	n, err := r.f.ReadAt(p, off)
	if err != nil && !errors.Is(err, io.EOF) {
		return n, fmt.Errorf("reading the disk image: %w", err)
	}

	return n, err
}

// qemuReader reads the disk that a qcow or qcow2 image holds, through its backing chain, with `qemu-img dd`. It
// reads whole blocks of readBlock bytes, keeps them, and reads only the blocks it does not have. It opens the image
// read-only and shares it with running VMs (-U), so it never writes to it.
type qemuReader struct {
	ctx     context.Context //nolint:containedctx // the inspection's deadline
	qemuImg string
	path    string
	format  string
	size    int64
	dir     string
	budget  *readBudget
	blocks  map[int64][]byte
	runs    int
}

func newQemuReader(ctx context.Context, qemuImg, path, format string, size int64, dir string, budget *readBudget) *qemuReader {
	return &qemuReader{
		ctx: ctx, qemuImg: qemuImg, path: path, format: format, size: size, dir: dir, budget: budget,
		blocks: make(map[int64][]byte), runs: 0,
	}
}

func (q *qemuReader) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, fmt.Errorf("reading the disk image at offset %d", off)
	}

	if off >= q.size {
		return 0, io.EOF
	}

	end := min(off+int64(len(p)), q.size)
	first, last := off/readBlock, (end-1)/readBlock

	for b := first; b <= last; {
		if q.blocks[b] != nil {
			b++

			continue
		}

		run := b
		for run <= last && q.blocks[run] == nil {
			run++
		}

		if err := q.fetch(b, run); err != nil {
			return 0, err
		}

		b = run
	}

	n := 0
	for pos := off; pos < end; {
		block := q.blocks[pos/readBlock]
		c := copy(p[n:end-off], block[pos%readBlock:])
		n += c
		pos += int64(c)
	}

	if n < len(p) {
		return n, io.EOF
	}

	return n, nil
}

// fetch reads blocks from up to but not including to with one qemu-img run: a raw node with an offset and a size
// over the image's format node limits the read to those blocks.
func (q *qemuReader) fetch(from, to int64) error {
	start := from * readBlock
	length := min(to*readBlock, q.size) - start

	if err := q.budget.take(length); err != nil {
		return err
	}

	out := filepath.Join(q.dir, "read-"+strconv.Itoa(q.runs))
	q.runs++

	opts := fmt.Sprintf("driver=raw,offset=%d,size=%d,file.driver=%s,file.file.driver=file,file.file.filename=%s",
		start, length, q.format, strings.ReplaceAll(q.path, ",", ",,"))

	cmd := exec.CommandContext(q.ctx, q.qemuImg, //nolint:gosec // G204 the arguments are not run by a shell
		"dd", "-U", "--image-opts", "-O", "raw", "bs="+strconv.FormatInt(length, 10), "count=1", "if="+opts, "of="+out)
	cmd.WaitDelay = time.Second

	if output, err := cmd.CombinedOutput(); err != nil {
		if ctxErr := q.ctx.Err(); ctxErr != nil {
			return fmt.Errorf("qemu-img dd: %w", ctxErr)
		}

		if msg := strings.TrimSpace(string(output)); msg != "" {
			return errors.New(msg)
		}

		return fmt.Errorf("qemu-img dd: %w", err)
	}

	data, err := os.ReadFile(out)
	_ = os.Remove(out)

	if err != nil {
		return fmt.Errorf("reading the qemu-img dd output: %w", err)
	}

	if int64(len(data)) != length {
		return fmt.Errorf("qemu-img dd wrote %d bytes, not %d", len(data), length)
	}

	for b := from; b < to; b++ {
		lo := (b - from) * readBlock
		q.blocks[b] = data[lo:min(lo+readBlock, length)]
	}

	return nil
}
