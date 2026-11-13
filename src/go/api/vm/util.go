package vm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"phenix/api/experiment"
	"phenix/util/common"
	"phenix/util/mm"
)

const snapshotOffset = 2

type qemuBackingChain struct {
	Filename    string `json:"filename"`
	BackingFile string `json:"backing-filename"`
}

// diskNameWithTstampRegex matches a file name with an earlier time: the text before and after the time.
var diskNameWithTstampRegex = regexp.MustCompile(`(?s)^(.*)_\d{14}(\D.*)?$`)

func GetNewDiskName(expName, vmName string) (string, error) {
	base, err := getVMImage(expName, vmName)
	if err != nil {
		return "", fmt.Errorf("getting base disk image: %w", err)
	}

	return newDiskName(base, time.Now()), nil
}

// newDiskName is the name of a new image made from the VM image base at now: base with the time in place of the
// last earlier one in its file name, or added to the end of the file name. It keeps the folder, a .qcow2, .qc2 or
// .qcow extension, since CommitToDisk writes into a copy of the image's base, which keeps its format, and adds .qc2
// to any other.
func newDiskName(base string, now time.Time) string {
	dir, file := filepath.Split(strings.TrimSuffix(base, filepath.Ext(base)))
	stamp := "_" + now.Format("20060102150405")

	// For example, web_20191117102805_v2 gives web_<now>_v2.
	if match := diskNameWithTstampRegex.FindStringSubmatch(file); match != nil {
		file = match[1] + stamp + match[2]
	} else {
		file += stamp
	}

	name := dir + file + filepath.Ext(base)

	if ext := filepath.Ext(name); ext != ".qcow2" && ext != ".qc2" && ext != ".qcow" {
		name += ".qc2"
	}

	return name
}

func getVMImage(expName, vmName string) (string, error) {
	exp, err := experiment.Get(expName)
	if err != nil {
		return "", fmt.Errorf("getting experiment %s: %w", expName, err)
	}

	vm := exp.Spec.Topology().FindNodeByName(vmName)
	if vm == nil {
		return "", fmt.Errorf("getting vm %s for experiment %s", vmName, expName)
	}

	drives := vm.Hardware().Drives()
	if len(drives) == 0 {
		return "", fmt.Errorf("vm %s for experiment %s has no drives", vmName, expName)
	}

	// base image from topology
	return drives[0].Image(), nil
}

// getBackingImage returns the base image of the disk path's backing chain, or the file a base that is a symlink
// names. A path without a folder is one of phenix's images. An empty backing chain is an error.
func getBackingImage(ctx context.Context, path string) (string, error) {
	if !filepath.IsAbs(path) {
		path = common.PhenixBase + "/images/" + path
	}

	chain, err := getImageBackingChain(ctx, path)
	if err != nil {
		return "", fmt.Errorf("getting image backing chain for %s: %w", path, err)
	}

	if len(chain) == 0 {
		return "", fmt.Errorf("getting image backing chain for %s: qemu-img returned no images", path)
	}

	// backing image should always be last image in chain
	path = chain[len(chain)-1].Filename

	stats, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("getting file stats for %s: %w", path, err)
	}

	// base image path is a symlink
	if stats.Mode()&os.ModeSymlink != 0 {
		origPath := path

		path, err = os.Readlink(path)
		if err != nil {
			return "", fmt.Errorf("getting symlink target for %s: %w", origPath, err)
		}
	}

	return path, nil
}

func getImageSnapshots(ctx context.Context, path string) ([]string, error) {
	chain, err := getImageBackingChain(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("getting image backing chain for %s: %w", path, err)
	}

	if len(chain) <= 1 {
		return nil, nil
	}

	var snapshots []string

	// range chain in reverse to get snapshots in correct order for rebasing
	// skip last entry since it will be the base image (not a snapshot)
	for i := len(chain) - snapshotOffset; i >= 0; i-- {
		snapshots = append(snapshots, chain[i].Filename)
	}

	return snapshots, nil
}

func getImageBackingChain(ctx context.Context, path string) ([]qemuBackingChain, error) {
	cmd := fmt.Sprintf("qemu-img info --backing-chain %s --output json", path)

	resp, err := mm.MeshShellRead(ctx, "", cmd)
	if err != nil {
		return nil, fmt.Errorf("getting image info for %s: %w", path, err)
	}

	var chain []qemuBackingChain

	if err := json.Unmarshal([]byte(resp), &chain); err != nil {
		return nil, fmt.Errorf("parsing image info for %s: %w", path, err)
	}

	return chain, nil
}

// copyProgressInterval is how often a copier reports its progress.
const copyProgressInterval = time.Second

type copier struct {
	subs     []chan float64
	interval time.Duration // how often it reports its progress
}

func newCopier(interval time.Duration) *copier {
	return &copier{subs: nil, interval: interval}
}

func (c *copier) subscribe() chan float64 {
	s := make(chan float64)

	c.subs = append(c.subs, s)

	return s
}

func (c copier) done() {
	for _, s := range c.subs {
		close(s)
	}
}

func (c copier) copy(ctx context.Context, src, dst string) error {
	defer c.done()

	in, err := os.Open(src)
	if err != nil {
		return err
	}

	defer func() { _ = in.Close() }()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}

	defer func() { _ = out.Close() }()

	pw := newProgressWriter(out)
	cr := newCancelableReader(ctx, in)

	var (
		done         = make(chan struct{})
		progressDone = make(chan struct{})
	)

	go func() {
		defer close(progressDone)

		var size int64
		if info, err := in.Stat(); err == nil {
			size = info.Size()
		}

		ticker := time.NewTicker(c.interval)
		defer ticker.Stop()

		for {
			n := pw.N()

			// a subscriber that stops reading does not hold up the copy's end
			for _, s := range c.subs {
				select {
				case s <- copyFraction(n, size):
				case <-done:
					return
				}
			}

			select {
			case <-done:
				return
			case <-ticker.C:
			}
		}
	}()

	_, err = io.Copy(pw, cr)

	close(done)

	// done closes the subscriptions, which the progress goroutine sends on
	<-progressDone

	if err != nil {
		return err
	}

	return out.Close()
}

// copyFraction is the fraction of size that n bytes are. An empty source has
// nothing left to copy, so its fraction is 1.
func copyFraction(n, size int64) float64 {
	if size <= 0 {
		return 1
	}

	return float64(n) / float64(size)
}

type progressWriter struct {
	mu sync.RWMutex

	w io.Writer
	n int64
}

func newProgressWriter(w io.Writer) *progressWriter {
	return &progressWriter{w: w} //nolint:exhaustruct // partial initialization
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)

	p.mu.Lock()
	defer p.mu.Unlock()

	p.n += int64(n)

	return n, err
}

func (p *progressWriter) N() int64 {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return p.n
}

type cancelableReader struct {
	ctx context.Context
	r   io.Reader
}

func newCancelableReader(ctx context.Context, r io.Reader) *cancelableReader {
	return &cancelableReader{ctx: ctx, r: r}
}

func (c cancelableReader) Read(p []byte) (int, error) {
	select {
	case <-c.ctx.Done():
		return 0, c.ctx.Err()
	default:
		return c.r.Read(p)
	}
}

// getTimestamp is the date, hour and minute of now, for the names of memory dumps and captures.
func getTimestamp(now time.Time) string {
	return now.Format("20060102_1504")
}
