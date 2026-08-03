package soh

import (
	"reflect"
	"testing"

	"github.com/mitchellh/mapstructure"
)

func TestHostStateAllStatesIncludesFiles(t *testing.T) {
	t.Parallel()

	networking := State{Success: "network configured"}
	file := State{Success: "file found"}
	fileAbsent := State{Success: "file not found"}
	process := State{Success: "process running"}

	state := HostState{
		Networking:  []State{networking},
		Files:       []State{file},
		FilesAbsent: []State{fileAbsent},
		Processes:   []State{process},
	}

	want := []State{networking, file, fileAbsent, process}
	if got := state.AllStates(); !reflect.DeepEqual(got, want) {
		t.Fatalf("AllStates() = %#v, want %#v", got, want)
	}
}

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

	if !reflect.DeepEqual(metadata.HostFiles, input["hostFiles"]) {
		t.Fatalf("HostFiles = %#v, want %#v", metadata.HostFiles, input["hostFiles"])
	}
}

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

	if !reflect.DeepEqual(metadata.HostFilesAbsent, input["hostFilesAbsent"]) {
		t.Fatalf(
			"HostFilesAbsent = %#v, want %#v",
			metadata.HostFilesAbsent,
			input["hostFilesAbsent"],
		)
	}
}

func TestFileCheckCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		osType string
		path   string
		want   string
	}{
		{
			name:   "linux",
			osType: "linux",
			path:   "/opt/user's file",
			want:   `stat -c present -- '/opt/user'"'"'s file'`,
		},
		{
			name:   "windows",
			osType: "windows",
			path:   `C:\Users\O'Brien\data.txt`,
			want:   `powershell -NoProfile -Command "if (Test-Path -LiteralPath 'C:\Users\O''Brien\data.txt' -PathType Leaf) { 'present' }"`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := fileCheckCommand(test.osType, test.path); got != test.want {
				t.Fatalf("fileCheckCommand() = %q, want %q", got, test.want)
			}
		})
	}
}
