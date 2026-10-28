package livecapture

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"phenix/util/mm/mmcli"
)

// meshReadTimeout bounds one read of a capture file on another node. It
// also runs the read on a connection of its own, so it never holds up other
// minimega commands.
const meshReadTimeout = 30 * time.Second

// meshReadScript prints a byte range of a file as base64: minimega returns a
// command's output as a string, which would not survive raw bytes. The file,
// offset and count are passed as arguments rather than written into the
// script, so none of them is ever parsed by the shell.
const meshReadScript = `dd if="$1" iflag=skip_bytes,count_bytes skip="$2" count="$3" bs=1048576 status=none | base64 -w 0`

var (
	// ErrUnsafePath is returned for a capture path phenix will not pass to a
	// remote node's shell command.
	ErrUnsafePath = errors.New("unsafe capture path")

	// remote capture paths are ones phenix built (an experiment name and a
	// file name, under the minimega files directory), so anything that would
	// need quoting in a minimega command is refused rather than escaped.
	safePath = regexp.MustCompile(`^/[A-Za-z0-9._+=@,:/-]+$`)
)

// source reads a capture file wherever minimega is writing it.
type source interface {
	// Read returns up to maxLen bytes of the file from off, fewer (or none)
	// past what has been written so far.
	Read(off int64, maxLen int) ([]byte, error)

	Close() error
}

// fileSource reads a capture the headnode is writing.
type fileSource struct {
	path string
	f    *os.File
	buf  []byte // reused: nothing keeps what Read returned past the next Read
}

func (s *fileSource) Read(off int64, maxLen int) ([]byte, error) {
	if s.f == nil {
		f, err := os.Open(s.path)
		if err != nil {
			return nil, fmt.Errorf("opening capture file: %w", err)
		}

		s.f = f
	}

	info, err := s.f.Stat()
	if err != nil {
		return nil, fmt.Errorf("getting capture file size: %w", err)
	}

	if info.Size() < off {
		return nil, fmt.Errorf("%w: %s shrank from %d to %d bytes", errTruncated, s.path, off, info.Size())
	}

	n := min(info.Size()-off, int64(maxLen))
	if n == 0 {
		return nil, nil
	}

	if int64(len(s.buf)) < n {
		s.buf = make([]byte, n)
	}

	read, err := s.f.ReadAt(s.buf[:n], off)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("reading capture file: %w", err)
	}

	return s.buf[:read], nil
}

func (s *fileSource) Close() error {
	if s.f == nil {
		return nil
	}

	return s.f.Close()
}

// meshSource reads a capture another node is writing, a byte range at a
// time, through minimega's mesh.
type meshSource struct {
	host string
	path string
}

func newMeshSource(host, capturePath string) (*meshSource, error) {
	if !safePath.MatchString(capturePath) || path.Clean(capturePath) != capturePath {
		return nil, fmt.Errorf("%w: %q", ErrUnsafePath, capturePath)
	}

	if !safePath.MatchString("/" + host) {
		return nil, fmt.Errorf("%w: host %q", ErrUnsafePath, host)
	}

	return &meshSource{host: host, path: capturePath}, nil
}

func (s *meshSource) Read(off int64, maxLen int) ([]byte, error) {
	cmd := mmcli.NewCommand()
	cmd.Command = fmt.Sprintf(
		"mesh send %s shell sh -c %s phenix %s %d %d",
		s.host, strconv.Quote(meshReadScript), s.path, off, maxLen,
	)
	cmd.Timeout = meshReadTimeout

	out, err := mmcli.SingleResponse(mmcli.Run(cmd))
	if err != nil {
		return nil, fmt.Errorf("reading %s on %s: %w", s.path, s.host, err)
	}

	data, err := decodeRange(out)
	if err != nil {
		return nil, fmt.Errorf("decoding %s from %s: %w", s.path, s.host, err)
	}

	return data, nil
}

// decodeRange decodes what meshReadScript printed.
func decodeRange(out string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(strings.TrimSpace(out)) //nolint:wrapcheck // wrapped by the caller
}

func (s *meshSource) Close() error { return nil }
