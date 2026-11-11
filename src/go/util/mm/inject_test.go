package mm

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestInjectCommands(t *testing.T) {
	pairs := []string{`"/src/a":"/a"`, `"/src/b":"/dir with space/b"`}

	tests := []struct {
		name   string
		osType string
		want   []string
	}{
		{
			name:   "linux",
			osType: "linux",
			want:   []string{`disk inject img:1 files "/src/a":"/a" "/src/b":"/dir with space/b"`},
		},
		{
			name:   "no OS type",
			osType: "",
			want:   []string{`disk inject img:1 files "/src/a":"/a" "/src/b":"/dir with space/b"`},
		},
		{
			name:   "windows",
			osType: "Windows",
			want: []string{
				`disk inject img:1 options "-t lowntfs-3g -o ignore_case" files "/src/a":"/a"`,
				`disk inject img:1 options "-t lowntfs-3g -o ignore_case" files "/src/b":"/dir with space/b"`,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := InjectCommands("img:1", test.osType, pairs...)
			if diff := cmp.Diff(test.want, got); diff != "" {
				t.Errorf("InjectCommands() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestDeleteCommand(t *testing.T) {
	tests := []struct {
		osType string
		want   string
	}{
		{osType: "linux", want: `disk inject img:2 delete files "/a","/b"`},
		{
			osType: "windows",
			want:   `disk inject img:2 options "-t lowntfs-3g -o ignore_case" delete files "/a","/b"`,
		},
	}

	for _, test := range tests {
		t.Run(test.osType, func(t *testing.T) {
			if got := DeleteCommand("img:2", test.osType, `"/a","/b"`); got != test.want {
				t.Errorf("DeleteCommand() = %q, want %q", got, test.want)
			}
		})
	}
}
