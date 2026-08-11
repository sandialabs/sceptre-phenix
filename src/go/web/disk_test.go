package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"

	v1 "phenix/types/version/v1"
	"phenix/web/middleware"
	"phenix/web/rbac"
)

func TestNormalizeBuildOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "default", want: "/phenix/vmdb"},
		{name: "absolute", input: "/tmp/../images", want: "/images"},
		{name: "relative", input: "images", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := normalizeBuildOutput(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("normalizeBuildOutput() error = %v, wantErr %v", err, tt.wantErr)
			}

			if got != tt.want {
				t.Errorf("normalizeBuildOutput() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNormalizeInjectMiniExeRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		request  injectMiniExeRequest
		wantExe  string
		wantDisk string
		wantErr  bool
	}{
		{
			name:     "defaults executable and disk directory",
			request:  injectMiniExeRequest{Disk: "mydisk.qc2"},
			wantExe:  "/phenix/miniccc",
			wantDisk: "/phenix/vmdb/mydisk.qc2",
		},
		{
			name:     "keeps absolute paths",
			request:  injectMiniExeRequest{Exe: "/tmp/minirouter", Disk: "/tmp/disk.qc2"},
			wantExe:  "/tmp/minirouter",
			wantDisk: "/tmp/disk.qc2",
		},
		{name: "requires disk", request: injectMiniExeRequest{}, wantErr: true},
		{name: "requires absolute executable", request: injectMiniExeRequest{Exe: "miniccc", Disk: "disk.qc2"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := normalizeInjectMiniExeRequest(tt.request)
			if (err != nil) != tt.wantErr {
				t.Fatalf("normalizeInjectMiniExeRequest() error = %v, wantErr %v", err, tt.wantErr)
			}

			if tt.wantErr {
				return
			}

			if got.Exe != tt.wantExe {
				t.Errorf("executable = %q, want %q", got.Exe, tt.wantExe)
			}

			if got.Disk != tt.wantDisk {
				t.Errorf("disk = %q, want %q", got.Disk, tt.wantDisk)
			}
		})
	}
}

func TestGetImageBuild(t *testing.T) {
	const name = "test-image"

	imageBuilds.mu.Lock()
	imageBuilds.status[name] = buildImageStatus{Status: buildStateComplete, Error: ""}
	imageBuilds.mu.Unlock()

	t.Cleanup(func() {
		imageBuilds.mu.Lock()
		delete(imageBuilds.status, name)
		imageBuilds.mu.Unlock()
	})

	role := rbac.Role{Spec: &v1.RoleSpec{Policies: []*v1.PolicySpec{{
		Resources:     []string{"images"},
		ResourceNames: []string{name},
		Verbs:         []string{"get"},
	}}}}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/images/"+name+"/build", nil)
	request = mux.SetURLVars(request, map[string]string{"name": name})
	request = request.WithContext(context.WithValue(request.Context(), middleware.ContextKeyRole, role))
	response := httptest.NewRecorder()

	GetImageBuild(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}

	var got buildImageStatus
	if err := json.NewDecoder(response.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if got.Status != buildStateComplete {
		t.Errorf("build status = %q, want %s", got.Status, buildStateComplete)
	}
}
