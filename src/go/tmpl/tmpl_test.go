package tmpl_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/tmpl"
)

const hostnameScript = "#!/bin/bash\n\nsed -i -e 's/phenix/host-01/g' /etc/hosts\nhostnamectl set-hostname host-01"

func TestGenerateFromTemplate(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	if err := tmpl.GenerateFromTemplate("linux_hostname.tmpl", "host-01", &out); err != nil {
		t.Fatalf("GenerateFromTemplate(linux_hostname.tmpl) = %v, want nil", err)
	}

	if got := out.String(); got != hostnameScript {
		t.Errorf("GenerateFromTemplate(linux_hostname.tmpl, host-01) = %q, want %q", got, hostnameScript)
	}
}

func TestGenerateFromTemplateFails(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		template string
		data     any
	}{
		{name: "missing template", template: "missing.tmpl", data: nil},
		{name: "data the template cannot use", template: "linux_interfaces.tmpl", data: 42},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if err := tmpl.GenerateFromTemplate(tt.template, tt.data, new(bytes.Buffer)); err == nil {
				t.Errorf("GenerateFromTemplate(%q, %v) = nil, want an error", tt.template, tt.data)
			}
		})
	}
}

// CreateFileFromTemplate makes the missing folders and replaces a file that is there.
func TestCreateFileFromTemplate(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "a", "b", "hostname.sh")

	for _, host := range []string{"long-host-name-01", "host-01"} {
		if err := tmpl.CreateFileFromTemplate("linux_hostname.tmpl", host, path); err != nil {
			t.Fatalf("CreateFileFromTemplate(linux_hostname.tmpl, %s, %q) = %v, want nil", host, path, err)
		}
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the created file: %v", err)
	}

	if string(got) != hostnameScript {
		t.Errorf("CreateFileFromTemplate() wrote %q, want %q", got, hostnameScript)
	}
}

func TestCreateFileFromTemplateFails(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatalf("writing a file: %v", err)
	}

	tests := []struct {
		name     string
		template string
		filename string
	}{
		{name: "a folder in the path is a file", template: "linux_hostname.tmpl", filename: filepath.Join(file, "out.sh")},
		{name: "the path is a folder", template: "linux_hostname.tmpl", filename: dir},
		{name: "missing template", template: "missing.tmpl", filename: filepath.Join(dir, "out.sh")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if err := tmpl.CreateFileFromTemplate(tt.template, "host-01", tt.filename); err == nil {
				t.Errorf("CreateFileFromTemplate(%q, host-01, %q) = nil, want an error", tt.template, tt.filename)
			}
		})
	}
}

// CreateFileFromTemplate keeps the old content of the file when the template fails, and leaves no temporary file.
func TestCreateFileFromTemplateKeepsOldFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		template string
		data     any
	}{
		{name: "missing template", template: "missing.tmpl", data: "host-01"},
		{name: "data the template cannot use", template: "linux_interfaces.tmpl", data: 42},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			path := filepath.Join(dir, "interfaces")

			if err := os.WriteFile(path, []byte("old content"), 0o600); err != nil {
				t.Fatalf("writing the old file: %v", err)
			}

			if err := tmpl.CreateFileFromTemplate(tt.template, tt.data, path); err == nil {
				t.Fatalf("CreateFileFromTemplate(%q, %v, %q) = nil, want an error", tt.template, tt.data, path)
			}

			if got, err := os.ReadFile(path); err != nil || string(got) != "old content" {
				t.Errorf("file after a failed CreateFileFromTemplate = %q, %v, want %q", got, err, "old content")
			}

			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatalf("os.ReadDir(%q) error = %v", dir, err)
			}

			names := make([]string, 0, len(entries))
			for _, e := range entries {
				names = append(names, e.Name())
			}

			if diff := cmp.Diff([]string{"interfaces"}, names); diff != "" {
				t.Errorf("folder after a failed CreateFileFromTemplate mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// CreateFileFromTemplate gives a new file mode 0600.
func TestCreateFileFromTemplateMode(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "hostname.sh")

	if err := tmpl.CreateFileFromTemplate("linux_hostname.tmpl", "host-01", path); err != nil {
		t.Fatalf("CreateFileFromTemplate(linux_hostname.tmpl, host-01, %q) = %v, want nil", path, err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("os.Stat(%q) error = %v", path, err)
	}

	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("CreateFileFromTemplate() file mode = %v, want %v", got, os.FileMode(0o600))
	}
}

// CreateFileFromTemplate writes a file with a name as long as the file system allows.
func TestCreateFileFromTemplateLongName(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), strings.Repeat("a", 250))

	if err := tmpl.CreateFileFromTemplate("linux_hostname.tmpl", "host-01", path); err != nil {
		t.Errorf("CreateFileFromTemplate(linux_hostname.tmpl, host-01, <250 characters>) = %v, want nil", err)
	}
}

// CreateFileFromTemplate fails in a folder that it cannot write, and keeps the file there.
func TestCreateFileFromTemplateReadOnlyFolder(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		t.Skip("root can write a read-only folder")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "hostname.sh")

	if err := os.WriteFile(path, []byte("old content"), 0o600); err != nil {
		t.Fatalf("writing the old file: %v", err)
	}

	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("os.Chmod(%q) error = %v", dir, err)
	}

	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if err := tmpl.CreateFileFromTemplate("linux_hostname.tmpl", "host-01", path); err == nil {
		t.Errorf("CreateFileFromTemplate(linux_hostname.tmpl, host-01, %q) = nil, want an error", path)
	}

	if got, err := os.ReadFile(path); err != nil || string(got) != "old content" {
		t.Errorf("file after a failed CreateFileFromTemplate = %q, %v, want %q", got, err, "old content")
	}
}
