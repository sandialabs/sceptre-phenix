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

	for i, f := range archive.File {
		r, err := f.Open()
		if err != nil {
			t.Fatalf("opening %s in POST %s %s: %v", f.Name, path, all, err)
		}

		content, err := io.ReadAll(r)
		_ = r.Close()

		if want := "name: " + []string{"a", "b"}[min(i, 1)]; err != nil || !strings.Contains(string(content), want) {
			t.Errorf("POST %s %s %s = %q, %v; want the config, holding %q", path, all, f.Name, content, err, want)
		}
	}

	for header, want := range map[string]string{"Accept-Ranges": "none", "Cache-Control": "no-store"} {
		if got := resp.Header.Get(header); got != want {
			t.Errorf("POST %s %s %s = %q, want %q, as the archive is built as it is sent", path, all, header, got, want)
		}
	}

	resp = do(t, http.MethodPost, server.URL+path, "Topology/a")

	got := errorMessage(t, resp.StatusCode, readBody(t, resp))
	if resp.StatusCode != http.StatusBadRequest || got != "unable to parse request" {
		t.Errorf("POST %s of a body not a JSON list = %d %q, want %d %q",
			path, resp.StatusCode, got, http.StatusBadRequest, "unable to parse request")
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
