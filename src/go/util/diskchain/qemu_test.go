package diskchain_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

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

// ParseHeader reads the format, version, encryption and feature bits of the images qemu-img writes.
func TestParseHeaderMatchesQemuImg(t *testing.T) {
	testenv.RequireCommands(t, "qemu-img")
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "passphrase.txt"), []byte("kalipass"), 0o600); err != nil {
		t.Fatalf("writing the passphrase: %v", err)
	}

	secret := []string{"--object", "secret,id=sec0,file=passphrase.txt"}

	tests := []struct {
		name string
		args []string
		want diskchain.Header
	}{
		{
			name: "v2.qc2", args: []string{"-f", "qcow2", "-o", "compat=0.10"},
			want: diskchain.Header{Format: diskchain.FormatQcow2, Version: 2},
		},
		{name: "v3.qc2", args: []string{"-f", "qcow2"}, want: diskchain.Header{Format: diskchain.FormatQcow2, Version: 3}},
		{name: "kali.qcow", args: []string{"-f", "qcow"}, want: diskchain.Header{Format: diskchain.FormatQcow, Version: 1}},
		{
			name: "aes.qcow", args: append(slices.Clone(secret), "-f", "qcow", "-o", "encrypt.format=aes,encrypt.key-secret=sec0"),
			want: diskchain.Header{Format: diskchain.FormatQcow, Version: 1, Encryption: diskchain.EncryptionAES},
		},
		{
			name: "aes.qc2", args: append(slices.Clone(secret), "-f", "qcow2", "-o", "encrypt.format=aes,encrypt.key-secret=sec0"),
			want: diskchain.Header{Format: diskchain.FormatQcow2, Version: 3, Encryption: diskchain.EncryptionAES},
		},
		{
			name: "luks.qc2", args: append(slices.Clone(secret), "-f", "qcow2", "-o", "encrypt.format=luks,encrypt.key-secret=sec0"),
			want: diskchain.Header{Format: diskchain.FormatQcow2, Version: 3, Encryption: diskchain.EncryptionLUKS},
		},
		{
			name: "zstd.qc2", args: []string{"-f", "qcow2", "-o", "compression_type=zstd"},
			want: diskchain.Header{
				Format: diskchain.FormatQcow2, Version: 3, Incompatible: diskchain.FeatureCompression, Zstd: true,
			},
		},
		{
			name: "l2.qc2", args: []string{"-f", "qcow2", "-o", "extended_l2=on"},
			want: diskchain.Header{Format: diskchain.FormatQcow2, Version: 3, Incompatible: diskchain.FeatureExtendedL2},
		},
		{
			name: "data.qc2", args: []string{"-f", "qcow2", "-o", "data_file=data.raw"},
			want: diskchain.Header{
				Format: diskchain.FormatQcow2, Version: 3, Incompatible: diskchain.FeatureDataFile, DataFile: true,
			},
		},
	}

	for _, tc := range tests {
		qemuImg(t, dir, append(append([]string{"create", "-q"}, tc.args...), tc.name, "1M")...)

		f, err := os.Open(filepath.Join(dir, tc.name))
		if err != nil {
			t.Fatalf("opening %s: %v", tc.name, err)
		}

		got, err := diskchain.ParseHeader(f)
		_ = f.Close()

		if err != nil {
			t.Errorf("ParseHeader(%s) error = %v", tc.name, err)
		}

		if diff := cmp.Diff(tc.want, got); diff != "" {
			t.Errorf("ParseHeader(%s) mismatch (-want +got):\n%s", tc.name, diff)
		}
	}
}

// Walk follows a qcow2 overlay of a qcow version 1 image, as minimega creates one with the format qemu-img info gives,
// with a note about the old image.
func TestWalkFollowsQcowV1AsQemuImg(t *testing.T) {
	testenv.RequireCommands(t, "qemu-img")
	t.Parallel()

	dir := t.TempDir()
	qemuImg(t, dir, "create", "-q", "-f", "raw", "base.img", "1M")
	qemuImg(t, dir, "create", "-q", "-f", "qcow", "-b", "base.img", "-F", "raw", "kali.qcow")
	qemuImg(t, dir, "create", "-q", "-f", "qcow2", "-b", "kali.qcow", "-F", "qcow", "kali.qc2")

	path := filepath.Join(dir, "kali.qc2")
	got := diskchain.Walk(t.Context(), path)

	want := diskchain.Result{
		Members: qemuChain(t, dir, path), Notes: []diskchain.Note{{Path: dir + "/kali.qcow", Kind: diskchain.QcowV1}},
	}

	if diff := cmp.Diff(want, got.MapPaths(filepath.Clean), cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("Walk mismatch (-want +got):\n%s", diff)
	}
}

// Walk refuses the images qemu-img itself cannot open: a qcow2 version qemu does not know, and an incompatible
// feature bit qemu does not know.
func TestWalkRefusesWhatQemuImgRefuses(t *testing.T) {
	testenv.RequireCommands(t, "qemu-img")
	t.Parallel()

	tests := []struct {
		name   string
		offset int64
		value  byte
		want   diskchain.Kind
		detail string
	}{
		{name: "v4.qc2", offset: 7, value: 4, want: diskchain.BadVersion, detail: "4"},
		{name: "unknown.qc2", offset: 79, value: 0x20, want: diskchain.UnknownFeatures, detail: "0x20"},
	}

	dir := t.TempDir()

	for _, tc := range tests {
		qemuImg(t, dir, "create", "-q", "-f", "qcow2", tc.name, "1M")

		path := filepath.Join(dir, tc.name)
		setByte(t, path, tc.offset, tc.value)

		info := exec.CommandContext(t.Context(), "qemu-img", "info", path)
		if out, err := info.CombinedOutput(); err == nil {
			t.Errorf("qemu-img info opened %s:\n%s", tc.name, out)
		}

		want := diskchain.Result{Members: []string{path}, Problem: tc.want, At: path, Detail: tc.detail}
		if diff := cmp.Diff(want, diskchain.Walk(t.Context(), path)); diff != "" {
			t.Errorf("Walk(%s) mismatch (-want +got):\n%s", tc.name, diff)
		}
	}
}

// Walk reads a file that starts with the qcow2 magic and version 0 as a raw image, as qemu-img probes it.
func TestWalkReadsVersion0AsRaw(t *testing.T) {
	testenv.RequireCommands(t, "qemu-img")
	t.Parallel()

	path := filepath.Join(t.TempDir(), "kali.img")
	if err := os.WriteFile(path, append([]byte("QFI\xfb"), make([]byte, 1<<20-4)...), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}

	var info struct {
		Format string `json:"format"`
	}

	if err := json.Unmarshal(qemuImg(t, "", "info", "--output=json", path), &info); err != nil {
		t.Fatalf("decoding qemu-img info: %v", err)
	}

	if info.Format != "raw" {
		t.Errorf("qemu-img info format = %q, want raw", info.Format)
	}

	if diff := cmp.Diff(diskchain.Result{Members: []string{path}}, diskchain.Walk(t.Context(), path)); diff != "" {
		t.Errorf("Walk mismatch (-want +got):\n%s", diff)
	}
}

// setByte writes value at offset in the file at path.
func setByte(t *testing.T, path string, offset int64, value byte) {
	t.Helper()

	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("opening %s: %v", path, err)
	}

	defer func() { _ = f.Close() }()

	if _, err := f.WriteAt([]byte{value}, offset); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}
