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

	resp := do(t, http.MethodPost, server.URL+"/api/v1/configs/download", `["Topology/a", "Topology/b", "Topology/c"]`)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}

	if got := resp.Header.Get("Content-Type"); got != "application/zip" {
		t.Errorf("Content-Type = %q, want application/zip", got)
	}

	if got := resp.Header.Get("Content-Disposition"); !strings.HasPrefix(got, "attachment") {
		t.Errorf("Content-Disposition = %q, want an attachment", got)
	}

	archive, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}

	names := make([]string, 0, len(archive.File))
	for _, f := range archive.File {
		names = append(names, f.Name)
	}

	if strings.Join(names, ",") != "Topology-a.yml,Topology-b.yml" {
		t.Errorf("archive holds %v, want Topology-a.yml and Topology-b.yml", names)
	}

	resp = do(t, http.MethodPost, server.URL+"/api/v1/configs/download", `["Topology/a", "Topology/none"]`)
	if resp.StatusCode != http.StatusBadRequest || resp.Header.Get("Content-Type") != mimeJSON {
		t.Errorf("an unknown config: status %d, Content-Type %q, want 400 with a JSON error",
			resp.StatusCode, resp.Header.Get("Content-Type"))
	}
}
