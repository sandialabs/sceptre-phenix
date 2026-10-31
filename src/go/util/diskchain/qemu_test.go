package diskchain_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/util/diskchain"
	"phenix/util/testenv"
)

// qemuImg runs qemu-img with args in dir, failing the test on an error.
func qemuImg(t *testing.T, dir string, args ...string) []byte {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "qemu-img", args...)
	cmd.Dir = dir

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("qemu-img %v: %v\n%s", args, err, out)
	}

	return out
}

// qemuChain is the chain `qemu-img info --backing-chain` reads from path, each file cleaned.
func qemuChain(t *testing.T, dir, path string) []string {
	t.Helper()

	var infos []struct {
		Filename string `json:"filename"`
	}

	if err := json.Unmarshal(qemuImg(t, dir, "info", "--backing-chain", "--output=json", path), &infos); err != nil {
		t.Fatalf("decoding qemu-img info: %v", err)
	}

	chain := make([]string, 0, len(infos))
	for _, info := range infos {
		chain = append(chain, filepath.Clean(info.Filename))
	}

	return chain
}

// Walk reads the chains qemu-img writes as qemu-img itself reads them: version 2 and 3 headers, absolute and
// relative backing names, each with a backing format as qemu-img requires, and a raw base. CI installs qemu-img.
func TestWalkMatchesQemuImg(t *testing.T) {
	testenv.RequireCommands(t, "qemu-img")
	t.Parallel()

	dir := t.TempDir()

	steps := [][]string{
		{"create", "-f", "raw", "base.img", "1M"},
		{"create", "-f", "qcow2", "-o", "compat=0.10", "-b", "base.img", "-F", "raw", "v2.qcow2"},
		{"create", "-f", "qcow2", "-b", filepath.Join(dir, "v2.qcow2"), "-F", "qcow2", "sub/abs.qcow2"},
		{"create", "-f", "qcow2", "-b", "abs.qcow2", "-F", "qcow2", "sub/rel.qcow2"},
	}

	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	for _, step := range steps {
		qemuImg(t, dir, step...)
	}

	for _, top := range []string{"v2.qcow2", "sub/abs.qcow2", "sub/rel.qcow2"} {
		path := filepath.Join(dir, top)

		res := diskchain.Walk(t.Context(), path)
		if res.Problem != diskchain.Whole {
			t.Errorf("Walk(%s) problem = %q, want a whole chain", top, res.Sentence())
		}

		got := make([]string, 0, len(res.Members))
		for _, member := range res.Members {
			got = append(got, filepath.Clean(member))
		}

		if diff := cmp.Diff(qemuChain(t, dir, path), got); diff != "" {
			t.Errorf("Walk(%s) members mismatch (-qemu-img +Walk):\n%s", top, diff)
		}
	}
}

// Walk finds the missing backing file of an image qemu-img cannot open.
func TestWalkFindsWhatQemuImgCannotOpen(t *testing.T) {
	testenv.RequireCommands(t, "qemu-img")
	t.Parallel()

	dir := t.TempDir()
	qemuImg(t, dir, "create", "-f", "qcow2", "-u", "-b", "ro_kali.qc2", "-F", "qcow2", "kali.qc2", "1M")

	info := exec.CommandContext(t.Context(), "qemu-img", "info", "--backing-chain", filepath.Join(dir, "kali.qc2"))
	if out, err := info.CombinedOutput(); err == nil {
		t.Fatalf("qemu-img info opened an image with a missing backing file:\n%s", out)
	}

	want := diskchain.Result{
		Members: []string{dir + "/kali.qc2"}, Problem: diskchain.Missing, At: dir + "/ro_kali.qc2",
	}

	if diff := cmp.Diff(want, diskchain.Walk(t.Context(), dir+"/kali.qc2")); diff != "" {
		t.Errorf("Walk mismatch (-want +got):\n%s", diff)
	}
}
