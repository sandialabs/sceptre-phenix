package web

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"
	gocmp "github.com/google/go-cmp/cmp"

	"phenix/store"
	"phenix/util/plog"
)

// FillVariables refuses a text that the variables make larger than
// maxFilledText, and keeps one that is just as large.
func TestFillVariablesLimitsFilledText(t *testing.T) {
	t.Parallel()

	value := strings.Repeat("A", 1<<20)

	tests := []struct {
		name     string
		text     string
		wantSize int
		wantErr  bool
	}{
		{name: "five copies of 1 MiB fit exactly", text: strings.Repeat("${A}", 5), wantSize: 5 << 20},
		{name: "six copies are refused", text: strings.Repeat("${A}", 6), wantErr: true},
		{name: "many copies are refused", text: strings.Repeat("${A}\n", 300), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := FillVariables([]byte(tt.text), "f", "main", map[string]string{"A": value}, nil)

			if tt.wantErr {
				if !errors.Is(err, errFilledTooLarge) || !strings.Contains(err.Error(), "5 MiB") {
					t.Fatalf("FillVariables() error = %v, want errFilledTooLarge that names 5 MiB", err)
				}

				return
			}

			if err != nil || len(got.Text) != tt.wantSize {
				t.Errorf("FillVariables() = %d bytes, %v; want %d bytes", len(got.Text), err, tt.wantSize)
			}
		})
	}
}

// FillVariables decides on the size of the text after the variables and the
// branch name are filled in: an escaped ${BRANCH_NAME} counts as its literal
// text. The subtests run one at a time to keep the memory of the test low.
func TestFillVariablesLimitsFinalText(t *testing.T) {
	longBranch := strings.Repeat("b", 10<<10)

	// pad returns prefix and then "a" up to size bytes.
	pad := func(prefix string, size int) func() []byte {
		return func() []byte {
			text := bytes.Repeat([]byte("a"), size)
			copy(text, prefix)

			return text
		}
	}

	tests := []struct {
		name     string
		text     func() []byte
		vars     map[string]string
		branch   string
		wantSize int
		wantErr  bool
	}{
		{
			name:   "branch names in the text count",
			text:   func() []byte { return bytes.Repeat([]byte("${BRANCH_NAME}"), 1000) },
			branch: longBranch, wantErr: true,
		},
		{
			name:   "branch names in a value count",
			text:   func() []byte { return bytes.Repeat([]byte("${D}"), 1000) },
			vars:   map[string]string{"D": "${BRANCH_NAME}"},
			branch: longBranch, wantErr: true,
		},
		{
			name: "placeholders that make a large text small",
			text: func() []byte {
				return append(pad("", 4<<20)(), strings.Repeat("${A:"+strings.Repeat("d", 146)+"}", 10000)...)
			},
			vars:   map[string]string{"A": ""},
			branch: "main", wantSize: 4 << 20,
		},
		{
			name:   "a large text with no placeholder",
			text:   pad("", maxFilledText+1),
			branch: "main", wantErr: true,
		},
		{
			name:   "exactly 5 MiB with no placeholder",
			text:   pad("", maxFilledText),
			branch: "main", wantSize: maxFilledText,
		},
		{
			name:   "exactly 5 MiB after the branch name",
			text:   pad("${BRANCH_NAME}", maxFilledText-len(longBranch)+len("${BRANCH_NAME}")),
			branch: longBranch, wantSize: maxFilledText - len(longBranch) + len("${BRANCH_NAME}"),
		},
		{
			name:   "one byte more after the branch name",
			text:   pad("${BRANCH_NAME}", maxFilledText-len(longBranch)+len("${BRANCH_NAME}")+1),
			branch: longBranch, wantErr: true,
		},
		{
			name:   "an escaped branch name counts as its literal text",
			text:   pad("$${BRANCH_NAME}", maxFilledText+1),
			branch: longBranch, wantSize: maxFilledText + 1,
		},
		{
			name:   "a short branch name makes the text smaller",
			text:   pad("${BRANCH_NAME}", maxFilledText-len("main")+len("${BRANCH_NAME}")),
			branch: "main", wantSize: maxFilledText - len("main") + len("${BRANCH_NAME}"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := FillVariables(tt.text(), "f", tt.branch, tt.vars, nil)

			if tt.wantErr {
				if !errors.Is(err, errFilledTooLarge) || !strings.Contains(err.Error(), "f is larger than 5 MiB") {
					t.Fatalf("FillVariables() error = %v, want errFilledTooLarge that names the file and 5 MiB", err)
				}

				return
			}

			if err != nil || len(got.Text) != tt.wantSize {
				t.Errorf("FillVariables() = %d bytes, %v; want %d bytes", len(got.Text), err, tt.wantSize)
			}
		})
	}
}

