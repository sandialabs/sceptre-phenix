package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/google/go-cmp/cmp"

	bapi "phenix/api/builder"
	"phenix/types/builder"
)

// doctorTemplateFile is a template file of one collection that phenix ui reads.
const doctorTemplateFile = "$schema: " + builder.TemplateFileSchemaURI + `
name: Plant
templates:
  - name: PLC
    device:
      spec:
        type: VirtualMachine
        general:
          hostname: plc
`

// The check reads the directory of base-dir.builder-templates, or the one
// under base-dir.phenix, and only warns: a missing default directory passes.
func TestBuilderTemplatesCheck(t *testing.T) {
	t.Parallel()

	dir := newDoctorTestDir(t)
	phenixBase := filepath.Join(dir, "phenix")
	defaultDir := filepath.Join(phenixBase, "builder", "templates")

	good := filepath.Join(dir, "good")
	bad := filepath.Join(dir, "bad")
	empty := filepath.Join(dir, "empty")

	for _, d := range []string{good, bad, empty} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatalf("os.MkdirAll(%s) error = %v", d, err)
		}
	}

	writeTestFile(t, good, "plant.yaml", doctorTemplateFile)
	writeTestFile(t, good, "notes.txt", "not a template file")
	writeTestFile(t, bad, "plant.yaml", doctorTemplateFile)

	for _, name := range []string{"a.yaml", "b.json", "c.yml", "d.yaml", "e.yaml", "f.yaml"} {
		writeTestFile(t, bad, name, "name: [secret-value")
	}

	file := writeTestFile(t, dir, "templates.yaml", doctorTemplateFile)
	allow := func(string, uint32) error { return nil }
	denied := errors.New("stat " + good + ": permission denied")

	tests := map[string]struct {
		value     string
		stat      func(string) (os.FileInfo, error)
		access    func(string, uint32) error
		templates func(string) (*bapi.TemplateDirectory, error)
		want      doctorCheck
	}{
		"default directory missing": {
			want: row("", doctorPass, defaultDir+" does not exist, so the Builder has no server templates"),
		},
		"default directory": {
			stat: func(path string) (os.FileInfo, error) {
				if path == defaultDir {
					return os.Stat(good)
				}

				return os.Stat(path)
			},
			access:    allow,
			templates: func(string) (*bapi.TemplateDirectory, error) { return bapi.ReadTemplateDirectory(good) },
			want:      row("", doctorPass, defaultDir+", with 1 template file"),
		},
		"set directory missing": {
			value: filepath.Join(dir, "missing"),
			want: row("", doctorWarn,
				filepath.Join(dir, "missing")+" does not exist, so phenix ui reads no template files"),
		},
		"set directory": {
			value: good,
			want:  row("", doctorPass, good+", with 1 template file"),
		},
		"empty directory": {
			value: empty,
			want:  row("", doctorPass, empty+", with 0 template files"),
		},
		"not a directory": {
			value: file,
			want:  row("", doctorWarn, file+" is not a directory, so phenix ui reads no template files"),
		},
		"stat error": {
			value: good,
			stat:  func(string) (os.FileInfo, error) { return nil, denied },
			want:  row("", doctorWarn, denied.Error()+", so phenix ui reads no template files"),
		},
		"not readable": {
			value:  good,
			access: func(string, uint32) error { return syscall.EACCES },
			want: row("", doctorWarn,
				good+" is not readable and searchable by this user, so phenix ui reads no template files"),
		},
		"relative": {
			value:  "templates",
			stat:   func(string) (os.FileInfo, error) { return os.Stat(good) },
			access: allow,
			want:   row("", doctorWarn, "templates is relative, so it depends on the working directory of phenix ui"),
		},
		"read error": {
			value:     good,
			templates: func(string) (*bapi.TemplateDirectory, error) { return nil, errors.New("listing " + good) },
			want:      row("", doctorWarn, "listing "+good+", so phenix ui reads no template files"),
		},
		"files phenix ui skips": {
			value: bad,
			want: row("", doctorWarn,
				"phenix ui skips 6 template files in "+bad+": a.yaml, b.json, c.yml, d.yaml, e.yaml, and 1 more"),
		},
		"one file phenix ui skips": {
			value: good,
			templates: func(string) (*bapi.TemplateDirectory, error) {
				return &bapi.TemplateDirectory{
					Collections: nil, Icons: nil, Missing: false,
					Problems: []bapi.TemplateFileProblem{{File: "x.yaml", Reason: "is not a valid template file"}},
				}, nil
			},
			want: row("", doctorWarn, "phenix ui skips 1 template file in "+good+": x.yaml"),
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			in := fakeDoctorInput(dir, map[string]string{builderTemplatesKey: tc.value})
			if tc.stat != nil {
				in.env.stat = tc.stat
			}

			if tc.access != nil {
				in.env.access = tc.access
			}

			if tc.templates != nil {
				in.templates = tc.templates
			}

			got := builderTemplatesCheck(in)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("builderTemplatesCheck() mismatch (-want +got):\n%s", diff)
			}

			if strings.Contains(got.Detail, "secret-value") {
				t.Errorf("builderTemplatesCheck() detail %q holds the content of a file", got.Detail)
			}
		})
	}
}

// The hint of a row that names skipped files says to fix them; the hint of
// any other row says to fix the directory.
func TestBuilderTemplatesHint(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		detail string
		want   string // a prefix
	}{
		"skipped files": {detail: "phenix ui skips 1 template file in /t: x.yaml", want: "fix or remove each file"},
		"missing":       {detail: "/t does not exist, so phenix ui reads no template files", want: "make " + builderTemplatesKey},
		"relative":      {detail: "t is relative, so it depends on the working directory of phenix ui", want: "make "},
	}

	for name, tc := range tests {
		if got := builderTemplatesHint(row("", doctorWarn, tc.detail)); !strings.HasPrefix(got, tc.want) {
			t.Errorf("%s: builderTemplatesHint() = %q, want prefix %q", name, got, tc.want)
		}
	}

	for _, d := range doctorChecks() {
		if d.id == builderTemplatesKey && (d.group != doctorGroupDirs || d.name != builderTemplatesKey) {
			t.Errorf("check %s group %s, name %s; want group %s and its id as name", d.id, d.group, d.name, doctorGroupDirs)
		}
	}
}
