package web

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"pgregory.net/rapid"

	"phenix/store"
	"phenix/util/envsubst"
)

// fillTestEnv is the server environment of the fill tests: FROM_ENV and
// BOTH are set.
func fillTestEnv(name string) (string, bool) {
	switch name {
	case "FROM_ENV":
		return "env", true
	case "BOTH":
		return "env-both", true
	default:
		return "", false
	}
}

func TestFillVariables(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		text    string
		vars    map[string]string
		include bool // an included file, which reads the server environment
		want    FilledText
	}{
		{
			name: "request value",
			text: "a: ${A}\n",
			vars: map[string]string{"A": "x"},
			want: FilledText{Text: []byte("a: x\n"), Used: []string{"A"}, Warnings: nil},
		},
		{
			name: "default without a value",
			text: "a: ${A:d}\n",
			want: FilledText{Text: []byte("a: d\n"), Used: []string{}, Warnings: nil},
		},
		{
			name: "request value over the default",
			text: "a: ${A:d}\n",
			vars: map[string]string{"A": "x"},
			want: FilledText{Text: []byte("a: x\n"), Used: []string{"A"}, Warnings: nil},
		},
		{
			name: "escapes",
			text: "$${A} $${BRANCH_NAME} $${U:d}",
			vars: map[string]string{"A": "x"},
			want: FilledText{Text: []byte("${A} $${BRANCH_NAME} ${U:d}"), Used: []string{}, Warnings: nil},
		},
		{
			name: "branch name stays for the second pass",
			text: "${BRANCH_NAME} ${BRANCH_NAME:d}",
			want: FilledText{Text: []byte("${BRANCH_NAME} ${BRANCH_NAME:d}"), Used: []string{}, Warnings: nil},
		},
		{
			name: "a value that holds the branch name",
			text: "dir: ${DIR}\n",
			vars: map[string]string{"DIR": "/phenix/injects/${BRANCH_NAME}"},
			want: FilledText{Text: []byte("dir: /phenix/injects/${BRANCH_NAME}\n"), Used: []string{"DIR"}, Warnings: nil},
		},
		{
			name: "only used names, sorted",
			text: "${B} ${A} ${B}",
			vars: map[string]string{"A": "1", "B": "2", "UNUSED": "3\n"},
			want: FilledText{Text: []byte("2 1 2"), Used: []string{"A", "B"}, Warnings: nil},
		},
		{
			name: "undefined in a config stays as written",
			text: "a: 1\nb: ${U}\n",
			vars: map[string]string{},
			want: FilledText{
				Text: []byte("a: 1\nb: ${U}\n"), Used: []string{},
				Warnings: []VariableWarning{{File: "f.yml", Line: 2, Name: "U", Kind: KeptAsWritten}},
			},
		},
		{
			name: "a config never reads the server environment",
			text: "${FROM_ENV}",
			want: FilledText{
				Text: []byte("${FROM_ENV}"), Used: []string{},
				Warnings: []VariableWarning{{File: "f.yml", Line: 1, Name: "FROM_ENV", Kind: KeptAsWritten}},
			},
		},
		{
			name:    "server environment for an included file",
			text:    "\n${FROM_ENV}",
			include: true,
			want: FilledText{
				Text: []byte("\nenv"), Used: []string{},
				Warnings: []VariableWarning{{File: "f.yml", Line: 2, Name: "FROM_ENV", Kind: FromServerEnv}},
			},
		},
		{
			name:    "request value over the server environment",
			text:    "${BOTH}",
			vars:    map[string]string{"BOTH": "request"},
			include: true,
			want:    FilledText{Text: []byte("request"), Used: []string{"BOTH"}, Warnings: nil},
		},
		{
			name:    "server environment over the default",
			text:    "${FROM_ENV:d}",
			include: true,
			want: FilledText{
				Text: []byte("env"), Used: []string{},
				Warnings: []VariableWarning{{File: "f.yml", Line: 1, Name: "FROM_ENV", Kind: FromServerEnv}},
			},
		},
		{
			name:    "default in an included file",
			text:    "${U:d}",
			include: true,
			want:    FilledText{Text: []byte("d"), Used: []string{}, Warnings: nil},
		},
		{
			name:    "undefined in an included file is empty",
			text:    "a: x${U}y\n",
			include: true,
			want: FilledText{
				Text: []byte("a: xy\n"), Used: []string{},
				Warnings: []VariableWarning{{File: "f.yml", Line: 1, Name: "U", Kind: FilledEmpty}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var env envsubst.Lookup
			if tt.include {
				env = fillTestEnv
			}

			got, err := FillVariables([]byte(tt.text), "f.yml", "main", tt.vars, env)
			if err != nil {
				t.Fatalf("FillVariables(%q) error = %v", tt.text, err)
			}

			if diff := cmp.Diff(tt.want, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("FillVariables(%q) mismatch (-want +got):\n%s", tt.text, diff)
			}
		})
	}
}

