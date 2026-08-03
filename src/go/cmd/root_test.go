package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSudoRanPhenix(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("unable to determine test executable: %v", err)
	}

	exeName := filepath.Base(exe)

	tests := []struct {
		name        string
		sudoCommand string
		want        bool
	}{
		{
			name:        "empty SUDO_COMMAND",
			sudoCommand: "",
			want:        false,
		},
		{
			name:        "sudo ran phenix directly",
			sudoCommand: "/usr/local/bin/phenix config list",
			want:        true,
		},
		{
			name:        "sudo ran current test binary",
			sudoCommand: exe + " -test.run TestSudoRanPhenix",
			want:        true,
		},
		{
			name:        "sudo ran su (root shell escalation)",
			sudoCommand: "/bin/su",
			want:        false,
		},
		{
			name:        "sudo ran su dash (root shell escalation)",
			sudoCommand: "/bin/su -",
			want:        false,
		},
		{
			name:        "sudo ran bash",
			sudoCommand: "/bin/bash",
			want:        false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SUDO_COMMAND", tc.sudoCommand)

			if got := sudoRanPhenix(); got != tc.want {
				t.Errorf("sudoRanPhenix() with SUDO_COMMAND=%q (exe=%s) = %v, want %v", tc.sudoCommand, exeName, got, tc.want)
			}
		})
	}
}
