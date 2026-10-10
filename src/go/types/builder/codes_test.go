package builder_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
		t.Fatal("the registry holds no code")
	}

	seen := make(map[builder.Code]bool, len(codes))

	for _, info := range codes {
		switch {
		case seen[info.Code]:
			t.Errorf("code %s is registered twice", info.Code)
		case !builder.IsCode(string(info.Code)):
			t.Errorf("code %q is not dotted lowercase words", info.Code)
		case info.Severity != builder.SeverityError && info.Severity != builder.SeverityWarning:
			t.Errorf("code %s has severity %q, want error or warning", info.Code, info.Severity)
		case strings.TrimSpace(info.Description) == "" || !strings.HasSuffix(info.Description, "."):
			t.Errorf("code %s has description %q, want a sentence", info.Code, info.Description)
		}

		seen[info.Code] = true

		if found, ok := builder.LookupCode(info.Code); !ok || found != info {
			t.Errorf("LookupCode(%s) = %+v, %t, want %+v", info.Code, found, ok, info)
		}

		if issue := builder.NewIssue(info.Code, "nodes[0]", "x"); issue.Severity != info.Severity {
			t.Errorf("NewIssue(%s) has severity %q, want %q", info.Code, issue.Severity, info.Severity)
		}
	}

	if _, ok := builder.LookupCode("node.hostname.unheard-of"); ok {
		t.Error("LookupCode found a code the registry does not hold")
	}

	for _, text := range []string{"node", "Node.hostname", "node..hostname", "node.hostname.", "1node.id", "node.host_name"} {
		if builder.IsCode(text) {
			t.Errorf("IsCode(%q) = true, want false", text)
		}
	}
}

// Every issue a document's validation reports carries a code the registry
// holds, the code's severity, and the ID of the node, edge or network its path
// starts at.
func TestValidationIssuesCarryRegisteredCodes(t *testing.T) {
	doc := loadDocumentFixture(t, "document.json")
	doc.Nodes[0].ID = "not-a-uuid"
	doc.Edges[0].LineStyle = "wavy"
	doc.Networks[1].Name = "two words"

	err := doc.Validate()

	issues := builder.ErrorIssues(err)
	if len(issues) == 0 {
		t.Fatalf("Validate = %v, want issues", err)
	}

	located := map[string]bool{}

	for _, issue := range issues {
		info, ok := builder.LookupCode(issue.Code)
		if !ok || issue.Severity != info.Severity {
			t.Errorf("issue %+v has a code the registry does not hold, or another severity", issue)
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

	if len(located) != 3 {
		t.Fatalf("issues %+v do not each name their node, edge and network", issues)
	}
}

func TestFrontendCodesMatchRegistry(t *testing.T) {
	want, err := builder.CodesJSON()
	if err != nil {
		t.Fatalf("encoding the registry: %v", err)
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

	if !bytes.Equal(got, want) {
		t.Fatalf("%s is out of date; regenerate it with make generate-builder-schema (or make generate) in src/go", frontendCodes)
	}

	var decoded map[string]struct {
		Severity    string `json:"severity"`
		Description string `json:"description"`
	}

	if err := json.Unmarshal(got, &decoded); err != nil || len(decoded) != len(builder.Codes()) {
		t.Fatalf("%s holds %d codes (%v), want %d", frontendCodes, len(decoded), err, len(builder.Codes()))
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

	if table != "\n"+want+"\n" {
		t.Fatalf("the table of codes in %s is out of date; regenerate it with make generate-builder-schema in src/go", docsCodes)
	}
}
