package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"pgregory.net/rapid"

	"phenix/util/common"
	"phenix/util/mm"
	"phenix/util/mm/mmtest"
	"phenix/util/polltest"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/weberror"
)

// getOptionsTestResponse serves GET /api/v1/options through
// [weberror.ErrorHandler], with role and a test user in the request context,
// and returns the recorded response.
func getOptionsTestResponse(t *testing.T, role rbac.Role) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/options", nil)

	ctx := context.WithValue(req.Context(), middleware.ContextKeyRole, role)
	ctx = context.WithValue(ctx, middleware.ContextKeyUser, "test-user")

	rec := httptest.NewRecorder()
	weberror.ErrorHandler(GetOptions).ServeHTTP(rec, req.WithContext(ctx))

	return rec
}

func TestGetOptionsReportsWorkflowSettings(t *testing.T) {
	original := common.InjectsBase
	t.Cleanup(func() { common.InjectsBase = original }) //nolint:reassign // restore test fixture

	common.InjectsBase = "/srv/phenix/injects" //nolint:reassign // monkey patching for test

	// Mirrors the global-admin role that the unix socket's NoAuth middleware
	// installs.
	rec := getOptionsTestResponse(t, workflowTestRole(true))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	if got := rec.Header().Get("Content-Type"); got != mimeJSON {
		t.Errorf("Content-Type = %q, want %q", got, mimeJSON)
	}

	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding body %q: %v", rec.Body.String(), err)
	}

	want := map[string]any{
		"bridge-mode":        string(common.BridgeMode),
		"deploy-mode":        string(common.DeployMode),
		"use-gre-mesh":       common.UseGREMesh,
		"base-dir.injects":   "/srv/phenix/injects",
		"workflow-dry-run":   true,
		"workflow-variables": true,
		"workflow-delete":    true,
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("GET /options body = %v, want %v", got, want)
	}
}

func TestGetOptionsForbidden(t *testing.T) {
	// A non-nil Spec is required: Role.Allowed dereferences Spec.Policies.
	rec := getOptionsTestResponse(t, workflowTestRole(false))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}

	body := rec.Body.String()
	if strings.Contains(body, "workflow-dry-run") || !strings.Contains(body, "listing options not allowed for test-user") {
		t.Errorf("body = %s, want only the forbidden error", body)
	}
}

// Each capture records 100 MiB by default; the option sets another limit, and
// the server refuses a negative one.
//
//nolint:paralleltest // sets the server options
func TestServeWithNetflowMaxSize(t *testing.T) {
	if got := newServerOptions().netflowMaxSize; got != 100<<20 {
		t.Errorf("default netflow max size = %d, want %d", got, 100<<20)
	}

	if got := newServerOptions(ServeWithNetflowMaxSize(0)).netflowMaxSize; got != 0 {
		t.Errorf("netflow max size with ServeWithNetflowMaxSize(0) = %d, want 0", got)
	}

	saved := o
	t.Cleanup(func() { o = saved })

	err := Start(t.Context(), ServeWithNetflowMaxSize(-1))
	if err == nil || !strings.Contains(err.Error(), "ui.netflow.max-size") {
		t.Errorf("Start(netflow max size -1) = %v, want an error naming ui.netflow.max-size", err)
	}
}

// The server refuses a negative capture size limit.
//
//nolint:paralleltest // sets the server options
func TestServeWithCaptureMaxSize(t *testing.T) {
	saved := o
	t.Cleanup(func() { o = saved })

	err := Start(t.Context(), ServeWithCaptureMaxSize(-1))
	if err == nil || !strings.Contains(err.Error(), "ui.captures.max-size") {
		t.Errorf("Start(capture max size -1) = %v, want an error naming ui.captures.max-size", err)
	}
}

// The features list has minimega-captures once minimega answers with a version that supports experiment captures,
// and ui.features with minimega-captures has no effect.
//
//nolint:paralleltest // sets the capture support of the process
func TestFeatureNamesMinimegaCaptures(t *testing.T) {
	tests := map[string]struct {
		features []string // ui.features
		version  string   // minimega's version answer, "" for none
		want     []string // with the features that phenix ui turns on by itself
	}{
		"minimega 3.4.0": {
			features: []string{FeatureVMMount}, version: mmtest.VersionWithCaptures,
			want: []string{FeatureVMMount, FeatureMinimegaCaptures},
		},
		"minimega 3.4.0 and minimega-captures in ui.features": {
			features: []string{FeatureVMMount, FeatureMinimegaCaptures}, version: mmtest.VersionWithCaptures,
			want: []string{FeatureVMMount, FeatureMinimegaCaptures},
		},
		"minimega 3.3.0 and minimega-captures in ui.features": {
			features: []string{FeatureVMMount, FeatureMinimegaCaptures}, version: mmtest.VersionWithoutCaptures,
			want: []string{FeatureVMMount},
		},
		"no answer and minimega-captures in ui.features": {
			features: []string{FeatureVMMount, FeatureMinimegaCaptures}, want: []string{FeatureVMMount},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			mm.ForgetCluster()
			t.Cleanup(mm.ForgetCluster)

			auto := newServerOptions().featureNames()

			if tc.version != "" {
				useMinimegaVersion(t, tc.version)
			}

			got := newServerOptions(ServeWithFeatures(tc.features)).featureNames()
			if diff := cmp.Diff(append(auto, tc.want...), got, cmpopts.SortSlices(func(a, b string) bool { return a < b })); diff != "" {
				t.Errorf("featureNames() with ui.features %q mismatch (-want +got):\n%s", tc.features, diff)
			}
		})
	}
}

