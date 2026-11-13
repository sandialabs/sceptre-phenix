package soh

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/mitchellh/mapstructure"
)

// AllStates lists the networking, file, absent file, service, process and
// container states, in that order.
func TestHostStateAllStatesIncludesFiles(t *testing.T) {
	t.Parallel()

	networking := State{Success: "network configured"}
	file := State{Success: "file found"}
	fileAbsent := State{Success: "file not found"}
	service := State{Success: "service running"}
	process := State{Success: "process running"}
	container := State{Success: "container running"}

	state := HostState{
		Networking:  []State{networking},
		Files:       []State{file},
		FilesAbsent: []State{fileAbsent},
		Services:    []State{service},
		Processes:   []State{process},
		Containers:  []State{container},
	}

	want := []State{networking, file, fileAbsent, service, process, container}
	if diff := cmp.Diff(want, state.AllStates()); diff != "" {
		t.Errorf("AllStates() mismatch (-want +got):\n%s", diff)
	}
}

// The hostFiles metadata decodes into HostFiles.
func TestSOHMetadataDecodesHostFiles(t *testing.T) {
	t.Parallel()

	input := map[string]any{
		"hostFiles": map[string][]string{
			"server": {"/etc/injected.conf", "/opt/data.txt"},
		},
	}

	var metadata sohMetadata
	if err := mapstructure.Decode(input, &metadata); err != nil {
		t.Fatalf("decoding metadata: %v", err)
	}

	if diff := cmp.Diff(input["hostFiles"], any(metadata.HostFiles)); diff != "" {
		t.Errorf("HostFiles mismatch (-want +got):\n%s", diff)
	}
}

// The hostFilesAbsent metadata decodes into HostFilesAbsent.
func TestSOHMetadataDecodesHostFilesAbsent(t *testing.T) {
	t.Parallel()

	input := map[string]any{
		"hostFilesAbsent": map[string][]string{
			"server": {"/tmp/should-not-exist.txt", "/opt/removed.txt"},
		},
	}

	var metadata sohMetadata
	if err := mapstructure.Decode(input, &metadata); err != nil {
		t.Fatalf("decoding metadata: %v", err)
	}

	if diff := cmp.Diff(input["hostFilesAbsent"], any(metadata.HostFilesAbsent)); diff != "" {
		t.Errorf("HostFilesAbsent mismatch (-want +got):\n%s", diff)
	}
}

// The dockerContainers metadata decodes into HostContainers.
func TestSOHMetadataDecodesDockerContainers(t *testing.T) {
	t.Parallel()

	input := map[string]any{
		"dockerContainers": map[string][]string{
			"server": {"nginx", "redis"},
		},
	}

	var metadata sohMetadata
	if err := mapstructure.Decode(input, &metadata); err != nil {
		t.Fatalf("decoding metadata: %v", err)
	}

	if diff := cmp.Diff(input["dockerContainers"], any(metadata.HostContainers)); diff != "" {
		t.Errorf("HostContainers mismatch (-want +got):\n%s", diff)
	}
}

// The hostServices metadata decodes into HostServices.
func TestSOHMetadataDecodesHostServices(t *testing.T) {
	t.Parallel()

	input := map[string]any{
		"hostServices": map[string][]string{
			"server": {"sshd", "nginx"},
		},
	}

	var metadata sohMetadata
	if err := mapstructure.Decode(input, &metadata); err != nil {
		t.Fatalf("decoding metadata: %v", err)
	}

	if diff := cmp.Diff(input["hostServices"], any(metadata.HostServices)); diff != "" {
		t.Errorf("HostServices mismatch (-want +got):\n%s", diff)
	}
}
