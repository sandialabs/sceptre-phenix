package cmd

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/spf13/cobra"

	"phenix/api/experiment"
	"phenix/util/common"
	"phenix/util/file"
)

// expFilesCluster is a cluster whose nodes list files and hold no copy of a file the head node lacks.
type expFilesCluster struct {
	file.ClusterFiles

	files   file.Files
	deleted []string
}

func (c *expFilesCluster) GetExperimentFiles(string, string) (file.Files, error) {
	return c.files, nil
}

func (c *expFilesCluster) CopyFile(src, _ string, _ file.CopyStatus) error {
	return fmt.Errorf("no mesh node has %s", src)
}

//nolint:unparam // file.existingFilesDeleter fixes the signature
func (c *expFilesCluster) DeleteExistingFiles(names []string) error {
	c.deleted = append(c.deleted, names...)

	return nil
}

// useExpFiles stores the stopped experiment exp1, writes files (path to contents) to its files directory on the head
// node, and has the cluster list them and listed, a file only a mesh node has.
func useExpFiles(t *testing.T, files map[string]string, listed ...string) *expFilesCluster {
	t.Helper()

	expCmdUseStore(t, vmCmdExperiment(t, "exp1", false))

	cluster := new(expFilesCluster)

	for p, contents := range files {
		local := filepath.Join(common.PhenixBase, "images", "exp1", "files", p)

		if err := os.MkdirAll(filepath.Dir(local), 0o750); err != nil {
			t.Fatalf("making the folder of %s: %v", p, err)
		}

		if err := os.WriteFile(local, []byte(contents), 0o600); err != nil {
			t.Fatalf("writing %s: %v", p, err)
		}

		listed = append(listed, p)
	}

	slices.Sort(listed)

	for _, p := range listed {
		cluster.files = append(cluster.files, file.File{Name: filepath.Base(p), Path: p, Size: int64(len(files[p]))})
	}

	original := file.DefaultClusterFiles
	file.DefaultClusterFiles = cluster //nolint:reassign // test double

	t.Cleanup(func() { file.DefaultClusterFiles = original }) //nolint:reassign // restore

	experiment.InvalidateFiles("exp1")

	return cluster
}

// runExpFilesCmd runs cmd with args and returns its output and error.
func runExpFilesCmd(t *testing.T, cmd *cobra.Command, args ...string) (string, error) {
	t.Helper()

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)

	err := cmd.ExecuteContext(t.Context())

	return out.String(), err
}

// rootCmd.Find sorts the command lists, so this test does not run in parallel.
func TestExperimentSubcommandsOnRoot(t *testing.T) {
	for _, path := range [][]string{
		{"experiment", "files", "archive"},
		{"experiment", "soh"},
		{"experiment", "netflow", "download"},
		{"disk", "rebase"},
		{"host", "list"},
		{"logs", "download"},
	} {
		found, _, err := rootCmd.Find(path)
		if err != nil || found.Name() != path[len(path)-1] {
			t.Errorf("rootCmd.Find(%q) = %v, %v, want the %s command", path, found.Name(), err, path[len(path)-1])
		}
	}
}

func TestExperimentFilesGetAndChecksum(t *testing.T) {
	useExpFiles(t, map[string]string{"captures/web.pcap": "packets"})

	out, err := runExpFilesCmd(t, newExperimentFilesCmd(), "get", "exp1", "captures/web.pcap")
	if err != nil || out != "packets" {
		t.Errorf("files get exp1 captures/web.pcap = %q, %v, want %q", out, err, "packets")
	}

	local := filepath.Join(t.TempDir(), "web.pcap")

	if _, err := runExpFilesCmd(t, newExperimentFilesCmd(), "get", "exp1", "captures/web.pcap", "-O", local); err != nil {
		t.Fatalf("files get -O %s error = %v", local, err)
	}

	if b, _ := os.ReadFile(local); string(b) != "packets" {
		t.Errorf("files get -O wrote %q, want %q", b, "packets")
	}

	for _, path := range []string{"../exp2/files/a", "/captures/web.pcap", "captures//web.pcap"} {
		_, err := runExpFilesCmd(t, newExperimentFilesCmd(), "get", "exp1", path)
		if !errors.Is(err, experiment.ErrInvalidFilePath) {
			t.Errorf("files get exp1 %s error = %v, want %v", path, err, experiment.ErrInvalidFilePath)
		}
	}

	out, err = runExpFilesCmd(t, newExperimentFilesCmd(), "checksum", "exp1", "captures/web.pcap")
	if err != nil {
		t.Fatalf("files checksum error = %v", err)
	}

	sum := sha256.Sum256([]byte("packets"))
	if want := hex.EncodeToString(sum[:]) + "  captures/web.pcap\n"; out != want {
		t.Errorf("files checksum = %q, want %q", out, want)
	}
}