// The base path gets a leading and a trailing slash only when it lacks one,
// and keeps every other slash.
func TestNormalizeBasePath(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"":         "/",
		"/":        "/",
		"phenix":   "/phenix/",
		"/phenix":  "/phenix/",
		"phenix/":  "/phenix/",
		"/phenix/": "/phenix/",
		"//a//":    "//a//",
		"a/b":      "/a/b/",
	}

	for in, want := range tests {
		if got := NormalizeBasePath(in); got != want {
			t.Errorf("NormalizeBasePath(%q) = %q, want %q", in, got, want)
		}

		if got := newServerOptions(ServeBasePath(in)).basePath; got != want {
			t.Errorf("newServerOptions(ServeBasePath(%q)).basePath = %q, want %q", in, got, want)
		}
	}
}

// A normalized base path starts and ends with a slash, holds the path it
// came from, and normalizing it again changes nothing.
func TestNormalizeBasePathProperty(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		p := rapid.StringMatching(`[a-z/]{0,12}`).Draw(t, "path")
		got := NormalizeBasePath(p)

		if !strings.HasPrefix(got, "/") || !strings.HasSuffix(got, "/") || !strings.Contains(got, p) {
			t.Fatalf("NormalizeBasePath(%q) = %q", p, got)
		}

		if again := NormalizeBasePath(got); again != got {
			t.Fatalf("NormalizeBasePath(%q) = %q, but of that %q", p, got, again)
		}
	})
}

// The server refuses webshark set by hand without a WebShark build, and
// accepts it with one; other features need no check.
func TestCheckFeatures(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		features    []string
		websharkDir string
		wantErr     bool
	}{
		"none":                  {},
		"vm-mount":              {features: []string{FeatureVMMount}},
		"webshark with a build": {features: []string{FeatureWebShark}, websharkDir: "/opt/phenix/webshark"},
		"webshark without one":  {features: []string{FeatureWebShark, FeatureVMMount}, wantErr: true},
		"others without theirs": {features: []string{FeatureTunnelerDownload, FeatureFileServer}},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			so := serverOptions{features: make(map[string]bool), websharkDir: tc.websharkDir}
			ServeWithFeatures(tc.features)(&so)

			err := so.checkFeatures()
			if (err != nil) != tc.wantErr || (err != nil && !strings.Contains(err.Error(), "ui.features")) {
				t.Errorf("checkFeatures(%v, %q) = %v, want an error naming ui.features: %t", tc.features, tc.websharkDir, err, tc.wantErr)
			}
		})
	}
}

// The server asks minimega for its version at startup, and only a version that supports experiment captures turns
// the capture support on. ui.features with minimega-captures has no effect.
//
//nolint:paralleltest // sets the capture support of the process
func TestStartCaptureSupport(t *testing.T) {
	tests := map[string]struct {
		features []string
		version  string
		want     mm.CaptureSupport
	}{
		"minimega with captures": {
			version: mmtest.VersionWithCaptures, want: mm.CaptureSupport{Available: true, Reason: "", Version: "3.4.0"},
		},
		"a prerelease of minimega 3.4.0": {
			version: "minimega 3.4.0-rc1 abc 2026-09-01",
			want:    mm.CaptureSupport{Available: true, Reason: "", Version: "3.4.0-rc1"},
		},
		"older minimega": {
			version: mmtest.VersionWithoutCaptures,
			want: mm.CaptureSupport{
				Available: false, Reason: "requires minimega 3.4.0 or later; the head node runs 3.3.0", Version: "3.3.0",
			},
		},
		"older minimega with minimega-captures in ui.features": {
			features: []string{FeatureMinimegaCaptures}, version: mmtest.VersionWithoutCaptures,
			want: mm.CaptureSupport{
				Available: false, Reason: "requires minimega 3.4.0 or later; the head node runs 3.3.0", Version: "3.3.0",
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			mm.ForgetCluster()
			t.Cleanup(mm.ForgetCluster)

			mmtest.Use(t, mmtest.AnswerVersion(tc.version))

			newServerOptions(ServeWithFeatures(tc.features)).startCaptureSupport(t.Context())

			polltest.Until(t, "minimega answers the version query", func() bool {
				return mm.CaptureSupportStatus().Version != ""
			})

			if diff := cmp.Diff(tc.want, mm.CaptureSupportStatus()); diff != "" {
				t.Errorf("capture support after startCaptureSupport mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
