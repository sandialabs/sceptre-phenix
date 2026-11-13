package envsubst_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"pgregory.net/rapid"

	"phenix/util/envsubst"
)

// setEnvFile writes an env file with the given text in a new folder and
// returns its path.
func setEnvFile(t *testing.T, name, text string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}

	return path
}

// setSubstitute substitutes one file with the set of o and returns its text.
func setSubstitute(o envsubst.Options, text string) (string, envsubst.Result, error) {
	set, err := envsubst.NewSet(o)
	if err != nil {
		return "", envsubst.Result{}, err
	}

	res, err := set.Substitute([]envsubst.File{{Path: "a.yml", Text: []byte(text)}})
	if err != nil {
		return "", envsubst.Result{}, err
	}

	return string(res.Files[0].Text), res, nil
}

func TestSetPrecedence(t *testing.T) {
	t.Parallel()

	first := setEnvFile(t, "first.env", "B=first\nC=first\nD=first\n")
	second := setEnvFile(t, "second.env", "C=second\nD=second\n")

	opts := envsubst.Options{
		Flags:          []string{"D=early", "D=flag"},
		EnvFiles:       []string{first, second},
		DefaultEnvFile: "",
		Environ:        []string{"A=shell", "B=shell", "C=shell", "D=shell", "E=a=b"},
		Reserved:       "",
	}

	got, res, err := setSubstitute(opts, "${A} ${B} ${C} ${D} ${E} ${A}")
	if err != nil {
		t.Fatalf("Substitute error = %v, want nil", err)
	}

	if want := "shell first second flag a=b shell"; got != want {
		t.Errorf("Substitute text = %q, want %q", got, want)
	}

	wantUsed := []envsubst.Var{
		{Name: "A", Value: "shell", Source: envsubst.Source{Kind: envsubst.SourceShell, Path: ""}},
		{Name: "B", Value: "first", Source: envsubst.Source{Kind: envsubst.SourceEnvFile, Path: first}},
		{Name: "C", Value: "second", Source: envsubst.Source{Kind: envsubst.SourceEnvFile, Path: second}},
		{Name: "D", Value: "flag", Source: envsubst.Source{Kind: envsubst.SourceFlag, Path: ""}},
		{Name: "E", Value: "a=b", Source: envsubst.Source{Kind: envsubst.SourceShell, Path: ""}},
	}

	if diff := cmp.Diff(wantUsed, res.Used); diff != "" {
		t.Errorf("Substitute used variables mismatch (-want +got):\n%s", diff)
	}
}