// TestFillVariablesLineBreaks checks that a used value or a default with a
// line break is an error that names the file, the line and the variable, and
// never the value.
func TestFillVariablesLineBreaks(t *testing.T) {
	t.Parallel()

	const valueLineBreak = "f.yml line 1: the value of the request variable A holds a line break"

	tests := []struct {
		name, text string
		vars       map[string]string
		want       string
	}{
		{name: "default", text: "a: 1\nb: ${D:se\ncret}\n", want: "f.yml line 2: the default of D holds a line break"},
		{
			name: "default of a set variable", text: "a: ${A:x\ny}\n", vars: map[string]string{"A": "v"},
			want: "f.yml line 1: the default of A holds a line break",
		},
		{name: "value with CR", text: "a: ${A}\n", vars: map[string]string{"A": "se\rcret"}, want: valueLineBreak},
		{name: "value with LF", text: "a: ${A}\n", vars: map[string]string{"A": "se\ncret"}, want: valueLineBreak},
		{name: "value with NEL", text: "a: ${A}\n", vars: map[string]string{"A": "se\u0085cret"}, want: valueLineBreak},
		{name: "value with LS", text: "a: ${A}\n", vars: map[string]string{"A": "se\u2028cret"}, want: valueLineBreak},
		{name: "value with PS", text: "a: ${A}\n", vars: map[string]string{"A": "se\u2029cret"}, want: valueLineBreak},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for _, env := range []envsubst.Lookup{nil, fillTestEnv} {
				got, err := FillVariables([]byte(tt.text), "f.yml", "main", tt.vars, env)
				if !errors.Is(err, errVariableLineBreak) {
					t.Fatalf("FillVariables(%q) = %q, %v, want an error that wraps errVariableLineBreak", tt.text, got.Text, err)
				}

				if !strings.Contains(err.Error(), tt.want) || strings.Contains(err.Error(), "se") {
					t.Errorf("FillVariables(%q) error = %q, want it to hold %q and no value", tt.text, err, tt.want)
				}
			}
		})
	}
}

// TestFillVariablesLineBreaksAreBounded checks that the error of many line
// break problems names the first ones and counts the others.
func TestFillVariablesLineBreaksAreBounded(t *testing.T) {
	t.Parallel()

	_, err := FillVariables([]byte(strings.Repeat("${A}\n", 1000)), "f.yml", "main", map[string]string{"A": "se\ncret"}, nil)
	if !errors.Is(err, errVariableLineBreak) {
		t.Fatalf("FillVariables() error = %v, want an error that wraps errVariableLineBreak", err)
	}

	msg := err.Error()

	for _, want := range []string{
		"f.yml line 1: the value of the request variable A holds a line break; f.yml line 2: ",
		"f.yml line 10: the value of the request variable A holds a line break; and 990 more",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("FillVariables() error = %q, want it to hold %q", msg, want)
		}
	}

	if len(msg) > 4<<10 || strings.Contains(msg, "line 11:") || strings.Contains(msg, "se") {
		t.Errorf("FillVariables() error is %d bytes long: %q, want at most 4 KiB, 10 problems and no value", len(msg), msg)
	}
}

