package builder_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/types/builder"
)

// frontendCodes is the web UI's copy of the code registry, which its
// validator's codes are checked against. It must match [builder.CodesJSON].
var frontendCodes = filepath.Join( //nolint:gochecknoglobals // test fixture path
	"..", "..", "..", "js", "src", "builder", "schema", "codes.json",
)

// codePattern is the form of a code: dot-separated words, the first only letters.
var codePattern = regexp.MustCompile(`^[a-z]+(\.[a-z0-9-]+)+$`)

// Every code is a dotted lowercase name, held once, with a severity an issue
// has and a sentence that says what the rule is.
func TestCodesAreUniqueDottedAndDescribed(t *testing.T) {
	codes := builder.Codes()
	if len(codes) == 0 {
		t.Fatal("Codes() = [], want the registered codes")
	}

	seen := make(map[builder.Code]bool, len(codes))

	for _, info := range codes {
		switch {
		case seen[info.Code]:
			t.Errorf("Codes() holds code %s twice, want it once", info.Code)
		case !codePattern.MatchString(string(info.Code)):
			t.Errorf("Codes() holds code %q, want dotted lowercase words", info.Code)
		case info.Severity != builder.SeverityError && info.Severity != builder.SeverityWarning:
			t.Errorf("code %s severity = %q, want error or warning", info.Code, info.Severity)
		case strings.TrimSpace(info.Description) == "" || !strings.HasSuffix(info.Description, "."):
			t.Errorf("code %s description = %q, want a sentence", info.Code, info.Description)
		}

		seen[info.Code] = true

		if found, ok := builder.LookupCode(info.Code); !ok || found != info {
			t.Errorf("LookupCode(%s) = %+v, %t, want %+v", info.Code, found, ok, info)
		}

		if issue := builder.NewIssue(info.Code, "nodes[0]", "x"); issue.Severity != info.Severity {
			t.Errorf("NewIssue(%s, \"nodes[0]\", \"x\").Severity = %q, want %q", info.Code, issue.Severity, info.Severity)
		}
	}

	if found, ok := builder.LookupCode("node.hostname.unheard-of"); ok {
		t.Errorf("LookupCode(node.hostname.unheard-of) = %+v, true, want false", found)
	}
}

// Every issue validation reports carries a registered code, that code's severity, and the ID of
// the node, edge or network its path starts at.
func TestValidationIssuesCarryRegisteredCodes(t *testing.T) {
	doc := loadDocumentFixture(t, "document.json")
	doc.Nodes[0].ID = "not-a-uuid"
	doc.Edges[0].LineStyle = "wavy"
	doc.Networks[1].Name = "two words"

	err := doc.Validate()

	issues := builder.ErrorIssues(err)
	if len(issues) == 0 {
		t.Fatalf("Validate() = %v, want issues", err)
	}

	located := map[string]bool{}

	for _, issue := range issues {
		info, ok := builder.LookupCode(issue.Code)
		if !ok || issue.Severity != info.Severity {
			t.Errorf("Validate() issue %+v: LookupCode(%s) = %+v, %t, want the code registered with severity %q",
				issue, issue.Code, info, ok, issue.Severity)
		}

		switch {
		case strings.HasPrefix(issue.Path, "nodes[0]") && issue.NodeID == "not-a-uuid":
			located["node"] = true
		case strings.HasPrefix(issue.Path, "edges[0]") && issue.EdgeID == doc.Edges[0].ID:
			located["edge"] = true
		case strings.HasPrefix(issue.Path, "networks[1]") && issue.NetworkID == doc.Networks[1].ID:
			located["network"] = true
		}
	}

	want := map[string]bool{"node": true, "edge": true, "network": true}
	if diff := cmp.Diff(want, located); diff != "" {
		t.Errorf("Validate() issues %+v located at their node, edge and network mismatch (-want +got):\n%s", issues, diff)
	}
}

func TestFrontendCodesMatchRegistry(t *testing.T) {
	want, err := builder.CodesJSON()
	if err != nil {
		t.Fatalf("CodesJSON(): %v", err)
	}

	if *updateFrontendSchema {
		if err := os.WriteFile(frontendCodes, want, 0o600); err != nil {
			t.Fatalf("writing %s: %v", frontendCodes, err)
		}
	}

	got, err := os.ReadFile(frontendCodes)
	if err != nil {
		t.Fatalf("reading %s: %v", frontendCodes, err)
	}

	if diff := cmp.Diff(string(want), string(got)); diff != "" {
		t.Errorf("%s is out of date; regenerate it with make generate-builder-schema (or make generate) in src/go "+
			"(-want +got):\n%s", frontendCodes, diff)
	}

	var decoded map[string]struct {
		Severity    string `json:"severity"`
		Description string `json:"description"`
	}

	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("decoding %s: %v", frontendCodes, err)
	}

	if len(decoded) != len(builder.Codes()) {
		t.Errorf("%s holds %d codes, want %d", frontendCodes, len(decoded), len(builder.Codes()))
	}
}