func TestSetEnvFiles(t *testing.T) {
	t.Parallel()

	def := setEnvFile(t, ".env", "A=default\n")
	named := setEnvFile(t, "named.env", "B=named\n")
	missing := filepath.Join(t.TempDir(), "missing.env")

	tests := []struct {
		name      string
		files     []string
		def       string
		text      string
		want      string
		wantFiles []envsubst.EnvFile
	}{
		{name: "the default file", def: def, text: "${A}", want: "default", wantFiles: []envsubst.EnvFile{{Path: def, Count: 1}}},
		{name: "no default file", def: missing, text: "${A:none}", want: "none", wantFiles: nil},
		{name: "no default file name", def: "", text: "${A:none}", want: "none", wantFiles: nil},
		{
			name:      "a named file replaces the default file",
			files:     []string{named},
			def:       def,
			text:      "${A:none} ${B}",
			want:      "none named",
			wantFiles: []envsubst.EnvFile{{Path: named, Count: 1}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			set, err := envsubst.NewSet(envsubst.Options{
				Flags: nil, EnvFiles: tt.files, DefaultEnvFile: tt.def, Environ: nil, Reserved: "",
			})
			if err != nil {
				t.Fatalf("NewSet error = %v, want nil", err)
			}

			res, err := set.Substitute([]envsubst.File{{Path: "a.yml", Text: []byte(tt.text)}})
			if err != nil {
				t.Fatalf("Substitute error = %v, want nil", err)
			}

			if got := string(res.Files[0].Text); got != tt.want {
				t.Errorf("Substitute(%q) = %q, want %q", tt.text, got, tt.want)
			}

			if diff := cmp.Diff(tt.wantFiles, set.EnvFiles(), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("EnvFiles mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestNewSetErrors(t *testing.T) {
	t.Parallel()

	bad := setEnvFile(t, "bad.env", "A=1\nnot a line\n")
	reserved := setEnvFile(t, "reserved.env", "A=1\nBRANCH_NAME=lab\n")
	missing := filepath.Join(t.TempDir(), "missing.env")
	environ := []string{"A=shell", "BRANCH_NAME=shell"}

	tests := []struct {
		name    string
		flags   []string
		files   []string
		wantErr error
	}{
		{name: "a flag with a bad name", flags: []string{"1A=x"}, wantErr: envsubst.ErrBadFlag},
		{name: "a flag with no name", flags: []string{"=x"}, wantErr: envsubst.ErrBadFlag},
		{name: "an empty flag", flags: []string{""}, wantErr: envsubst.ErrBadFlag},
		{name: "a flag with a space in the name", flags: []string{"A B=x"}, wantErr: envsubst.ErrBadFlag},
		{name: "a flag that names no shell variable", flags: []string{"UNSET"}, wantErr: envsubst.ErrNotInShell},
		{name: "a flag that sets the reserved name", flags: []string{"BRANCH_NAME=x"}, wantErr: envsubst.ErrReserved},
		{name: "a flag that names the reserved name", flags: []string{"BRANCH_NAME"}, wantErr: envsubst.ErrReserved},
		{name: "an env file that sets the reserved name", files: []string{reserved}, wantErr: envsubst.ErrReserved},
		{name: "a named env file that is missing", files: []string{missing}, wantErr: fs.ErrNotExist},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := envsubst.NewSet(envsubst.Options{
				Flags: tt.flags, EnvFiles: tt.files, DefaultEnvFile: "", Environ: environ, Reserved: "BRANCH_NAME",
			})
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("NewSet error = %v, want %v", err, tt.wantErr)
			}
		})
	}

	_, err := envsubst.NewSet(envsubst.Options{
		Flags: nil, EnvFiles: []string{bad}, DefaultEnvFile: "", Environ: nil, Reserved: "",
	})

	var parseErr *envsubst.ParseError
	if !errors.As(err, &parseErr) || parseErr.Path != bad || parseErr.Line != 2 {
		t.Errorf("NewSet with a bad env file error = %v, want a *ParseError for %s line 2", err, bad)
	}
}

func TestNewSetBadFlagHidesValue(t *testing.T) {
	t.Parallel()

	_, err := envsubst.NewSet(envsubst.Options{
		Flags: []string{"DB-PASS=hunter2"}, EnvFiles: nil, DefaultEnvFile: "", Environ: nil, Reserved: "",
	})
	if !errors.Is(err, envsubst.ErrBadFlag) {
		t.Fatalf("NewSet(-e DB-PASS=hunter2) error = %v, want %v", err, envsubst.ErrBadFlag)
	}

	if msg := err.Error(); !strings.Contains(msg, "DB-PASS") || strings.Contains(msg, "hunter2") {
		t.Errorf("NewSet(-e DB-PASS=hunter2) error = %q, want the name DB-PASS and not the value", msg)
	}
}

func TestSetShellFilterAndFlagsByName(t *testing.T) {
	t.Parallel()

	environ := []string{"PHENIX_UI_USERS=secret", "NODES=3", "EMPTY="}

	tests := []struct {
		name  string
		flags []string
		text  string
		want  string
	}{
		{name: "a filtered name gives its default", text: "${PHENIX_UI_USERS:none}", want: "none"},
		{name: "a flag passes a filtered name", flags: []string{"PHENIX_UI_USERS"}, text: "${PHENIX_UI_USERS}", want: "secret"},
		{name: "a flag sets a filtered name", flags: []string{"PHENIX_UI_USERS=x"}, text: "${PHENIX_UI_USERS}", want: "x"},
		{name: "a flag passes a name", flags: []string{"NODES"}, text: "${NODES}", want: "3"},
		{name: "a flag passes an empty value", flags: []string{"EMPTY"}, text: "[${EMPTY:d}]", want: "[]"},
		{name: "a flag sets an empty value", flags: []string{"NODES="}, text: "[${NODES}]", want: "[]"},
		{name: "a flag value holds an equals sign", flags: []string{"NODES=a=b"}, text: "${NODES}", want: "a=b"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, _, err := setSubstitute(envsubst.Options{
				Flags: tt.flags, EnvFiles: nil, DefaultEnvFile: "", Environ: environ, Reserved: "",
			}, tt.text)
			if err != nil || got != tt.want {
				t.Errorf("Substitute(%q) = %q, %v, want %q", tt.text, got, err, tt.want)
			}
		})
	}
}

func TestSetReservedAndEscapes(t *testing.T) {
	t.Parallel()

	const text = "${BRANCH_NAME} ${BRANCH_NAME:d} $${BRANCH_NAME} $${A} $${UNSET:d} ${A}"

	got, res, err := setSubstitute(envsubst.Options{
		Flags: nil, EnvFiles: nil, DefaultEnvFile: "", Environ: []string{"BRANCH_NAME=shell", "A=v:${A}"}, Reserved: "BRANCH_NAME",
	}, text)
	if err != nil {
		t.Fatalf("Substitute(%q) error = %v, want nil", text, err)
	}

	if want := "${BRANCH_NAME} ${BRANCH_NAME:d} $${BRANCH_NAME} ${A} ${UNSET:d} v:${A}"; got != want {
		t.Errorf("Substitute(%q) = %q, want %q", text, got, want)
	}

	wantUsed := []envsubst.Var{{Name: "A", Value: "v:${A}", Source: envsubst.Source{Kind: envsubst.SourceShell, Path: ""}}}
	if diff := cmp.Diff(wantUsed, res.Used); diff != "" {
		t.Errorf("Substitute used variables mismatch (-want +got):\n%s", diff)
	}
}

func TestSetSubstituteProblems(t *testing.T) {
	t.Parallel()

	file := setEnvFile(t, "vars.env", "FROM_FILE=\"a\rb\"\n")

	set, err := envsubst.NewSet(envsubst.Options{
		Flags:          []string{"FROM_FLAG=a\nb", "UNUSED=1", "ESCAPED=1", "USED=ok"},
		EnvFiles:       nil,
		DefaultEnvFile: "",
		Environ:        []string{"FROM_SHELL=a\nb", "PHENIX_HIDDEN=x", "FROM_FILE=shell"},
		Reserved:       "",
	})
	if err != nil {
		t.Fatalf("NewSet error = %v, want nil", err)
	}

	// The parser refuses a carriage return, so a value with a line break can
	// come only from a flag or the shell.
	if _, err := envsubst.NewSet(envsubst.Options{
		Flags: nil, EnvFiles: []string{file}, DefaultEnvFile: "", Environ: nil, Reserved: "",
	}); err == nil {
		t.Error("NewSet with a carriage return in an env file value error = nil, want an error")
	}

	files := []envsubst.File{
		{Path: "a.yml", Text: []byte("x: ${UNSET}\ny: ${FROM_FLAG}\n\nz: ${USED} $${ESCAPED} ${PHENIX_HIDDEN}\n")},
		{Path: "b.json", Text: []byte("${FROM_SHELL}${D:a\nb}\n${UNSET}")},
	}

	flag := envsubst.Source{Kind: envsubst.SourceFlag, Path: ""}
	shell := envsubst.Source{Kind: envsubst.SourceShell, Path: ""}
	none := envsubst.Source{Kind: 0, Path: ""}

	want := []envsubst.Problem{
		{Kind: envsubst.Undefined, Name: "UNSET", Path: "a.yml", Line: 1, Source: none, Filtered: false},
		{Kind: envsubst.ValueLineBreak, Name: "FROM_FLAG", Path: "a.yml", Line: 2, Source: flag, Filtered: false},
		{Kind: envsubst.Undefined, Name: "PHENIX_HIDDEN", Path: "a.yml", Line: 4, Source: none, Filtered: true},
		{Kind: envsubst.ValueLineBreak, Name: "FROM_SHELL", Path: "b.json", Line: 1, Source: shell, Filtered: false},
		{Kind: envsubst.DefaultLineBreak, Name: "D", Path: "b.json", Line: 1, Source: none, Filtered: false},
		{Kind: envsubst.Undefined, Name: "UNSET", Path: "b.json", Line: 3, Source: none, Filtered: false},
		{Kind: envsubst.UnusedFlag, Name: "UNUSED", Path: "", Line: 0, Source: flag, Filtered: false},
		{Kind: envsubst.UnusedFlag, Name: "ESCAPED", Path: "", Line: 0, Source: flag, Filtered: false},
	}

	res, err := set.Substitute(files)

	var subErr *envsubst.Error
	if !errors.As(err, &subErr) {
		t.Fatalf("Substitute = %+v, %v, want an *Error", res, err)
	}

	if diff := cmp.Diff(want, subErr.Problems); diff != "" {
		t.Errorf("Substitute problems mismatch (-want +got):\n%s", diff)
	}

	if len(res.Files) != 0 {
		t.Errorf("Substitute with problems gave %d files, want none", len(res.Files))
	}

	for _, problem := range subErr.Problems {
		if !strings.Contains(subErr.Error(), problem.String()) || !strings.Contains(problem.String(), problem.Name) {
			t.Errorf("Error() = %q, want it to hold %q, which names %s", subErr.Error(), problem.String(), problem.Name)
		}
	}
}

func TestSetSubstituteWarnings(t *testing.T) {
	t.Parallel()

	const text = "a: ${A:-3}\nb: ${B:?needed} ${C:ok}\nc: $${D:-1} ${E:}\n"

	_, res, err := setSubstitute(envsubst.Options{
		Flags: nil, EnvFiles: nil, DefaultEnvFile: "", Environ: []string{"B=set"}, Reserved: "",
	}, text)
	if err != nil {
		t.Fatalf("Substitute(%q) error = %v, want nil", text, err)
	}

	want := []envsubst.Warning{
		{Path: "a.yml", Line: 1, Name: "A", Default: "-3"},
		{Path: "a.yml", Line: 2, Name: "B", Default: "?needed"},
	}

	if diff := cmp.Diff(want, res.Warnings); diff != "" {
		t.Errorf("Substitute warnings mismatch (-want +got):\n%s", diff)
	}
}

func TestSourceString(t *testing.T) {
	t.Parallel()

	for source, want := range map[envsubst.Source]string{
		{Kind: envsubst.SourceFlag, Path: ""}:          "flag",
		{Kind: envsubst.SourceEnvFile, Path: "a/.env"}: "env file a/.env",
		{Kind: envsubst.SourceShell, Path: ""}:         "shell",
		{Kind: 0, Path: ""}:                            "unknown",
	} {
		if got := source.String(); got != want {
			t.Errorf("Source%+v.String() = %q, want %q", source, got, want)
		}
	}
}

// TestSetSubstituteProperties checks the order of the sources, and that a
// text with no problem gives what Expand gives with the values of the set.
func TestSetSubstituteProperties(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	rapid.Check(t, func(t *rapid.T) {
		names := []string{"A", "B", "C", "D"}
		want := map[string]string{}

		var (
			environ, flags []string
			fileText       strings.Builder
		)

		for _, name := range names {
			if rapid.Bool().Draw(t, name+" in the shell") {
				environ = append(environ, name+"=shell")
				want[name] = "shell"
			}

			if rapid.Bool().Draw(t, name+" in the env file") {
				fileText.WriteString(name + "=file\n")

				want[name] = "file"
			}

			if rapid.Bool().Draw(t, name+" in a flag") {
				flags = append(flags, name+"=flag")
				want[name] = "flag"
			}
		}

		path := filepath.Join(dir, "vars.env")
		if err := os.WriteFile(path, []byte(fileText.String()), 0o600); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}

		// Each flag is used, and each name has a default, so there is no problem.
		var text strings.Builder

		for _, name := range names {
			text.WriteString("${" + name + ":d} ")
		}

		text.WriteString(rapid.StringMatching(`[a ${}:A-D]{0,12}`).Draw(t, "tail"))

		set, err := envsubst.NewSet(envsubst.Options{
			Flags: flags, EnvFiles: []string{path}, DefaultEnvFile: "", Environ: environ, Reserved: "",
		})
		if err != nil {
			t.Fatalf("NewSet error = %v, want nil", err)
		}

		res, err := set.Substitute([]envsubst.File{{Path: "a.yml", Text: []byte(text.String())}})

		var subErr *envsubst.Error
		if errors.As(err, &subErr) {
			return // the tail holds a placeholder with no value and no default
		}

		if err != nil {
			t.Fatalf("Substitute(%q) error = %v, want nil or an *Error", text.String(), err)
		}

		model := string(envsubst.Expand([]byte(text.String()), envsubstLookup(want)))
		if got := string(res.Files[0].Text); got != model {
			t.Fatalf("Substitute(%q) = %q, want %q", text.String(), got, model)
		}
	})
}