// TestFillVariablesMatchesSetSubstitute checks that for a text with no lenient
// step the server gives what the CLI's strict substitution gives, both before
// and after ${BRANCH_NAME} is filled in, with the same used names. A text that
// the CLI refuses only for undefined variables gets a warning for each.
func TestFillVariablesMatchesSetSubstitute(t *testing.T) {
	t.Parallel()

	pieces := []string{
		"${A}", "${B:d}", "${C:}", "$${A}", "${BRANCH_NAME}", "$${BRANCH_NAME}", "${BRANCH_NAME:x}",
		"${A:/a/${BRANCH_NAME}}", "a", " ", "\n", "$", "{", "}", ":", "$$",
	}
	values := []string{"", "v", "${BRANCH_NAME}", "$${A}", "x:y", "${B}", "$", "l\nf", "l\u2028s"}

	rapid.Check(t, func(t *rapid.T) {
		var environ []string

		vars := map[string]string{}

		for _, name := range []string{"A", "B", "C"} {
			if rapid.Bool().Draw(t, name+" is set") {
				value := rapid.SampledFrom(values).Draw(t, name+" value")
				vars[name] = value
				environ = append(environ, name+"="+value)
			}
		}

		text := strings.Join(rapid.SliceOfN(rapid.SampledFrom(pieces), 0, 12).Draw(t, "pieces"), "")

		set, err := envsubst.NewSet(envsubst.Options{
			Flags: nil, EnvFiles: nil, DefaultEnvFile: "", Environ: environ, Reserved: store.BranchNameVar,
		})
		if err != nil {
			t.Fatalf("NewSet error = %v", err)
		}

		filled, fillErr := FillVariables([]byte(text), "a.yml", "lab", vars, nil)
		res, err := set.Substitute([]envsubst.File{{Path: "a.yml", Text: []byte(text)}})

		var (
			subErr *envsubst.Error
			want   []VariableWarning
			broken bool
		)

		if errors.As(err, &subErr) {
			for _, problem := range subErr.Problems {
				broken = broken || problem.Kind != envsubst.Undefined
				want = append(want, VariableWarning{File: "a.yml", Line: problem.Line, Name: problem.Name, Kind: KeptAsWritten})
			}
		}

		// A default that the pieces join across a line break, or a used value
		// that holds one.
		if broken || fillErr != nil {
			if !broken || !errors.Is(fillErr, errVariableLineBreak) {
				t.Fatalf("FillVariables(%q) error = %v, CLI error = %v, want a line break error from both", text, fillErr, err)
			}

			return
		}

		if subErr != nil {
			if diff := cmp.Diff(want, filled.Warnings); diff != "" {
				t.Fatalf("FillVariables(%q) warnings mismatch with the CLI problems (-want +got):\n%s", text, diff)
			}

			return
		}

		if err != nil {
			t.Fatalf("Substitute(%q) error = %v", text, err)
		}

		if len(filled.Warnings) != 0 {
			t.Fatalf("FillVariables(%q) warnings = %v, want none", text, filled.Warnings)
		}

		if got, want := string(filled.Text), string(res.Files[0].Text); got != want {
			t.Fatalf("FillVariables(%q) = %q, want %q", text, got, want)
		}

		branch := envsubst.Fixed("lab")
		if got, want := store.ExpandBranchName(
			filled.Text,
			branch,
		), store.ExpandBranchName(
			res.Files[0].Text,
			branch,
		); string(
			got,
		) != string(
			want,
		) {
			t.Fatalf("FillVariables(%q) with the branch = %q, want %q", text, got, want)
		}

		used := make([]string, 0, len(res.Used))
		for _, v := range res.Used {
			used = append(used, v.Name)
		}

		if !slices.Equal(filled.Used, used) {
			t.Fatalf("FillVariables(%q) used = %q, want %q", text, filled.Used, used)
		}
	})
}