func TestCodeDefaultSeverity(t *testing.T) {
	for _, test := range []struct {
		code builder.Code
		want builder.Severity
	}{
		{code: builder.CodeNodeHostnameRequired, want: builder.SeverityError},
		{code: builder.CodeNetworkSwitchMissing, want: builder.SeverityWarning},
		{code: "node.hostname.unheard-of", want: builder.SeverityError},
		{code: "", want: builder.SeverityError},
	} {
		t.Run(string(test.code), func(t *testing.T) {
			if got := test.code.DefaultSeverity(); got != test.want {
				t.Errorf("Code(%q).DefaultSeverity() = %q, want %q", test.code, got, test.want)
			}
		})
	}
}

func TestNewIssue(t *testing.T) {
	want := builder.Issue{
		Code: builder.CodeNetworkSwitchMissing, Severity: builder.SeverityWarning, Path: "networks[2]", Message: "no switch",
	}
	if diff := cmp.Diff(want, builder.NewIssue(builder.CodeNetworkSwitchMissing, "networks[2]", "no switch")); diff != "" {
		t.Errorf("NewIssue(network.switch.missing) mismatch (-want +got):\n%s", diff)
	}

	// A code the registry does not hold is an error.
	want = builder.Issue{Code: "x.unheard-of", Severity: builder.SeverityError, Message: "ñandú"}
	if diff := cmp.Diff(want, builder.NewIssue("x.unheard-of", "", "ñandú")); diff != "" {
		t.Errorf("NewIssue(x.unheard-of) mismatch (-want +got):\n%s", diff)
	}
}

func TestIssueMessages(t *testing.T) {
	issues := []builder.Issue{
		builder.NewIssue(builder.CodeNodeHostnameRequired, "nodes[0]", "first"),
		builder.NewIssue(builder.CodeNetworkSwitchMissing, "networks[0]", ""),
		builder.NewIssue(builder.CodeNodeHostnameRequired, "nodes[1]", "third"),
	}

	if diff := cmp.Diff([]string{"first", "", "third"}, builder.IssueMessages(issues)); diff != "" {
		t.Errorf("IssueMessages() mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff([]string{}, builder.IssueMessages(nil)); diff != "" {
		t.Errorf("IssueMessages(nil) mismatch (-want +got):\n%s", diff)
	}
}

// listedIssuesError is an error that lists its issues, as a publish blocker does.
type listedIssuesError struct {
	issues []builder.Issue
}

func (e listedIssuesError) Error() string { return "blocked" }

func (e listedIssuesError) Issues() []builder.Issue { return e.issues }

func TestErrorIssues(t *testing.T) {
	issues := []builder.Issue{
		builder.NewIssue(builder.CodeNodeHostnameRequired, "nodes[0]", "a device needs a hostname"),
		builder.NewIssue(builder.CodeNetworkSwitchMissing, "networks[1]", "no switch"),
	}

	for _, test := range []struct {
		name string
		err  error
		want []builder.Issue
	}{
		{name: "no error", err: nil, want: nil},
		{name: "an error without issues", err: errors.New("disk full"), want: nil},
		{name: "a validation error", err: &builder.ValidationError{Issues: issues}, want: issues},
		{
			name: "a wrapped validation error",
			err:  fmt.Errorf("saving: %w", &builder.ValidationError{Issues: issues}),
			want: issues,
		},
		{name: "a package error", err: &builder.PackageError{Issues: issues}, want: issues},
		{name: "a template file error", err: fmt.Errorf("reading: %w", &builder.TemplateFileError{Issues: issues}), want: issues},
		{name: "a layer set error", err: &builder.LayerSetError{Issues: issues}, want: issues},
		{name: "an error that lists its issues", err: fmt.Errorf("publishing: %w", listedIssuesError{issues: issues}), want: issues},
		{name: "a validation error without issues", err: &builder.ValidationError{}, want: nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			if diff := cmp.Diff(test.want, builder.ErrorIssues(test.err)); diff != "" {
				t.Errorf("ErrorIssues(%v) mismatch (-want +got):\n%s", test.err, diff)
			}
		})
	}

	// The issues are a copy: a change to them does not change the error.
	invalid := &builder.ValidationError{Issues: []builder.Issue{issues[0]}}
	builder.ErrorIssues(invalid)[0].Message = "changed"

	if got := invalid.Issues[0].Message; got != issues[0].Message {
		t.Errorf("ValidationError issue message after a change to ErrorIssues() = %q, want %q", got, issues[0].Message)
	}
}