// A config of six 900 KiB values is a 400 that names the limit, not a 200.
func TestWorkflowUpsertConfigFilledTextLimit(t *testing.T) {
	installValidationTestStore(t)

	cfg := varsTopology("t", "") + strings.Repeat("# ${A}\n", 6)
	body, typ := varsTestRequest(t, mimeYAML, cfg, map[string]string{"A": strings.Repeat("a", 900<<10)})
	rec := httptest.NewRecorder()

	err := WorkflowUpsertConfig(rec, varsUpsertRequest(t, "?dryRun=true", body, typ))
	if err == nil || !errors.Is(err, errFilledTooLarge) || !strings.Contains(err.Error(), "5 MiB") {
		t.Fatalf("WorkflowUpsertConfig() error = %v, want a 400 that names 5 MiB", err)
	}
}

// A config that a long branch name makes larger than 5 MiB is a 400 that
// names the limit, not a 200.
func TestWorkflowUpsertConfigBranchNameLimit(t *testing.T) {
	installValidationTestStore(t)

	branch := strings.Repeat("b", 10<<10)
	cfg := varsTopology("t", "") + strings.Repeat("# ${BRANCH_NAME}\n", 1000)
	body, typ := varsTestRequest(t, mimeYAML, cfg, map[string]string{})
	req := validationTestRequest(
		t, http.MethodPost, "/api/v1/workflow/configs/"+branch+"?dryRun=true", typ, body, map[string]string{"branch": branch},
	)

	err := WorkflowUpsertConfig(httptest.NewRecorder(), req)
	if err == nil || !errors.Is(err, errFilledTooLarge) || !strings.Contains(err.Error(), "5 MiB") {
		t.Fatalf("WorkflowUpsertConfig() error = %v, want a 400 that names 5 MiB", err)
	}
}

// One undefined variable on many lines gives one warning with the first line
// and the count of the others; the response and the log stay short.
func TestWorkflowUpsertConfigWarningsAreBounded(t *testing.T) {
	m := installValidationTestStore(t)
	expectUpsertExists(m, workflowTestKey("topology/t"), false)

	var cfg strings.Builder

	cfg.WriteString(varsTopology("t", ""))

	for range 1000 {
		cfg.WriteString("# ${U}\n")
	}

	for i := range 200 {
		fmt.Fprintf(&cfg, "# ${V%d}\n", i)
	}

	body, typ := varsTestRequest(t, mimeYAML, cfg.String(), map[string]string{})
	rec := httptest.NewRecorder()

	if err := WorkflowUpsertConfig(rec, varsUpsertRequest(t, "?dryRun=true", body, typ)); err != nil {
		t.Fatalf("WorkflowUpsertConfig() error = %v", err)
	}

	warnings, _ := decodeVarsBody(t, rec)["warnings"].([]any)
	if len(warnings) != maxVariableWarnings+1 {
		t.Fatalf("got %d warnings, want %d", len(warnings), maxVariableWarnings+1)
	}

	wantFirst := "Topology/t line 7 (and 999 other lines): the request has no variable U and the placeholder has no default; " +
		"phenix kept the text ${U}"
	if warnings[0] != wantFirst {
		t.Errorf("first warning = %q, want %q", warnings[0], wantFirst)
	}

	wantLast := "phenix left out 151 more warnings: it reports at most 50 for each request"
	if warnings[len(warnings)-1] != wantLast {
		t.Errorf("last warning = %q, want %q", warnings[len(warnings)-1], wantLast)
	}
}