func TestVariableWarningString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		warning VariableWarning
		want    string
	}{
		{
			warning: VariableWarning{File: "/srv/inc.yml", Line: 3, Name: "SITE", Kind: FromServerEnv},
			want:    "/srv/inc.yml line 3: the request has no variable SITE; phenix used the value from the server environment",
		},
		{
			warning: VariableWarning{File: "Topology/lab", Line: 4, Name: "SITE", Kind: KeptAsWritten},
			want: "Topology/lab line 4: the request has no variable SITE and the placeholder has no default; " +
				"phenix kept the text ${SITE}",
		},
		{
			warning: VariableWarning{File: "/srv/inc.yml", Line: 5, Name: "SITE", Kind: FilledEmpty},
			want: "/srv/inc.yml line 5: no variable SITE in the request or in the server environment, " +
				"and the placeholder has no default; phenix used an empty string",
		},
	}

	for _, tt := range tests {
		if got := tt.warning.String(); got != tt.want {
			t.Errorf("%+v.String() = %q, want %q", tt.warning, got, tt.want)
		}
	}
}

// parseVariablesCases are the variables parts of the parser tests and the
// seeds of its fuzz target. A nil want is an error.
func parseVariablesCases() []struct {
	name, data string
	want       map[string]string
} {
	return []struct {
		name, data string
		want       map[string]string
	}{
		{name: "empty object", data: `{}`, want: map[string]string{}},
		{
			name: "values",
			data: ` {"A":"x", "b_2":"", "_C":"${BRANCH_NAME}"} `,
			want: map[string]string{"A": "x", "b_2": "", "_C": "${BRANCH_NAME}"},
		},
		{name: "a line break is a value like another", data: `{"A":"a\nb"}`, want: map[string]string{"A": "a\nb"}},
		{name: "empty text", data: ``},
		{name: "array", data: `["secret"]`},
		{name: "string", data: `"secret"`},
		{name: "null", data: `null`},
		{name: "unclosed", data: `{"A":"secret"`},
		{name: "number value", data: `{"A":12345}`},
		{name: "null value", data: `{"A":null}`},
		{name: "object value", data: `{"A":{"B":"secret"}}`},
		{name: "array value", data: `{"A":["secret"]}`},
		{name: "duplicate name", data: `{"A":"secret1","A":"secret2"}`},
		{name: "name starts with a digit", data: `{"1A":"secret"}`},
		{name: "name with a dash", data: `{"A-B":"secret"}`},
		{name: "empty name", data: `{"":"secret"}`},
		{name: "branch name", data: `{"BRANCH_NAME":"secret"}`},
		{name: "a second value", data: `{"A":"secret"} {}`},
		{name: "text after the object", data: `{"A":"secret"}x`},
	}
}

func TestParseRequestVariables(t *testing.T) {
	t.Parallel()

	for _, tt := range parseVariablesCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseRequestVariables([]byte(tt.data))
			if tt.want == nil {
				if !errors.Is(err, errBadVariables) || got != nil {
					t.Fatalf("parseRequestVariables(%s) = %v, %v, want an error that wraps errBadVariables", tt.data, got, err)
				}

				if strings.Contains(err.Error(), "secret") {
					t.Errorf("parseRequestVariables(%s) error = %q, want no value", tt.data, err)
				}

				return
			}

			if err != nil {
				t.Fatalf("parseRequestVariables(%s) error = %v", tt.data, err)
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("parseRequestVariables(%s) mismatch (-want +got):\n%s", tt.data, diff)
			}
		})
	}
}

func TestParseRequestVariablesBranchNameError(t *testing.T) {
	t.Parallel()

	_, err := parseRequestVariables([]byte(`{"BRANCH_NAME":"x"}`))
	if want := "the branch in the path sets the branch name"; err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("parseRequestVariables(BRANCH_NAME) error = %v, want it to say %q", err, want)
	}
}
