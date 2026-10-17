package builder_test

import (
	"encoding/json"
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

// docsCodes is the docs page whose table lists every code.
var docsCodes = filepath.Join( //nolint:gochecknoglobals // test fixture path
	"..", "..", "..", "..", "docs", "content", "builder", "error-codes.md",
)

// codePattern is the form of a code: dot-separated words, the first only letters.
var codePattern = regexp.MustCompile(`^[a-z]+(\.[a-z0-9-]+)+$`)

// The markers around the table of codes in [docsCodes].
const (
	codeTableStart = "<!-- codes:start -->"
	codeTableEnd   = "<!-- codes:end -->"
)

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

// codeTable is the docs' table of every code, in the order of the registry.
func codeTable() string {
	var table strings.Builder

	table.WriteString("| Code | Severity | Rule |\n|---|---|---|\n")

	for _, info := range builder.Codes() {
		table.WriteString("| `" + string(info.Code) + "` | " + string(info.Severity) + " | " + info.Description + " |\n")
	}

	return table.String()
}

// The docs list every code, as the registry has them, between the table's
// markers.
func TestDocsCodeTableMatchesRegistry(t *testing.T) {
	page, err := os.ReadFile(docsCodes)
	if err != nil {
		t.Fatalf("reading %s: %v", docsCodes, err)
	}

	text := string(page)

	before, rest, found := strings.Cut(text, codeTableStart+"\n")
	if !found {
		t.Fatalf("%s has no %s line", docsCodes, codeTableStart)
	}

	table, after, found := strings.Cut(rest, codeTableEnd)
	if !found {
		t.Fatalf("%s has no %s after %s", docsCodes, codeTableEnd, codeTableStart)
	}

	want := codeTable()

	if *updateFrontendSchema && table != "\n"+want+"\n" {
		updated := before + codeTableStart + "\n\n" + want + "\n" + codeTableEnd + after
		if err := os.WriteFile(docsCodes, []byte(updated), 0o600); err != nil {
			t.Fatalf("writing %s: %v", docsCodes, err)
		}

		table = "\n" + want + "\n"
	}

	if diff := cmp.Diff("\n"+want+"\n", table); diff != "" {
		t.Errorf("the table of codes in %s is out of date; regenerate it with make generate-builder-schema in src/go "+
			"(-want +got):\n%s", docsCodes, diff)
	}
}
