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

type qemuBackingChain struct {
	Filename    string `json:"filename"`
	BackingFile string `json:"backing-filename"`
}

var diskNameWithTstampRegex = regexp.MustCompile(`(.*)_\d{14}`)

func GetNewDiskName(expName, vmName string) (string, error) {
	base, err := getVMImage(expName, vmName)
	if err != nil {
		return "", fmt.Errorf("getting base disk image: %w", err)
	}

	name := strings.TrimSuffix(base, filepath.Ext(base))

	// For example, if name = ubuntu_server_20191117102805, then this
	// will match and match[1] will be `ubuntu_server`.
	if match := diskNameWithTstampRegex.FindStringSubmatch(name); match != nil {
		name = match[1]
	}

	name = name + "_" + time.Now().Format("20060102150405") + filepath.Ext(base)

	if ext := filepath.Ext(name); ext != ".qcow2" && ext != ".qc2" {
		name += ".qc2"
	}

	return name, nil
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

	// base image from topology
	return vm.Hardware().Drives()[0].Image(), nil
}

func getBackingImage(path string) (string, error) {
	if !filepath.IsAbs(path) {
		path = common.PhenixBase + "/images/" + path
	}

	chain, err := getImageBackingChain(path)
	if err != nil {
		return "", fmt.Errorf("getting image backing chain for %s: %w", path, err)
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

func getImageSnapshots(path string) ([]string, error) {
	chain, err := getImageBackingChain(path)
	if err != nil {
		return nil, fmt.Errorf("getting image backing chain for %s: %w", path, err)
	}

	if len(chain) <= 1 {
		return nil, nil
	}

	var snapshots []string

	// range chain in reverse to get snapshots in correct order for rebasing
	// skip last entry since it will be the base image (not a snapshot)
	for i := len(chain) - 2; i >= 0; i-- {
		snapshots = append(snapshots, chain[i].Filename)
	}

	return snapshots, nil
}

func getImageBackingChain(path string) ([]qemuBackingChain, error) {
	cmd := fmt.Sprintf("qemu-img info --backing-chain %s --output json", path)

	resp, err := mm.MeshShellResponse("", cmd)
	if err != nil {
		return nil, fmt.Errorf("getting image info for %s: %w", path, err)
	}

	var chain []qemuBackingChain

	if err := json.Unmarshal([]byte(resp), &chain); err != nil {
		return nil, fmt.Errorf("parsing image info for %s: %w", path, err)
	}

	return chain, nil
}

type copier struct {
	subs []chan float64
}

func newCopier() *copier {
	return new(copier)
}

func (this *copier) subscribe() chan float64 {
	s := make(chan float64)

	this.subs = append(this.subs, s)

	return s
}

func (this copier) done() {
	for _, s := range this.subs {
		close(s)
	}
}

func (this copier) copy(ctx context.Context, src, dst string) error {
	defer this.done()

	in, err := os.Open(src)
	if err != nil {
		return err
	}

	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}

	defer out.Close()

	pw := newProgressWriter(out)
	cr := newCancelableReader(ctx, in)
	done := make(chan struct{})

	go func() {
		info, _ := in.Stat()
		size := info.Size()

		for {
			select {
			case <-done:
				return
			default:
				n := pw.N()

				for _, s := range this.subs {
					s <- float64(n) / float64(size)
				}

				time.Sleep(1 * time.Second)
			}
		}
	}()

	_, err = io.Copy(pw, cr)
	close(done)

	if err != nil {
		return err
	}

	return out.Close()
}

type progressWriter struct {
	sync.RWMutex

	w io.Writer
	n int64
}

func newProgressWriter(w io.Writer) *progressWriter {
	return &progressWriter{w: w}
}

func (this *progressWriter) Write(b []byte) (int, error) {
	n, err := this.w.Write(b)

	this.Lock()
	defer this.Unlock()

	this.n += int64(n)

	return n, err
}

func (this *progressWriter) N() int64 {
	this.RLock()
	defer this.RUnlock()

	return this.n
}

type cancelableReader struct {
	ctx context.Context
	r   io.Reader
}

func newCancelableReader(ctx context.Context, r io.Reader) *cancelableReader {
	return &cancelableReader{ctx: ctx, r: r}
}

func (this cancelableReader) Read(p []byte) (int, error) {
	select {
	case <-this.ctx.Done():
		return 0, this.ctx.Err()
	default:
		return this.r.Read(p)
	}
}

func getTimestamp() string {

	return time.Now().Format("20060102_1500")
}