// A second fill of the same file adds no line to a warning, and the warnings
// past the limit count once for each file, variable and kind.
func TestWorkflowVarsCountsWarningsOnce(t *testing.T) {
	t.Parallel()

	var text strings.Builder

	for i := range maxVariableWarnings + 2 {
		fmt.Fprintf(&text, "${V%d}\n${V%d}\n", i, i)
	}

	vars := newWorkflowVars("main", map[string]string{})

	for range 2 {
		if _, err := vars.fill([]byte(text.String()), "f", nil); err != nil {
			t.Fatalf("fill() error = %v, want nil", err)
		}
	}

	texts := vars.warningTexts()
	if len(texts) != maxVariableWarnings+1 {
		t.Fatalf("got %d warnings, want %d", len(texts), maxVariableWarnings+1)
	}

	want := []string{
		"f line 1 (and 1 other line): the request has no variable V0 and the placeholder has no default; " +
			"phenix kept the text ${V0}",
		"phenix left out 2 more warnings: it reports at most 50 for each request",
	}
	if diff := gocmp.Diff(want, []string{texts[0], texts[len(texts)-1]}); diff != "" {
		t.Errorf("first and last warning mismatch (-want +got):\n%s", diff)
	}
}

// A long config name does not repeat in each warning.
func TestWorkflowUpsertConfigLongNameIsNotInWarnings(t *testing.T) {
	m := installValidationTestStore(t)
	name := strings.Repeat("n", 10000)
	expectUpsertExists(m, workflowTestKey("topology/"+name), false)

	cfg := varsTopology(name, "") + strings.Repeat("# ${U}\n", 100)
	body, typ := varsTestRequest(t, mimeYAML, cfg, map[string]string{})
	rec := httptest.NewRecorder()

	if err := WorkflowUpsertConfig(rec, varsUpsertRequest(t, "?dryRun=true", body, typ)); err != nil {
		t.Fatalf("WorkflowUpsertConfig() error = %v", err)
	}

	warnings, _ := decodeVarsBody(t, rec)["warnings"].([]any)
	if len(warnings) != 1 {
		t.Fatalf("got %d warnings, want 1", len(warnings))
	}

	if text, _ := warnings[0].(string); len(text) > 300 {
		t.Errorf("the warning is %d bytes long, and holds the config name", len(text))
	}
}

// A config name with a line break is not in the console log message.
func TestWorkflowUpsertConfigNameCannotForgeLogLine(t *testing.T) {
	m := installValidationTestStore(t)
	m.EXPECT().Get(gomock.Any()).Return(fmt.Errorf("%w: absent", store.ErrNotExist)).AnyTimes()

	var console bytes.Buffer

	plog.NewPhenixHandler(&console, false)
	t.Cleanup(func() { plog.NewPhenixHandler(os.Stderr, false) })

	cfg := "apiVersion: phenix.sandia.gov/v1\nkind: Topology\nmetadata:\n" +
		"  name: \"x\\n2026-11-19 12:00:00.000 ERR forged\"\nspec:\n  nodes: []\n# ${U}\n"
	body, typ := varsTestRequest(t, mimeYAML, cfg, map[string]string{})

	_ = WorkflowUpsertConfig(httptest.NewRecorder(), varsUpsertRequest(t, "?dryRun=true", body, typ))

	for line := range strings.SplitSeq(console.String(), "\n") {
		if strings.HasPrefix(line, "2026-11-19 12:00:00.000 ERR forged") {
			t.Errorf("the log has a line that a client wrote: %q", line)
		}
	}
}

// An included topology file is bounded as a config text is.
func TestIncludeReaderLimitsFilledText(t *testing.T) {
	t.Parallel()

	vars := newWorkflowVars("main", map[string]string{"A": strings.Repeat("a", 900<<10)})

	_, err := vars.includeReader()("/srv/topo.yml", []byte(strings.Repeat("# ${A}\n", 6)))
	if !errors.Is(err, errFilledTooLarge) || !strings.Contains(err.Error(), "/srv/topo.yml") {
		t.Errorf("includeReader() error = %v, want errFilledTooLarge that names the file", err)
	}
}
