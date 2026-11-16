package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/util/common"
	"phenix/util/mm"
)

// savedScreenshotPath is the path that POST .../screenshots saves test-vm's screenshot at.
var savedScreenshotPath = regexp.MustCompile(`^screenshots/test-vm-\d{8}-\d{6}(-\d+)?\.png$`)

// saveScreenshot sends POST /experiments/test-experiment/vms/test-vm/screenshots as role, and returns the answer and
// the path that its body names.
func saveScreenshot(t *testing.T, role string) (handlerAnswer, string) {
	t.Helper()

	r := testRole("vms/screenshot", role, "test-experiment/test-vm")
	vars := map[string]string{"exp": "test-experiment", "name": "test-vm"}

	got, rec := serveHandler(SaveScreenshot, roleRequest(http.MethodPost, "/", "", vars, r))
	if got.Status != http.StatusCreated {
		return got, ""
	}

	if ct := rec.Header().Get("Content-Type"); ct != mimeJSON {
		t.Errorf("POST screenshots Content-Type = %q, want %q", ct, mimeJSON)
	}

	var body SavedScreenshot
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding POST screenshots body %s: %v", rec.Body, err)
	}

	return got, body.Path
}

// POST /experiments/{exp}/vms/{name}/screenshots saves a full-size PNG of the running VM in the experiment's files,
// never in place of a file, and refuses a role without vms/screenshot create or a VM that does not run.
//
//nolint:paralleltest // replaces the store, minimega and the phenix base
func TestSaveScreenshot(t *testing.T) {
	png := []byte("\x89PNG not really")
	running := mm.VMs{{Name: "test-vm", State: "RUNNING", Running: true}}

	errorCases := map[string]struct {
		role string
		mm   *testMM
		want int
	}{
		"a role that may only get screenshots": {role: "get", mm: &testMM{vms: running, screenshot: png}, want: http.StatusForbidden},
		"a VM that is not running": {
			role: "create", mm: &testMM{vms: mm.VMs{{Name: "test-vm", State: "QUIT"}}, screenshot: png},
			want: http.StatusConflict,
		},
		"a screenshot that minimega cannot take": {
			role: "create", mm: &testMM{vms: running, screenshotErr: errors.New("no screen")},
			want: http.StatusInternalServerError,
		},
		"an empty screenshot": {role: "create", mm: &testMM{vms: running}, want: http.StatusInternalServerError},
	}

	for name, tc := range errorCases {
		t.Run(name, func(t *testing.T) {
			useTestExperiment(t)
			useTestMM(t, tc.mm)
			useExperimentFilesDir(t, nil)

			events := captureAudit(t)

			if got, _ := saveScreenshot(t, tc.role); got.Status != tc.want {
				t.Fatalf("POST screenshots = %+v, want %d", got, tc.want)
			}

			if tc.want == http.StatusForbidden {
				wantDenied(t, events(), "vm-screenshot")
			} else {
				wantAudit(t, events(), "vm-screenshot", "failure", map[string]any{"phenix.vm.name": "test-vm"})
			}

			dir := filepath.Join(common.PhenixBase, "images", "test-experiment", "files", "screenshots")
			if entries, _ := os.ReadDir(dir); len(entries) != 0 {
				t.Errorf("POST screenshots refused, but saved %d files", len(entries))
			}
		})
	}

	t.Run("an experiment that is not running", func(t *testing.T) {
		useTestStore(t, testExperiment(t, "", map[string]any{
			"type": "VirtualMachine", "general": map[string]any{"hostname": "test-vm"},
		}))
		useTestMM(t, &testMM{vms: running, screenshot: png})

		if got, _ := saveScreenshot(t, "create"); got.Status != http.StatusConflict {
			t.Errorf("POST screenshots of a stopped experiment = %+v, want 409", got)
		}
	})

	t.Run("saves each screenshot in a new file", func(t *testing.T) {
		useTestExperiment(t)
		useTestMM(t, &testMM{vms: running, screenshot: png})
		useExperimentFilesDir(t, nil)

		events := captureAudit(t)
		saved := make([]string, 0, 2)

		for range 2 {
			got, path := saveScreenshot(t, "create")
			if got.Status != http.StatusCreated {
				t.Fatalf("POST screenshots = %+v, want 201", got)
			}

			if !savedScreenshotPath.MatchString(path) {
				t.Errorf("POST screenshots saved %q, want a path matching %s", path, savedScreenshotPath)
			}

			wantAudit(t, events(), "vm-screenshot", "success", map[string]any{"phenix.file.target_path": path})

			saved = append(saved, path)
		}

		if saved[0] == saved[1] {
			t.Fatalf("two POST screenshots both saved %q", saved[0])
		}

		for _, path := range saved {
			data, err := os.ReadFile(filepath.Join(common.PhenixBase, "images", "test-experiment", "files", path))
			if err != nil {
				t.Fatalf("reading saved screenshot: %v", err)
			}

			if diff := cmp.Diff(png, data); diff != "" {
				t.Errorf("saved screenshot %s mismatch (-want +got):\n%s", path, diff)
			}
		}
	})
}
