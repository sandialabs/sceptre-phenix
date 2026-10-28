package web

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/store"
)

// An archive of several configs is a zip attachment however large, holds the configs the role may get,
// and answers an unreadable config with an error rather than a broken archive.
func TestDownloadConfigsArchive(t *testing.T) {
	// notes the zip writer cannot compress, so the archive passes its buffer
	notes := make([]byte, 8192)
	_, _ = rand.Read(notes)

	topology := func(name string) store.Config {
		return store.Config{
			Version: "phenix.sandia.gov/v1",
			Kind:    "Topology",
			Metadata: store.ConfigMetadata{
				Name: name, Annotations: store.Annotations{"notes": hex.EncodeToString(notes)},
			},
			Spec: map[string]any{"nodes": []any{}},
		}
	}

	useTestStore(t, topology("a"), topology("b"), topology("c"))

	server := serveAs(t, "alice", testRole("configs", "get", "Topology/a", "Topology/b", "Topology/none"))

	const (
		path = "/api/v1/configs/download"
		all  = `["Topology/a", "Topology/b", "Topology/c"]`
	)

	resp := do(t, http.MethodPost, server.URL+path, all)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading POST %s %s: %v", path, all, err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST %s %s = %d, want %d: %s", path, all, resp.StatusCode, http.StatusOK, body)
	}

	if got := resp.Header.Get("Content-Type"); got != "application/zip" {
		t.Errorf("POST %s %s Content-Type = %q, want application/zip", path, all, got)
	}

	if got := resp.Header.Get("Content-Disposition"); !strings.HasPrefix(got, "attachment") {
		t.Errorf("POST %s %s Content-Disposition = %q, want an attachment", path, all, got)
	}

	archive, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("POST %s %s is not a zip archive: %v", path, all, err)
	}

	names := make([]string, 0, len(archive.File))
	for _, f := range archive.File {
		names = append(names, f.Name)
	}

	if diff := cmp.Diff([]string{"Topology-a.yml", "Topology-b.yml"}, names); diff != "" {
		t.Errorf("POST %s %s archive files mismatch (-want +got):\n%s", path, all, diff)
	}

	const unknown = `["Topology/a", "Topology/none"]`

	resp = do(t, http.MethodPost, server.URL+path, unknown)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("POST %s %s = %d, want %d", path, unknown, resp.StatusCode, http.StatusBadRequest)
	}

	if got := resp.Header.Get("Content-Type"); got != mimeJSON {
		t.Errorf("POST %s %s Content-Type = %q, want a JSON error", path, unknown, got)
	}
}
