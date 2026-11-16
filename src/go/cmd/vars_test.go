package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/spf13/cobra"

	"phenix/util/envsubst"
)

// varsTestCmd returns a command with the variable flags, parsed from args.
func varsTestCmd(t *testing.T, args ...string) *cobra.Command {
	t.Helper()

	cmd := &cobra.Command{Use: "vars"}
	addVarFlags(cmd)

	if err := cmd.ParseFlags(args); err != nil {
		t.Fatalf("ParseFlags(%q) error = %v", args, err)
	}

	return cmd
}

func TestAddVarFlags(t *testing.T) {
	t.Parallel()

	cmd := varsTestCmd(t)

	for name, shorthand := range map[string]string{envFlagName: "e", envFileFlagName: ""} {
		flag := cmd.Flags().Lookup(name)
		if flag == nil {
			t.Fatalf("flag --%s is not defined", name)
		}

		if flag.Shorthand != shorthand || flag.DefValue != "[]" {
			t.Errorf("flag --%s = shorthand %q, default %q, want %q, []", name, flag.Shorthand, flag.DefValue, shorthand)
		}
	}
}

func TestVarOptionsFromFlags(t *testing.T) {
	t.Setenv("PHENIX_VARS_TEST", "from-the-shell")

	cmd := varsTestCmd(t, "-e", "A=1,2", "--env", "B", "--env-file", "a.env", "--env-file=b.env")

	want := envsubst.Options{
		Flags:          []string{"A=1,2", "B"},
		EnvFiles:       []string{"a.env", "b.env"},
		DefaultEnvFile: "",
		Environ:        os.Environ(),
		Reserved:       "",
	}

	if diff := cmp.Diff(want, varOptionsFromFlags(cmd)); diff != "" {
		t.Errorf("varOptionsFromFlags mismatch (-want +got):\n%s", diff)
	}

	none := varOptionsFromFlags(varsTestCmd(t))
	if len(none.Flags) != 0 || len(none.EnvFiles) != 0 {
		t.Errorf("varOptionsFromFlags with no flags = %q, %q, want none", none.Flags, none.EnvFiles)
	}
}

func TestNewVarSet(t *testing.T) {
	logs := captureLogs(t)

	path := filepath.Join(t.TempDir(), "site.env")
	if err := os.WriteFile(path, []byte("A=1\nB=2\n"), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}

	opts := envsubst.Options{Flags: nil, EnvFiles: []string{path}, DefaultEnvFile: "", Environ: nil, Reserved: "BRANCH_NAME"}

	if _, err := newVarSet(opts, "step", stepPreflight); err != nil {
		t.Fatalf("newVarSet error = %v, want nil", err)
	}

	checkFields(t, logs.first(t, "env file read"), map[string]string{"step": stepPreflight, "file": path, "variables": "2"})

	opts.Flags = []string{"BRANCH_NAME=lab"}
	if _, err := newVarSet(opts); !errors.Is(err, envsubst.ErrReserved) {
		t.Errorf("newVarSet with a flag for the reserved name error = %v, want ErrReserved", err)
	}

	opts.Flags = []string{"not a name=x"}
	if _, err := newVarSet(opts); !errors.Is(err, envsubst.ErrBadFlag) {
		t.Errorf("newVarSet with a bad flag error = %v, want ErrBadFlag", err)
	}
}

func TestSubstituteVars(t *testing.T) {
	logs := captureLogs(t)

	const secret = "s3cret-value"

	set, err := envsubst.NewSet(envsubst.Options{
		Flags:          []string{"TOKEN=" + secret},
		EnvFiles:       nil,
		DefaultEnvFile: "",
		Environ:        []string{"NODES=3"},
		Reserved:       "",
	})
	if err != nil {
		t.Fatalf("NewSet error = %v", err)
	}

	files := []envsubst.File{{Path: "topology.yml", Text: []byte("count: ${NODES}\ntoken: ${TOKEN}\noffset: ${OFFSET:-1}\n")}}

	got, err := substituteVars(set, files, "step", stepPreflight)
	if err != nil {
		t.Fatalf("substituteVars error = %v, want nil", err)
	}

	want := []envsubst.File{{Path: "topology.yml", Text: []byte("count: 3\ntoken: " + secret + "\noffset: -1\n")}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("substituteVars files mismatch (-want +got):\n%s", diff)
	}

	checkFields(t, logs.first(t, "variables filled in"), map[string]string{
		"step": stepPreflight, "variables": "NODES (shell), TOKEN (flag)",
	})

	checkFields(t, logs.first(t, composeDefaultWarning), map[string]string{
		"file": "topology.yml", "line": "3", "variable": "OFFSET", "default": "-1",
	})

	// Only a debug record holds a value.
	for _, record := range logs.records(t) {
		if strings.Contains(fmt.Sprint(record), secret) && record["level"] != "DEBUG" {
			t.Errorf("the %v record %q holds a variable value: %v", record["level"], record["msg"], record)
		}
	}

	checkFields(t, logs.all(t, "variable value")[1], map[string]string{"variable": "TOKEN", "source": "flag", "value": secret})
}

func TestSubstituteVarsProblems(t *testing.T) {
	logs := captureLogs(t)

	set, err := envsubst.NewSet(envsubst.Options{
		Flags:          []string{"UNUSED=1", "LINES=a\nb"},
		EnvFiles:       nil,
		DefaultEnvFile: "",
		Environ:        []string{"PHENIX_HIDDEN=x"},
		Reserved:       "",
	})
	if err != nil {
		t.Fatalf("NewSet error = %v", err)
	}

	files := []envsubst.File{{Path: "a.yml", Text: []byte("a: ${UNSET}\nb: ${PHENIX_HIDDEN}\nc: ${LINES}\nd: ${D:x\ny}\n")}}

	got, err := substituteVars(set, files)
	if !errors.Is(err, errVariables) || got != nil {
		t.Fatalf("substituteVars = %v, %v, want no files and errVariables", got, err)
	}

	wantErrors := []string{
		"variable is not set and has no default",
		"variable is not set and has no default",
		"variable value holds a line break",
		"variable default holds a line break",
		"no file uses the variable that -e sets",
	}

	if diff := cmp.Diff(wantErrors, logs.messages(t, "ERROR"), cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("error records mismatch (-want +got):\n%s", diff)
	}

	undefined := logs.all(t, "variable is not set and has no default")
	checkFields(t, undefined[0], map[string]string{"variable": "UNSET", "file": "a.yml", "line": "1"})
	checkFields(t, undefined[1], map[string]string{
		"variable": "PHENIX_HIDDEN", "line": "2",
		"hint": "the shell has it, but PHENIX_ variables are not used; pass -e PHENIX_HIDDEN",
	})

	if _, has := undefined[0]["hint"]; has {
		t.Errorf("the record for UNSET has a hint: %v", undefined[0])
	}

	checkFields(t, logs.first(t, "variable value holds a line break"), map[string]string{
		"variable": "LINES", "source": "flag", "line": "3",
	})
	checkFields(t, logs.first(t, "variable default holds a line break"), map[string]string{"variable": "D", "line": "4"})
	checkFields(t, logs.first(t, "no file uses the variable that -e sets"), map[string]string{"variable": "UNUSED"})
}