// files get copies all of a file, and refuses to write a binary file, but not
// a text file, to a terminal.
func TestExperimentFilesGetBinary(t *testing.T) {
	pcap := "\xd4\xc3\xb2\xa1" + strings.Repeat("\x00\x01", sniffLen)

	useExpFiles(t, map[string]string{"web.pcap": pcap, "notes.txt": "notes"})

	if out, err := runExpFilesCmd(t, newExperimentFilesCmd(), "get", "exp1", "web.pcap"); err != nil || out != pcap {
		t.Errorf("files get exp1 web.pcap = %d bytes, %v, want the %d bytes of the file", len(out), err, len(pcap))
	}

	for _, tt := range []struct {
		path    string
		refused bool
	}{
		{path: "web.pcap", refused: true},
		{path: "notes.txt", refused: false},
	} {
		cmd := newExperimentFilesCmd()
		cmd.SetOut(openTestTerminal(t))
		cmd.SetErr(io.Discard)
		cmd.SetArgs([]string{"get", "exp1", tt.path})

		err := cmd.ExecuteContext(t.Context())
		if refused := err != nil && strings.Contains(err.Error(), "to a terminal"); refused != tt.refused || (err != nil && !refused) {
			t.Errorf("files get exp1 %s to a terminal error = %v, want refused %t", tt.path, err, tt.refused)
		}
	}
}

func TestExperimentFilesList(t *testing.T) {
	useExpFiles(t, map[string]string{"b.log": "b", "a.log": "a"})

	out, err := runExpFilesCmd(t, newExperimentFilesCmd(), "list", "exp1")
	if err != nil {
		t.Fatalf("files list exp1 error = %v", err)
	}

	if a, b := strings.Index(out, "a.log"), strings.Index(out, "b.log"); a < 0 || b < a {
		t.Errorf("files list exp1 = %q, want a.log, then b.log", out)
	}

	if _, err := runExpFilesCmd(t, newExperimentFilesCmd(), "list", "exp1", "--filter", "(a.log"); err == nil {
		t.Errorf("files list --filter with an open parenthesis error = nil, want an error")
	}

	if _, err := runExpFilesCmd(t, newExperimentFilesCmd(), "list", "nope"); err == nil {
		t.Errorf("files list of an unknown experiment error = nil, want an error")
	}
}

func TestExperimentFilesDelete(t *testing.T) {
	cluster := useExpFiles(t, map[string]string{"a.log": "a"})

	out, err := runExpFilesCmd(t, newExperimentFilesCmd(), "delete", "exp1", "a.log", "missing.log")
	if err == nil || !strings.Contains(err.Error(), "missing.log") {
		t.Errorf("files delete exp1 a.log missing.log error = %v, want one that names missing.log", err)
	}

	if !strings.Contains(out, "Deleted a.log") {
		t.Errorf("files delete exp1 a.log missing.log = %q, want it to report a.log deleted", out)
	}

	if diff := cmp.Diff([]string{"exp1/files/a.log"}, cluster.deleted); diff != "" {
		t.Errorf("files deleted mismatch (-want +got):\n%s", diff)
	}
}

func TestExperimentFilesArchive(t *testing.T) {
	useExpFiles(t, map[string]string{"a.log": "aaa", "sub/b.log": "bb"}, "mesh-only.log")

	out := filepath.Join(t.TempDir(), "files.zip")

	if _, err := runExpFilesCmd(t, newExperimentFilesCmd(), "archive", "exp1", "a.log", "sub/b.log", "--output-file", out); err != nil {
		t.Fatalf("files archive error = %v", err)
	}

	if diff := cmp.Diff(map[string]string{"a.log": "aaa", "sub/b.log": "bb"}, readZip(t, out)); diff != "" {
		t.Errorf("archive mismatch (-want +got):\n%s", diff)
	}

	_, err := runExpFilesCmd(t, newExperimentFilesCmd(), "archive", "exp1", "a.log", "mesh-only.log", "--output-file", out)
	if err == nil || !strings.Contains(err.Error(), "mesh-only.log") {
		t.Errorf("files archive with a file no node can send error = %v, want one that names mesh-only.log", err)
	}

	got := readZip(t, out)
	if got["a.log"] != "aaa" || !strings.Contains(got["MISSING.txt"], "mesh-only.log") {
		t.Errorf("incomplete archive = %q, want a.log and a MISSING.txt that names mesh-only.log", got)
	}

	if _, err := runExpFilesCmd(t, newExperimentFilesCmd(), "archive", "exp1", "a.log"); err == nil {
		t.Errorf("files archive without --output error = nil, want an error")
	}

	if _, err := runExpFilesCmd(t, newExperimentFilesCmd(), "archive", "exp1", "../x", "--output-file", out); !errors.Is(err,
		experiment.ErrInvalidFilePath) {
		t.Errorf("files archive ../x error = %v, want %v", err, experiment.ErrInvalidFilePath)
	}
}

// readZip returns each entry of the zip archive at path with its contents.
func readZip(t *testing.T, path string) map[string]string {
	t.Helper()

	r, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("opening zip %s: %v", path, err)
	}

	defer func() { _ = r.Close() }()

	entries := map[string]string{}

	for _, f := range r.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("opening zip entry %s: %v", f.Name, err)
		}

		b, err := io.ReadAll(rc)
		_ = rc.Close()

		if err != nil {
			t.Fatalf("reading zip entry %s: %v", f.Name, err)
		}

		entries[f.Name] = string(b)
	}

	return entries
}
