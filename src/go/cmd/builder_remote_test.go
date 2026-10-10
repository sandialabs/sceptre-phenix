package cmd

import (
	"bytes"
	"cmp"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	bdoc "phenix/types/builder"
	"phenix/util/common"
)

// builderTestToken is the API token the tests send. No output may hold it.
const builderTestToken = "s3cret-token"

// builderTestDrafts is an answer of GET /builder/drafts: two drafts of
// alice, the caller, and one of bob shared with her.
const builderTestDrafts = `{
	"drafts": [
		{"id": "riverside", "owner": "alice", "title": "Riverside", "created": "2026-10-01T09:00:00Z",
		 "updated": "2026-10-02T10:30:00Z", "lastModifiedBy": "alice", "cursor": 1, "snapshots": 2,
		 "size": 512, "historyBytes": 900, "dirty": true, "canUndo": true, "canRedo": false,
		 "readOnly": false, "etag": "\"4\"", "access": "owner", "canShare": true},
		{"id": "annex", "owner": "alice", "created": "2026-10-03T07:00:00Z", "updated": "2026-10-03T08:00:00Z",
		 "lastModifiedBy": "alice", "cursor": 0, "snapshots": 1, "size": 128, "historyBytes": 128,
		 "dirty": false, "canUndo": false, "canRedo": false, "readOnly": false, "etag": "\"1\"", "access": "owner"}
	],
	"shared": [
		{"id": "pumps", "owner": "bob", "title": "Pump station", "created": "2026-09-30T09:00:00Z",
		 "updated": "2026-10-04T12:00:00Z", "lastModifiedBy": "carol", "cursor": 0, "snapshots": 1,
		 "size": 256, "historyBytes": 256, "dirty": false, "canUndo": false, "canRedo": false,
		 "readOnly": false, "etag": "\"2\"", "access": "edit", "via": "share"}
	],
	"damaged": []
}`

// builderTestDraftsJSON is what drafts list -o json writes for
// builderTestDrafts.
const builderTestDraftsJSON = `{
  "drafts": [
    {
      "owner": "alice",
      "id": "annex",
      "updatedAt": "2026-10-03T08:00:00Z",
      "updatedBy": "alice",
      "access": "owner"
    },
    {
      "owner": "alice",
      "id": "riverside",
      "name": "Riverside",
      "updatedAt": "2026-10-02T10:30:00Z",
      "updatedBy": "alice",
      "access": "owner"
    }
  ]
}
`

// builderTestDocument is the current document of the draft alice/riverside.
const builderTestDocument = `{"$schema":"https://phenix.sandia.gov/schemas/builder/v1","revision":1,` +
	`"metadata":{"id":"riverside-doc","name":"Riverside","description":"Pumps and valves"},` +
	`"nodes":[],"networks":[],"edges":[],"viewport":{"x":0,"y":0,"zoom":1}}`

// builderTestDraft is the answer of GET /builder/drafts/alice/riverside.
const builderTestDraft = `{"id": "riverside", "owner": "alice", "title": "Riverside", "etag": "\"4\"", ` +
	`"access": "owner", "history": [], "document": ` + builderTestDocument + `}`

// The routes the tests answer.
const (
	routeDrafts    = "GET /api/v1/builder/drafts"
	routeDraft     = "GET /api/v1/builder/drafts/alice/riverside"
	routeExport    = "POST /api/v1/builder/export/topology"
	routePackage   = "POST /api/v1/builder/package"
	routePreflight = "POST /api/v1/builder/drafts/alice/riverside/preflight"
	routeTemplates = "GET /api/v1/builder/templates"
	routeIcons     = "GET /api/v1/builder/icons"
	routeAddIcon   = "POST /api/v1/builder/icons"
	routeAddItems  = "POST /api/v1/builder/templates/alice/items"
)

// builderFakeAnswer is how the fake answers a route: with the status (200
// when 0) and the body, when its request body holds when.
type builderFakeAnswer struct {
	status int
	body   string
	when   string
}

// builderFakeRequest is one request the fake received.
type builderFakeRequest struct {
	method string
	path   string
	token  string
	body   string
}

// builderFake stands in for a phenix server over HTTP, answering the routes
// it was given and recording every request. A route it was not given is a
// 404.
type builderFake struct {
	url string

	mu       sync.Mutex
	requests []builderFakeRequest
}

// newBuilderFake starts a fake that answers with answers, by "METHOD path",
// and stops it when the test ends.
func newBuilderFake(t *testing.T, answers map[string][]builderFakeAnswer) *builderFake {
	t.Helper()

	fake := &builderFake{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		body := string(data)
		route := r.Method + " " + r.URL.EscapedPath()

		fake.mu.Lock()
		fake.requests = append(fake.requests, builderFakeRequest{
			method: r.Method, path: r.URL.EscapedPath(), token: r.Header.Get(builderTokenHeader), body: body,
		})
		fake.mu.Unlock()

		answer := builderFakeAnswer{status: http.StatusNotFound, body: `{"message":"no route ` + route + `"}`}

		for _, candidate := range answers[route] {
			if strings.Contains(body, candidate.when) {
				answer = candidate

				break
			}
		}

		w.Header().Set("Content-Type", mimeJSON)
		w.WriteHeader(cmp.Or(answer.status, http.StatusOK))
		_, _ = io.WriteString(w, answer.body)
	}))

	t.Cleanup(server.Close)

	fake.url = server.URL

	return fake
}

// recorded returns the requests received so far.
func (f *builderFake) recorded() []builderFakeRequest {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.requests)
}

// builderOK is the answers of one route: body, with 200.
func builderOK(body string) []builderFakeAnswer {
	return []builderFakeAnswer{{status: http.StatusOK, body: body}}
}

// remote returns args with the fake's URL and the test token.
func (f *builderFake) remote(args ...string) []string {
	return append(args, "--url", f.url, "--token", builderTestToken)
}

// runBuilderRemote runs `phenix builder` with args, and returns what it
// wrote to standard output and to standard error.
func runBuilderRemote(args ...string) (string, string, error) {
	root := &cobra.Command{Use: "phenix", SilenceUsage: true, SilenceErrors: true}
	builderCmd := newBuilderCmd()
	builderCmd.AddCommand(newBuilderDraftsCmd(), newBuilderTemplatesCmd())
	root.AddCommand(builderCmd)
	root.SetArgs(append([]string{"builder"}, args...))

	var stdout, stderr bytes.Buffer

	root.SetOut(&stdout)
	root.SetErr(&stderr)

	_, err := root.ExecuteC()

	return stdout.String(), stderr.String(), err
}

// wantExit fails the test unless err ends phenix with code; 0 is no error.
func wantExit(t *testing.T, err error, code int) {
	t.Helper()

	switch {
	case code == 0 && err != nil:
		t.Fatalf("error = %v, want none", err)
	case code != 0 && err == nil:
		t.Fatalf("no error, want one that exits %d", code)
	case code != 0 && exitCode(err) != code:
		t.Fatalf("exit status of %v = %d, want %d", err, exitCode(err), code)
	}
}

// builderTestIcon returns a PNG an icon library and a template file accept,
// as base64.
func builderTestIcon(t *testing.T) string {
	t.Helper()

	var buffer bytes.Buffer

	if err := png.Encode(&buffer, image.NewNRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatalf("encoding the icon: %v", err)
	}

	return base64.StdEncoding.EncodeToString(buffer.Bytes())
}

// sameDraftsAsJSON fails the test unless the YAML text holds the values of
// builderTestDraftsJSON.
func sameDraftsAsJSON(t *testing.T, yamlText string) {
	t.Helper()

	var fromYAML, fromJSON any

	if err := yaml.Unmarshal([]byte(yamlText), &fromYAML); err != nil {
		t.Fatalf("decoding the YAML: %v\n%s", err, yamlText)
	}

	// Through JSON, so numbers are alike.
	reencoded, err := json.Marshal(fromYAML)
	if err != nil {
		t.Fatalf("encoding the YAML's values: %v", err)
	}

	if err := json.Unmarshal(reencoded, &fromYAML); err != nil {
		t.Fatalf("decoding the YAML's values: %v", err)
	}

	if err := json.Unmarshal([]byte(builderTestDraftsJSON), &fromJSON); err != nil {
		t.Fatalf("decoding the JSON: %v", err)
	}

	if !reflect.DeepEqual(fromYAML, fromJSON) {
		t.Errorf("the YAML holds %v, want %v", fromYAML, fromJSON)
	}
}

func TestBuilderDraftsListJSON(t *testing.T) {
	t.Parallel()

	fake := newBuilderFake(t, map[string][]builderFakeAnswer{routeDrafts: builderOK(builderTestDrafts)})

	stdout, stderr, err := runBuilderRemote(fake.remote("drafts", "list", "-o", FormatJSON)...)
	wantExit(t, err, 0)

	if stdout != builderTestDraftsJSON {
		t.Errorf("drafts list -o json wrote\n%s\nwant\n%s", stdout, builderTestDraftsJSON)
	}

	requests := fake.recorded()
	if len(requests) != 1 || requests[0].method != http.MethodGet || requests[0].token != "Bearer "+builderTestToken {
		t.Errorf("requests = %+v, want one GET with the token as Bearer", requests)
	}

	if strings.Contains(stdout+stderr, builderTestToken) {
		t.Error("the output holds the token")
	}

	// The fake answers on a loopback address, where http keeps the token on
	// this host: no warning.
	if stderr != "" {
		t.Errorf("drafts list over loopback http wrote %q to standard error, want nothing", stderr)
	}
}

func TestBuilderDraftsListSharedAndOwner(t *testing.T) {
	t.Parallel()

	fake := newBuilderFake(t, map[string][]builderFakeAnswer{routeDrafts: builderOK(builderTestDrafts)})

	tests := []struct {
		args []string
		want []string
	}{
		{args: []string{"--shared"}, want: []string{"alice/annex", "alice/riverside", "bob/pumps"}},
		{args: []string{"--owner", "bob"}, want: []string{"bob/pumps"}},
		{args: []string{"--owner", "alice", "--shared"}, want: []string{"alice/annex", "alice/riverside"}},
	}

	for _, test := range tests {
		stdout, _, err := runBuilderRemote(fake.remote(append([]string{"drafts", "list", "-o", FormatJSON}, test.args...)...)...)
		wantExit(t, err, 0)

		var list builderDraftList

		if err := json.Unmarshal([]byte(stdout), &list); err != nil {
			t.Fatalf("%v: decoding %s: %v", test.args, stdout, err)
		}

		got := make([]string, 0, len(list.Drafts))
		for _, draft := range list.Drafts {
			got = append(got, draft.Owner+"/"+draft.ID)
		}

		if !slices.Equal(got, test.want) {
			t.Errorf("%v listed %v, want %v", test.args, got, test.want)
		}
	}
}

func TestBuilderDraftsListTableAndYAML(t *testing.T) {
	t.Parallel()

	fake := newBuilderFake(t, map[string][]builderFakeAnswer{routeDrafts: builderOK(builderTestDrafts)})

	stdout, _, err := runBuilderRemote(fake.remote("drafts", "list", "--shared")...)
	wantExit(t, err, 0)

	lines := strings.Split(stdout, "\n")

	for _, want := range [][]string{
		{"OWNER", "DRAFT", "NAME", "UPDATED", "UPDATED BY", "ACCESS"},
		{"alice", "riverside", "Riverside", "2026-10-02T10:30:00Z", "owner"},
		{"bob", "pumps", "Pump station", "carol", "edit"},
	} {
		if !slices.ContainsFunc(lines, func(line string) bool {
			return !slices.ContainsFunc(want, func(cell string) bool { return !strings.Contains(line, cell) })
		}) {
			t.Errorf("no line of the table holds %q:\n%s", want, stdout)
		}
	}

	stdout, _, err = runBuilderRemote(fake.remote("drafts", "list", "-o", FormatYAML)...)
	wantExit(t, err, 0)

	if !strings.HasPrefix(stdout, "drafts:\n") {
		t.Errorf("drafts list -o yaml wrote\n%s\nwant the drafts key first", stdout)
	}

	sameDraftsAsJSON(t, stdout)
}

func TestBuilderDraftsExport(t *testing.T) {
	t.Parallel()

	const (
		wantJSON = `{
  "$schema": "https://phenix.sandia.gov/schemas/builder/v1",
  "revision": 1,
  "metadata": {
    "id": "riverside-doc",
    "name": "Riverside",
    "description": "Pumps and valves"
  },
  "nodes": [],
  "networks": [],
  "edges": [],
  "viewport": {
    "x": 0,
    "y": 0,
    "zoom": 1
  }
}
`
		wantYAML = `$schema: https://phenix.sandia.gov/schemas/builder/v1
revision: 1
metadata:
  id: riverside-doc
  name: Riverside
  description: Pumps and valves
nodes: []
networks: []
edges: []
viewport:
  x: 0
  y: 0
  zoom: 1
`
	)

	fake := newBuilderFake(t, map[string][]builderFakeAnswer{routeDraft: builderOK(builderTestDraft)})

	stdout, _, err := runBuilderRemote(fake.remote("drafts", "export", "alice/riverside")...)
	wantExit(t, err, 0)

	if stdout != wantJSON {
		t.Errorf("drafts export wrote\n%s\nwant\n%s", stdout, wantJSON)
	}

	stdout, _, err = runBuilderRemote(fake.remote("drafts", "export", "alice/riverside", "--format", FormatYAML)...)
	wantExit(t, err, 0)

	if stdout != wantYAML {
		t.Errorf("drafts export --format yaml wrote\n%s\nwant\n%s", stdout, wantYAML)
	}

	output := filepath.Join(t.TempDir(), "riverside.builder.json")

	stdout, _, err = runBuilderRemote(fake.remote("drafts", "export", "alice/riverside", "--output", output)...)
	wantExit(t, err, 0)

	if written, err := os.ReadFile(output); err != nil || string(written) != wantJSON || stdout != "" {
		t.Errorf(
			"drafts export --output wrote %q (err = %v) and %q to standard output, want the JSON in the file only",
			written, err, stdout,
		)
	}
}

func TestBuilderDraftsExportPackage(t *testing.T) {
	t.Parallel()

	// The route answers the package and its warnings; the command writes the
	// package alone, and the warnings on standard error.
	const (
		pack    = `{"$schema":"https://phenix.sandia.gov/schemas/builder/package/v1","document":` + builderTestDocument + `}`
		warning = `Custom icon plc-icon is not in the server's icon library: the package names it but does not carry it.`
		answer  = `{"package":` + pack + `,"warnings":[{"code":"package.icon.missing","severity":"warning",` +
			`"message":"` + warning + `"}]}`
	)

	fake := newBuilderFake(t, map[string][]builderFakeAnswer{
		routeDraft:   builderOK(builderTestDraft),
		routePackage: builderOK(answer),
	})

	stdout, stderr, err := runBuilderRemote(
		fake.remote("drafts", "export", "alice/riverside", "--package", "--include", "scenarios,icons")...,
	)
	wantExit(t, err, 0)

	var indented bytes.Buffer
	if err := json.Indent(&indented, []byte(pack), "", "  "); err != nil {
		t.Fatalf("indenting the package: %v", err)
	}

	if want := indented.String() + "\n"; stdout != want {
		t.Errorf("drafts export --package wrote\n%s\nwant\n%s", stdout, want)
	}

	if want := "warning: [package.icon.missing] " + warning + "\n"; stderr != want {
		t.Errorf("drafts export --package wrote %q to standard error, want %q", stderr, want)
	}

	requests := fake.recorded()
	want := `{"document":` + builderTestDocument + `,"include":["scenarios","icons"]}`

	if len(requests) != 2 || requests[1].path != "/api/v1/builder/package" || requests[1].body != want {
		t.Errorf("requests = %+v, want the package request %s", requests, want)
	}

	for _, args := range [][]string{
		{"drafts", "export", "alice/riverside", "--include", "icons"},
		{"drafts", "export", "alice/riverside", "--package", "--include", "configs"},
		{"drafts", "export", "alice/riverside", "--format", "xml"},
		{"drafts", "export", "riverside"},
	} {
		_, _, err := runBuilderRemote(fake.remote(args...)...)
		wantExit(t, err, exitRefused)
	}

	if requests := fake.recorded(); len(requests) != 2 {
		t.Errorf("requests = %+v, want none more for refused arguments", requests)
	}

	// An answer without a package is refused, and nothing is written.
	empty := newBuilderFake(t, map[string][]builderFakeAnswer{
		routeDraft:   builderOK(builderTestDraft),
		routePackage: builderOK(`{"package": null, "warnings": []}`),
	})

	stdout, _, err = runBuilderRemote(empty.remote("drafts", "export", "alice/riverside", "--package")...)
	wantExit(t, err, exitRefused)

	if stdout != "" || !strings.Contains(err.Error(), "no package") {
		t.Errorf("an answer without a package: wrote %q, error = %v; want nothing written and the error", stdout, err)
	}
}

// TestBuilderDraftsExportPackageMatchesRoute runs drafts export --package
// against the package route's recorded answer: the request and the answer
// that TestBuilderPackageAnswersAsRecorded in phenix/web checks the route
// against. The command must send the recorded request, write the package
// alone, as JSON or YAML, and print each warning with its code on standard
// error.
func TestBuilderDraftsExportPackageMatchesRoute(t *testing.T) {
	t.Parallel()

	path := filepath.Join("..", "web", "testdata", "builder-package-exchange.json")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	var exchange struct {
		Request json.RawMessage `json:"request"`
		Answer  json.RawMessage `json:"answer"`
	}

	if err := json.Unmarshal(data, &exchange); err != nil {
		t.Fatalf("decoding %s: %v", path, err)
	}

	var (
		request struct {
			Document json.RawMessage `json:"document"`
			Include  []string        `json:"include"`
		}
		answer struct {
			Package  json.RawMessage `json:"package"`
			Warnings []builderIssue  `json:"warnings"`
		}
	)

	if err := json.Unmarshal(exchange.Request, &request); err != nil {
		t.Fatalf("decoding the recorded request: %v", err)
	}

	if err := json.Unmarshal(exchange.Answer, &answer); err != nil {
		t.Fatalf("decoding the recorded answer: %v", err)
	}

	if len(answer.Warnings) == 0 {
		t.Fatalf("the answer recorded in %s has no warning", path)
	}

	var compact bytes.Buffer
	if err := json.Compact(&compact, exchange.Request); err != nil {
		t.Fatalf("compacting the recorded request: %v", err)
	}

	draft := `{"id": "riverside", "owner": "alice", "title": "Riverside", "etag": "\"4\"", "access": "owner", ` +
		`"history": [], "document": ` + string(request.Document) + `}`

	fake := newBuilderFake(t, map[string][]builderFakeAnswer{
		routeDraft:   builderOK(draft),
		routePackage: builderOK(string(exchange.Answer)),
	})

	args := []string{"drafts", "export", "alice/riverside", "--package", "--include", strings.Join(request.Include, ",")}

	stdout, stderr, err := runBuilderRemote(fake.remote(args...)...)
	wantExit(t, err, 0)

	if requests := fake.recorded(); len(requests) != 2 || requests[1].body != compact.String() {
		t.Errorf("requests = %+v, want the recorded request %s", requests, compact.String())
	}

	var indented bytes.Buffer
	if err := json.Indent(&indented, answer.Package, "", "  "); err != nil {
		t.Fatalf("indenting the recorded package: %v", err)
	}

	if want := indented.String() + "\n"; stdout != want {
		t.Errorf("drafts export --package wrote\n%s\nwant the recorded package alone\n%s", stdout, want)
	}

	for _, warning := range answer.Warnings {
		if want := "warning: [" + warning.Code + "] " + warning.Message + "\n"; !strings.Contains(stderr, want) {
			t.Errorf("standard error = %q, want the recorded warning %q", stderr, want)
		}
	}

	stdout, _, err = runBuilderRemote(fake.remote(append(args, "--format", FormatYAML)...)...)
	wantExit(t, err, 0)

	wantYAML, err := builderYAML(answer.Package)
	if err != nil {
		t.Fatalf("converting the recorded package to YAML: %v", err)
	}

	if stdout != string(wantYAML) || !strings.HasPrefix(stdout, "$schema: "+bdoc.PackageSchemaURI+"\n") {
		t.Errorf("drafts export --package --format yaml wrote\n%s\nwant the recorded package alone\n%s", stdout, wantYAML)
	}

	if pack, err := bdoc.ParsePackage([]byte(stdout)); err != nil || pack.Schema != bdoc.PackageSchemaURI {
		t.Errorf("the YAML package does not load: %v", err)
	}
}

func TestBuilderDraftsValidate(t *testing.T) {
	t.Parallel()

	const (
		blocked = `{"name": "Riverside", "yaml": "apiVersion: phenix.sandia.gov/v1\n",
			"warnings": ["hostname Phenix may clash"],
			"publishBlockers": [
				"interface eth0 of device web has no VLAN",
				{"code": "address-shared", "severity": "error", "message": "IP address 10.0.0.5 is used twice",
				 "nodeId": "n-web", "field": "spec.network.interfaces.0.address"}
			]}`
		wantJSON = `{
  "draft": {
    "owner": "alice",
    "id": "riverside",
    "name": "Riverside"
  },
  "valid": false,
  "errors": [
    {
      "severity": "error",
      "message": "interface eth0 of device web has no VLAN"
    },
    {
      "code": "address-shared",
      "severity": "error",
      "message": "IP address 10.0.0.5 is used twice",
      "nodeId": "n-web",
      "field": "spec.network.interfaces.0.address"
    }
  ],
  "warnings": [
    {
      "severity": "warning",
      "message": "hostname Phenix may clash"
    }
  ]
}
`
	)

	fake := newBuilderFake(t, map[string][]builderFakeAnswer{
		routeDraft:  builderOK(builderTestDraft),
		routeExport: builderOK(blocked),
	})

	stdout, _, err := runBuilderRemote(fake.remote("drafts", "validate", "alice/riverside", "-o", FormatJSON)...)
	wantExit(t, err, exitFindings)

	if stdout != wantJSON {
		t.Errorf("drafts validate -o json wrote\n%s\nwant\n%s", stdout, wantJSON)
	}

	if want := "draft alice/riverside has 2 errors that block publishing"; err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}

	requests := fake.recorded()
	if len(requests) != 2 || requests[1].body != `{"document":`+builderTestDocument+`}` {
		t.Errorf("requests = %+v, want the document posted to export/topology", requests)
	}

	stdout, _, err = runBuilderRemote(fake.remote("drafts", "validate", "alice/riverside")...)
	wantExit(t, err, exitFindings)

	for _, want := range []string{
		"Draft alice/riverside (Riverside) cannot be published: 2 errors, 1 warning.",
		"SEVERITY", "address-shared", "node n-web", "hostname Phenix may clash",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the table lacks %q:\n%s", want, stdout)
		}
	}

	const wantYAML = `draft:
  owner: alice
  id: riverside
  name: Riverside
valid: false
errors:
  - severity: error
    message: interface eth0 of device web has no VLAN
  - code: address-shared
    severity: error
    message: IP address 10.0.0.5 is used twice
    nodeId: n-web
    field: spec.network.interfaces.0.address
warnings:
  - severity: warning
    message: hostname Phenix may clash
`

	stdout, _, err = runBuilderRemote(fake.remote("drafts", "validate", "alice/riverside", "-o", FormatYAML)...)
	wantExit(t, err, exitFindings)

	if stdout != wantYAML {
		t.Errorf("drafts validate -o yaml wrote\n%s\nwant\n%s", stdout, wantYAML)
	}
}

// TestBuilderDraftsValidateRefusalIssues checks that the issues of a 422
// refusal are the report's errors and warnings, by their severity.
func TestBuilderDraftsValidateRefusalIssues(t *testing.T) {
	t.Parallel()

	fake := newBuilderFake(t, map[string][]builderFakeAnswer{
		routeDraft: builderOK(builderTestDraft),
		routeExport: {{
			status: http.StatusUnprocessableEntity,
			body: `{"message": "builder document is not valid", "cause": "1 error", "code": "document-invalid",
				"issues": [
					{"code": "hostname-short", "severity": "error", "message": "hostname w must be at least 2 characters",
					 "nodeId": "n-w", "field": "spec.general.hostname"},
					{"code": "alias-dropped", "severity": "warning", "message": "the VLAN alias is not published",
					 "networkId": "net-exp"}
				]}`,
		}},
	})

	stdout, _, err := runBuilderRemote(fake.remote("drafts", "validate", "alice/riverside", "-o", FormatJSON)...)
	wantExit(t, err, exitFindings)

	// One error: the verb agrees with it.
	if want := "draft alice/riverside has 1 error that blocks publishing"; err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}

	var report builderValidation

	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("decoding %s: %v", stdout, err)
	}

	wantErrors := []builderIssue{{
		Code: "hostname-short", Severity: builderSeverityError, Message: "hostname w must be at least 2 characters",
		NodeID: "n-w", Field: "spec.general.hostname",
	}}
	wantWarnings := []builderIssue{{
		Code: "alias-dropped", Severity: builderSeverityWarning, Message: "the VLAN alias is not published",
		NetworkID: "net-exp",
	}}

	if report.Valid || !reflect.DeepEqual(report.Errors, wantErrors) || !reflect.DeepEqual(report.Warnings, wantWarnings) {
		t.Errorf("report = %+v, want errors %+v and warnings %+v", report, wantErrors, wantWarnings)
	}
}

func TestBuilderDraftsValidateValidAndRefused(t *testing.T) {
	t.Parallel()

	const wantValid = `{
  "draft": {
    "owner": "alice",
    "id": "riverside",
    "name": "Riverside"
  },
  "valid": true,
  "errors": [],
  "warnings": []
}
`

	valid := newBuilderFake(t, map[string][]builderFakeAnswer{
		routeDraft:  builderOK(builderTestDraft),
		routeExport: builderOK(`{"name": "Riverside", "yaml": "", "warnings": [], "publishBlockers": []}`),
	})

	stdout, _, err := runBuilderRemote(valid.remote("drafts", "validate", "alice/riverside", "-o", FormatJSON)...)
	wantExit(t, err, 0)

	if stdout != wantValid {
		t.Errorf("drafts validate -o json wrote\n%s\nwant\n%s", stdout, wantValid)
	}

	stdout, _, err = runBuilderRemote(valid.remote("drafts", "validate", "alice/riverside")...)
	wantExit(t, err, 0)

	if want := "Draft alice/riverside (Riverside) can be published: 0 errors, 0 warnings.\n"; stdout != want {
		t.Errorf("drafts validate wrote %q, want %q", stdout, want)
	}

	// A document the route refuses is not valid: the refusal is its error.
	// The publish refusal gives a general message, and the reason in its
	// cause.
	refusedDocument := newBuilderFake(t, map[string][]builderFakeAnswer{
		routeDraft: builderOK(builderTestDraft),
		routeExport: {{
			status: http.StatusUnprocessableEntity,
			body: `{"message": "builder document cannot be published as topology riverside", ` +
				`"cause": "Error at \"/mac\": string doesn't match the regular expression"}`,
		}},
	})

	stdout, _, err = runBuilderRemote(refusedDocument.remote("drafts", "validate", "alice/riverside", "-o", FormatJSON)...)
	wantExit(t, err, exitFindings)

	var report builderValidation

	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("decoding %s: %v", stdout, err)
	}

	want := `builder document cannot be published as topology riverside: ` +
		`Error at "/mac": string doesn't match the regular expression`
	if report.Valid || len(report.Errors) != 1 || report.Errors[0].Message != want || len(report.Warnings) != 0 {
		t.Errorf("report = %+v, want the refusal's message and cause as its one error", report)
	}

	// A draft the server does not find is a refused request.
	missing := newBuilderFake(t, nil)

	_, _, err = runBuilderRemote(missing.remote("drafts", "validate", "alice/riverside")...)
	wantExit(t, err, exitRefused)
}

func TestBuilderAuthenticationRefused(t *testing.T) {
	t.Parallel()

	fake := newBuilderFake(t, map[string][]builderFakeAnswer{
		routeDrafts: {{status: http.StatusUnauthorized, body: "token is expired\n"}},
	})

	stdout, stderr, err := runBuilderRemote(fake.remote("drafts", "list")...)
	wantExit(t, err, exitRefused)

	message := err.Error()

	if !strings.Contains(message, "401 Unauthorized: token is expired") || !strings.Contains(message, "--token") {
		t.Errorf("error = %q, want the server's refusal and what to do", message)
	}

	if strings.Contains(stdout+stderr+message, builderTestToken) {
		t.Error("the output or the error holds the token")
	}

	// Without a token, a server with sign-in on answers 403.
	forbidden := newBuilderFake(t, map[string][]builderFakeAnswer{
		routeDrafts: {{status: http.StatusForbidden, body: "Forbidden\n"}},
	})

	_, _, err = runBuilderRemote("drafts", "list", "--url", forbidden.url)
	wantExit(t, err, exitRefused)

	if requests := forbidden.recorded(); len(requests) != 1 || requests[0].token != "" {
		t.Errorf("requests = %+v, want one without a token", requests)
	}

	if !strings.Contains(err.Error(), "No token was sent") {
		t.Errorf("error = %q, want it to say no token was sent", err)
	}
}

func TestBuilderServerURLRefused(t *testing.T) {
	t.Parallel()

	for _, server := range []string{"ftp://phenix.example", "phenix.example", "https://alice:pa55word@phenix.example"} {
		_, _, err := runBuilderRemote("drafts", "list", "--url", server)
		wantExit(t, err, exitRefused)

		if strings.Contains(err.Error(), "pa55word") {
			t.Errorf("error = %q holds the password", err)
		}
	}

	_, _, err := runBuilderRemote("drafts", "list", "--url", "https://phenix.example", "-o", "csv")
	wantExit(t, err, exitRefused)

	_, _, err = runBuilderRemote("drafts", "list", "--no-such-flag")
	wantExit(t, err, exitRefused)
}

// TestBuilderRedirectRefused checks that a redirect is refused, not
// followed: the server it names gets no request, so neither the token nor a
// POST replayed as a GET.
func TestBuilderRedirectRefused(t *testing.T) {
	t.Parallel()

	elsewhere := newBuilderFake(t, map[string][]builderFakeAnswer{
		routeDrafts: builderOK(builderTestDrafts),
		routeDraft:  builderOK(builderTestDraft),
		routeExport: builderOK(`{"name": "Riverside", "yaml": "", "warnings": [], "publishBlockers": []}`),
	})

	// It answers the draft itself, and redirects every other request:
	// a GET with 302, a POST with 307.
	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method+" "+r.URL.EscapedPath() == routeDraft {
			w.Header().Set("Content-Type", mimeJSON)
			_, _ = io.WriteString(w, builderTestDraft)

			return
		}

		status := http.StatusFound
		if r.Method == http.MethodPost {
			status = http.StatusTemporaryRedirect
		}

		http.Redirect(w, r, elsewhere.url+r.URL.Path, status)
	}))
	t.Cleanup(redirecting.Close)

	for _, test := range []struct {
		args   []string
		status string
		path   string
	}{
		{args: []string{"drafts", "list"}, status: "302 Found", path: "/api/v1/builder/drafts"},
		{
			args:   []string{"drafts", "validate", "alice/riverside"},
			status: "307 Temporary Redirect",
			path:   "/api/v1/builder/export/topology",
		},
	} {
		stdout, stderr, err := runBuilderRemote(append(test.args, "--url", redirecting.URL, "--token", builderTestToken)...)
		wantExit(t, err, exitRefused)

		message := err.Error()

		if !strings.Contains(message, test.status) || !strings.Contains(message, "a redirect to "+elsewhere.url+test.path) {
			t.Errorf("%v: error = %q, want the redirect and where it points", test.args, message)
		}

		if strings.Contains(stdout+stderr+message, builderTestToken) {
			t.Errorf("%v: the output or the error holds the token", test.args)
		}
	}

	if requests := elsewhere.recorded(); len(requests) != 0 {
		t.Errorf("the server a redirect names got %+v, want no request", requests)
	}
}

// TestBuilderErrorBodyNotJSON checks that of an error answer that is not
// JSON, such as the page of a proxy, only its first line is shown, cut short.
func TestBuilderErrorBodyNotJSON(t *testing.T) {
	t.Parallel()

	page := "<!DOCTYPE html><title>502 Bad Gateway</title>" + strings.Repeat("x", 4*builderMaxErrorText) +
		"\n<p>second line</p>\n" + strings.Repeat("filler\n", builderMaxErrorBytes)

	fake := newBuilderFake(t, map[string][]builderFakeAnswer{
		routeDrafts: {{status: http.StatusBadGateway, body: page}},
	})

	_, _, err := runBuilderRemote(fake.remote("drafts", "list")...)
	wantExit(t, err, exitRefused)

	message := err.Error()

	if !strings.HasPrefix(message, "phenix server answered 502 Bad Gateway: <!DOCTYPE html><title>502 Bad Gateway</title>xxx") ||
		!strings.HasSuffix(message, "x ...") || strings.Contains(message, "second line") ||
		len(message) > 2*builderMaxErrorText {
		t.Errorf("error = %q (%d bytes), want the start of the first line only", message, len(message))
	}

	for _, test := range []struct {
		body string
		want string
	}{
		{body: "token is expired\n", want: "token is expired"},
		{body: "\n  Bad Gateway  \r\n<hr>\n", want: "Bad Gateway ..."},
		{body: "", want: ""},
		{body: strings.Repeat("é", builderMaxErrorText), want: strings.Repeat("é", builderMaxErrorText/2) + " ..."},
	} {
		if got := builderErrorText([]byte(test.body)); got != test.want {
			t.Errorf("builderErrorText(%q) = %q, want %q", test.body, got, test.want)
		}
	}
}

// TestBuilderUnknownSubcommand checks that a command that only groups
// subcommands refuses one it does not have, and prints its help without one.
func TestBuilderUnknownSubcommand(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{{"drafts", "validatex", "x"}, {"templates", "listx"}} {
		stdout, _, err := runBuilderRemote(args...)
		wantExit(t, err, exitRefused)

		if want := `unknown command "` + args[1] + `" for "phenix builder ` + args[0] + `"`; !strings.HasPrefix(err.Error(), want) {
			t.Errorf("%v: error = %q, want it to start %q", args, err, want)
		}

		if stdout != "" {
			t.Errorf("%v wrote %q to standard output, want nothing", args, stdout)
		}
	}

	_, _, err := runBuilderRemote("drafts", "validatex", "x")
	if err == nil || !strings.Contains(err.Error(), "Did you mean this?\n\tvalidate") {
		t.Errorf("error = %v, want it to suggest validate", err)
	}

	stdout, _, err := runBuilderRemote("drafts")
	wantExit(t, err, 0)

	if !strings.Contains(stdout, "Available Commands:") || !strings.Contains(stdout, "validate") {
		t.Errorf("drafts with no subcommand wrote %q, want its help", stdout)
	}
}

// TestBuilderServerFromEnvironmentAndSocket runs the commands without --url:
// first with PHENIX_URL and PHENIX_TOKEN, then over the unix socket.
func TestBuilderServerFromEnvironmentAndSocket(t *testing.T) {
	fake := newBuilderFake(t, map[string][]builderFakeAnswer{routeDrafts: builderOK(builderTestDrafts)})

	t.Setenv(builderURLEnv, fake.url)
	t.Setenv(builderTokenEnv, builderTestToken)

	// Flags given empty values, as from unset variables, leave the
	// environment's values in place.
	for _, args := range [][]string{
		{"drafts", "list", "-o", FormatJSON},
		{"drafts", "list", "-o", FormatJSON, "--url", "", "--token", ""},
		{"drafts", "list", "-o", FormatJSON, "--url=", "--token="},
	} {
		before := len(fake.recorded())

		stdout, _, err := runBuilderRemote(args...)
		wantExit(t, err, 0)

		if requests := fake.recorded(); stdout != builderTestDraftsJSON || len(requests) != before+1 ||
			requests[before].token != "Bearer "+builderTestToken {
			t.Errorf("%v with %s: wrote %s after %+v", args, builderURLEnv, stdout, requests)
		}
	}

	socket := newFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(builderTokenHeader) != "" {
			w.WriteHeader(http.StatusBadRequest)

			return
		}

		w.Header().Set("Content-Type", mimeJSON)

		switch r.Method + " " + r.URL.EscapedPath() {
		case routeDrafts:
			_, _ = io.WriteString(w, builderTestDrafts)
		case routeDraft:
			_, _ = io.WriteString(w, builderTestDraft)
		case routeExport:
			_, _ = io.WriteString(w, `{"name": "Riverside", "yaml": "", "warnings": ["hostname Phenix may clash"], `+
				`"publishBlockers": []}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	// The mode of phenix ui's socket, whatever the umask: one other users
	// may write to is refused.
	if err := os.Chmod(socket.socket, 0o700); err != nil {
		t.Fatalf("setting the mode of the socket: %v", err)
	}

	previous := common.UnixSocket
	common.UnixSocket = socket.socket //nolint:reassign // the fake's socket

	t.Cleanup(func() { common.UnixSocket = previous }) //nolint:reassign // restore the socket
	t.Setenv(builderURLEnv, "")

	// A token without a URL is refused: over the socket, the requests would
	// act as global-admin, not as the token's user.
	for _, args := range [][]string{
		{"drafts", "list"},
		{"drafts", "list", "--token", "other-token"},
		{"templates", "list", "--url", ""},
	} {
		stdout, stderr, err := runBuilderRemote(args...)
		wantExit(t, err, exitRefused)

		if message := err.Error(); !strings.Contains(message, "--token needs --url") ||
			strings.Contains(stdout+stderr+message, builderTestToken) {
			t.Errorf("%v: error = %q, want it to say --token needs --url, without the token", args, message)
		}
	}

	if requests := socket.recorded(); len(requests) != 0 {
		t.Errorf("socket requests = %+v, want none with a token and no URL", requests)
	}

	t.Setenv(builderTokenEnv, "")

	// Over the socket, as global-admin, every draft it may see is listed
	// without --shared: its own and those of other users.
	stdout, _, err := runBuilderRemote("drafts", "list", "-o", FormatJSON)
	wantExit(t, err, 0)

	var list builderDraftList

	if err := json.Unmarshal([]byte(stdout), &list); err != nil {
		t.Fatalf("decoding %s: %v", stdout, err)
	}

	listed := make([]string, 0, len(list.Drafts))
	for _, draft := range list.Drafts {
		listed = append(listed, draft.Owner+"/"+draft.ID)
	}

	if want := []string{"alice/annex", "alice/riverside", "bob/pumps"}; !slices.Equal(listed, want) {
		t.Errorf("over the socket: listed %v, want %v\n%s", listed, want, stdout)
	}

	if requests := socket.recorded(); len(requests) != 1 || requests[0].path != "/api/v1/builder/drafts" {
		t.Errorf("socket requests = %+v, want one GET /api/v1/builder/drafts", requests)
	}

	// A command that posts goes over the socket too.
	stdout, _, err = runBuilderRemote("drafts", "validate", "alice/riverside", "-o", FormatJSON)
	wantExit(t, err, 0)

	var report builderValidation

	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("decoding %s: %v", stdout, err)
	}

	if !report.Valid || len(report.Warnings) != 1 || report.Warnings[0].Message != "hostname Phenix may clash" {
		t.Errorf("validate over the socket reported %+v, want it valid with the one warning", report)
	}

	if requests := socket.recorded(); len(requests) != 3 || requests[2].method != http.MethodPost ||
		requests[2].path != "/api/v1/builder/export/topology" || requests[2].body != `{"document":`+builderTestDocument+`}` {
		t.Errorf("socket requests = %+v, want the document posted to export/topology", requests)
	}

	common.UnixSocket = filepath.Join(filepath.Dir(socket.socket), "none.sock") //nolint:reassign // no server there

	_, _, err = runBuilderRemote("drafts", "list")
	wantExit(t, err, exitRefused)

	if !strings.Contains(err.Error(), "start phenix ui") {
		t.Errorf("error = %q, want it to say how to reach a server", err)
	}
}

func TestBuilderDraftsPreflight(t *testing.T) {
	t.Parallel()

	const (
		failed = `{"checks": [
			{"name": "capacity", "status": "passed", "summary": "4 of 16 VMs fit", "issues": []},
			{"name": "disks", "status": "failed", "summary": "1 disk image is missing",
			 "issues": [{"code": "disk-missing", "severity": "error", "message": "disk image win10.qc2 does not exist",
			             "nodeId": "n-ws"}]},
			{"name": "apps", "status": "unavailable", "summary": "the app list cannot be read"}
		], "passed": ["capacity"], "failed": ["disks"], "unavailable": ["apps"]}`
		wantJSON = `{
  "checks": [
    {
      "name": "capacity",
      "status": "passed",
      "summary": "4 of 16 VMs fit",
      "issues": []
    },
    {
      "name": "disks",
      "status": "failed",
      "summary": "1 disk image is missing",
      "issues": [
        {
          "code": "disk-missing",
          "severity": "error",
          "message": "disk image win10.qc2 does not exist",
          "nodeId": "n-ws"
        }
      ]
    },
    {
      "name": "apps",
      "status": "unavailable",
      "summary": "the app list cannot be read",
      "issues": []
    }
  ],
  "passed": [
    "capacity"
  ],
  "failed": [
    "disks"
  ],
  "unavailable": [
    "apps"
  ]
}
`
	)

	fake := newBuilderFake(t, map[string][]builderFakeAnswer{routePreflight: builderOK(failed)})

	stdout, _, err := runBuilderRemote(fake.remote(
		"drafts", "preflight", "alice/riverside", "--check", "capacity,disks,apps", "--experiment", "riverside", "-o", FormatJSON,
	)...)
	wantExit(t, err, exitFindings)

	if stdout != wantJSON {
		t.Errorf("drafts preflight -o json wrote\n%s\nwant\n%s", stdout, wantJSON)
	}

	if !strings.Contains(err.Error(), "failed: disks") {
		t.Errorf("error = %q, want it to name the failed check", err)
	}

	requests := fake.recorded()
	if want := `{"checks":["capacity","disks","apps"],"experiment":"riverside"}`; len(requests) != 1 || requests[0].body != want {
		t.Errorf("requests = %+v, want one with %s", requests, want)
	}

	stdout, _, err = runBuilderRemote(fake.remote("drafts", "preflight", "alice/riverside")...)
	wantExit(t, err, exitFindings)

	for _, want := range []string{"CHECK", "unavailable", "disk-missing", "node n-ws", "4 of 16 VMs fit"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the table lacks %q:\n%s", want, stdout)
		}
	}

	if requests := fake.recorded(); len(requests) != 2 || requests[1].body != `{"checks":["capacity","network","disks","apps"]}` {
		t.Errorf("requests = %+v, want the second to ask for every check", requests)
	}

	_, _, err = runBuilderRemote(fake.remote("drafts", "preflight", "alice/riverside", "--check", "memory")...)
	wantExit(t, err, exitRefused)

	const wantYAML = `checks:
  - name: capacity
    status: passed
    summary: 4 of 16 VMs fit
    issues: []
  - name: disks
    status: failed
    summary: 1 disk image is missing
    issues:
      - code: disk-missing
        severity: error
        message: disk image win10.qc2 does not exist
        nodeId: n-ws
  - name: apps
    status: unavailable
    summary: the app list cannot be read
    issues: []
passed:
  - capacity
failed:
  - disks
unavailable:
  - apps
`

	stdout, _, err = runBuilderRemote(fake.remote("drafts", "preflight", "alice/riverside", "-o", FormatYAML)...)
	wantExit(t, err, exitFindings)

	if stdout != wantYAML {
		t.Errorf("drafts preflight -o yaml wrote\n%s\nwant\n%s", stdout, wantYAML)
	}
}

// TestBuilderDraftsPreflightMatchesRoute runs drafts preflight against the
// preflight route's recorded answer: the request and the answer that
// TestBuilderPreflightAnswersAsRecorded in phenix/web checks the route
// against. The command must send the recorded request and write every field
// of the answer, with the same values.
func TestBuilderDraftsPreflightMatchesRoute(t *testing.T) {
	t.Parallel()

	path := filepath.Join("..", "web", "testdata", "builder-preflight-exchange.json")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	var exchange struct {
		Request json.RawMessage `json:"request"`
		Answer  json.RawMessage `json:"answer"`
	}

	if err := json.Unmarshal(data, &exchange); err != nil {
		t.Fatalf("decoding %s: %v", path, err)
	}

	var request bytes.Buffer
	if err := json.Compact(&request, exchange.Request); err != nil {
		t.Fatalf("compacting the recorded request: %v", err)
	}

	fake := newBuilderFake(t, map[string][]builderFakeAnswer{routePreflight: builderOK(string(exchange.Answer))})

	stdout, _, err := runBuilderRemote(fake.remote(
		"drafts", "preflight", "alice/riverside", "--check", "disks,apps", "-o", FormatJSON,
	)...)
	wantExit(t, err, exitFindings)

	if !strings.Contains(err.Error(), "failed: apps") {
		t.Errorf("error = %q, want it to name the failed check", err)
	}

	if requests := fake.recorded(); len(requests) != 1 || requests[0].body != request.String() {
		t.Errorf("requests = %+v, want one with the recorded request %s", requests, request.String())
	}

	var got, want any

	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("decoding %s: %v", stdout, err)
	}

	if err := json.Unmarshal(exchange.Answer, &want); err != nil {
		t.Fatalf("decoding the recorded answer: %v", err)
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("drafts preflight -o json did not write the route's answer recorded in %s; it wrote\n%s", path, stdout)
	}
}

func TestBuilderDraftsPreflightStrict(t *testing.T) {
	t.Parallel()

	fake := newBuilderFake(t, map[string][]builderFakeAnswer{routePreflight: builderOK(`{"checks": [
		{"name": "capacity", "status": "passed", "summary": "fits", "issues": []},
		{"name": "apps", "status": "unavailable", "summary": "the app list cannot be read", "issues": []}
	], "passed": ["capacity"], "failed": [], "unavailable": ["apps"]}`)})

	_, _, err := runBuilderRemote(fake.remote("drafts", "preflight", "alice/riverside", "-o", FormatJSON)...)
	wantExit(t, err, 0)

	_, _, err = runBuilderRemote(fake.remote("drafts", "preflight", "alice/riverside", "--strict", "-o", FormatJSON)...)
	wantExit(t, err, exitFindings)

	if !strings.Contains(err.Error(), "apps") {
		t.Errorf("error = %q, want it to name the unavailable check", err)
	}
}

// builderTestTemplates is an answer of GET /builder/templates for alice: a
// built-in template and a collection of her own, a collection bob shares
// with her, and a collection the server read from a file.
const builderTestTemplates = `{
	"owner": "alice",
	"templates": [
		{"id": "server", "owner": "alice", "source": "own", "name": "Server", "description": "Generic Linux server",
		 "device": {"iconKey": "server", "spec": {"general": {"hostname": "server"}}},
		 "version": 1, "etag": "\"1\"", "serverWide": false, "collections": []},
		{"id": "t-plc", "owner": "alice", "source": "own", "name": "PLC", "description": "",
		 "device": {"icon": "plc-icon", "spec": {"general": {"hostname": "plc"}}},
		 "version": 2, "etag": "\"2\"", "serverWide": false, "collections": ["c-lab"]},
		{"id": "t-hmi", "owner": "bob", "source": "shared", "name": "plc", "description": "Operator HMI",
		 "device": {"spec": {"general": {"hostname": "hmi"}}},
		 "version": 1, "etag": "\"1\"", "serverWide": false, "collections": ["c-ot"]}
	],
	"collections": [
		{"id": "c-lab", "owner": "alice", "source": "own", "name": "Lab", "description": "",
		 "templateIds": ["t-plc"], "version": 1, "etag": "\"1\"", "serverWide": false},
		{"id": "c-ot", "owner": "bob", "source": "shared", "name": "OT", "description": "OT devices of bob",
		 "templateIds": ["t-hmi"], "version": 1, "etag": "\"1\"", "serverWide": false}
	],
	"preloaded": [
		{"collection": {"id": "server-0123", "owner": "", "source": "preloaded", "name": "Substation", "description": "",
		                "templateIds": ["server-0123-a"], "version": 1, "etag": "\"1\"", "serverWide": false},
		 "templates": [{"id": "server-0123-a", "owner": "", "source": "preloaded", "name": "RTU", "description": "",
		                "device": {"spec": {"general": {"hostname": "rtu"}}},
		                "version": 1, "etag": "\"1\"", "serverWide": false, "collections": ["server-0123"]}]}
	],
	"canShare": true, "canPublish": false, "damaged": false,
	"limits": {"templates": 200, "collections": 50, "shares": 25, "nameBytes": 128, "descriptionBytes": 1024,
	           "deviceBytes": 16384}
}`

func TestBuilderTemplatesList(t *testing.T) {
	t.Parallel()

	const wantJSON = `{
  "user": "alice",
  "collections": [
    {
      "id": "c-lab",
      "name": "Lab",
      "owner": "alice",
      "source": "mine",
      "templates": [
        "t-plc"
      ]
    },
    {
      "id": "c-ot",
      "name": "OT",
      "description": "OT devices of bob",
      "owner": "bob",
      "source": "shared",
      "templates": [
        "t-hmi"
      ]
    },
    {
      "id": "server-0123",
      "name": "Substation",
      "source": "server",
      "templates": [
        "server-0123-a"
      ]
    }
  ],
  "templates": [
    {
      "id": "server",
      "name": "Server",
      "description": "Generic Linux server",
      "owner": "alice",
      "source": "built-in",
      "collections": []
    },
    {
      "id": "t-plc",
      "name": "PLC",
      "owner": "alice",
      "source": "mine",
      "collections": [
        "c-lab"
      ]
    },
    {
      "id": "t-hmi",
      "name": "plc",
      "description": "Operator HMI",
      "owner": "bob",
      "source": "shared",
      "collections": [
        "c-ot"
      ]
    },
    {
      "id": "server-0123-a",
      "name": "RTU",
      "source": "server",
      "collections": [
        "server-0123"
      ]
    }
  ]
}
`

	fake := newBuilderFake(t, map[string][]builderFakeAnswer{routeTemplates: builderOK(builderTestTemplates)})

	stdout, _, err := runBuilderRemote(fake.remote("templates", "list", "-o", FormatJSON)...)
	wantExit(t, err, 0)

	if stdout != wantJSON {
		t.Errorf("templates list -o json wrote\n%s\nwant\n%s", stdout, wantJSON)
	}

	stdout, _, err = runBuilderRemote(fake.remote("templates", "list", "--owner", "bob", "-o", FormatJSON)...)
	wantExit(t, err, 0)

	var report builderTemplateReport

	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("decoding %s: %v", stdout, err)
	}

	if len(report.Collections) != 1 || report.Collections[0].ID != "c-ot" ||
		len(report.Templates) != 1 || report.Templates[0].ID != "t-hmi" {
		t.Errorf("--owner bob listed %+v, want only the items of bob", report)
	}

	stdout, _, err = runBuilderRemote(fake.remote("templates", "list")...)
	wantExit(t, err, 0)

	for _, want := range []string{"Collections:", "Templates:", "built-in", "Substation"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the tables lack %q:\n%s", want, stdout)
		}
	}

	if !slices.ContainsFunc(strings.Split(stdout, "\n"), func(line string) bool {
		return strings.Contains(line, "PLC") && strings.Contains(line, "Lab") && strings.Contains(line, "t-plc")
	}) {
		t.Errorf("no line names template PLC in collection Lab:\n%s", stdout)
	}

	const wantYAML = `user: alice
collections:
  - id: c-lab
    name: Lab
    owner: alice
    source: mine
    templates:
      - t-plc
  - id: c-ot
    name: OT
    description: OT devices of bob
    owner: bob
    source: shared
    templates:
      - t-hmi
  - id: server-0123
    name: Substation
    source: server
    templates:
      - server-0123-a
templates:
  - id: server
    name: Server
    description: Generic Linux server
    owner: alice
    source: built-in
    collections: []
  - id: t-plc
    name: PLC
    owner: alice
    source: mine
    collections:
      - c-lab
  - id: t-hmi
    name: plc
    description: Operator HMI
    owner: bob
    source: shared
    collections:
      - c-ot
  - id: server-0123-a
    name: RTU
    source: server
    collections:
      - server-0123
`

	stdout, _, err = runBuilderRemote(fake.remote("templates", "list", "-o", FormatYAML)...)
	wantExit(t, err, 0)

	if stdout != wantYAML {
		t.Errorf("templates list -o yaml wrote\n%s\nwant\n%s", stdout, wantYAML)
	}
}

func TestBuilderTemplatesExport(t *testing.T) {
	t.Parallel()

	icon := builderTestIcon(t)
	library := `{"icons": [{"name": "PLC-Icon", "id": "abc", "owner": "alice", "width": 2, "height": 2, "bytes": 70,
		"created": "2026-10-01T09:00:00Z", "updated": "2026-10-01T09:00:00Z", "aliases": [], "data": "` + icon + `",
		"canRename": true, "canDelete": true}], "maxIcons": 64, "maxBytes": 1048576, "usedBytes": 70, "usedIcons": 1}`

	fake := newBuilderFake(t, map[string][]builderFakeAnswer{
		routeTemplates: builderOK(builderTestTemplates),
		routeIcons:     builderOK(library),
	})

	wantLab := `{
  "$schema": "https://phenix.sandia.gov/schemas/builder/templates/v1",
  "name": "Lab",
  "templates": [
    {
      "name": "PLC",
      "device": {
        "icon": "plc-icon",
        "spec": {
          "general": {
            "hostname": "plc"
          }
        }
      }
    }
  ],
  "icons": {
    "plc-icon": {
      "data": "` + icon + `"
    }
  }
}
`

	stdout, _, err := runBuilderRemote(fake.remote("templates", "export", "--collection", "Lab", "--format", FormatJSON)...)
	wantExit(t, err, 0)

	if stdout != wantLab {
		t.Errorf("templates export --collection Lab --format json wrote\n%s\nwant\n%s", stdout, wantLab)
	}

	stdout, stderr, err := runBuilderRemote(fake.remote("templates", "export")...)
	wantExit(t, err, 0)

	file, err := bdoc.ParseTemplateFile([]byte(stdout))
	if err != nil {
		t.Fatalf("the exported file does not load: %v\n%s", err, stdout)
	}

	names := make([]string, 0, len(file.Templates))
	for _, template := range file.Templates {
		names = append(names, template.Name)
	}

	if want := []string{"Server", "PLC", "plc (2)", "RTU"}; file.Name != builderTemplatesFileName || !slices.Equal(names, want) {
		t.Errorf("the file is collection %q of %v, want %q of %v", file.Name, names, builderTemplatesFileName, want)
	}

	if icons := slices.Sorted(maps.Keys(file.Icons)); !slices.Equal(icons, []string{"plc-icon"}) {
		t.Errorf("the file carries icons %v, want plc-icon", icons)
	}

	if !strings.Contains(stderr, `warning: template "plc" is "plc (2)" in the file`) {
		t.Errorf("standard error = %q, want a warning about the renamed template", stderr)
	}

	again, _, err := runBuilderRemote(fake.remote("templates", "export")...)
	wantExit(t, err, 0)

	if again != stdout {
		t.Error("exporting the same templates again wrote other bytes")
	}

	stdout, _, err = runBuilderRemote(fake.remote("templates", "export", "--collection", "server-0123", "--format", FormatJSON)...)
	wantExit(t, err, 0)

	if file, err := bdoc.ParseTemplateFile([]byte(stdout)); err != nil || file.Name != "Substation" ||
		len(file.Templates) != 1 || file.Templates[0].Name != "RTU" {
		t.Errorf("--collection by ID wrote %s (err = %v), want the Substation collection", stdout, err)
	}

	for _, args := range [][]string{
		{"templates", "export", "--collection", "Nowhere"},
		{"templates", "export", "--owner", "carol"},
		{"templates", "export", "--format", "xml"},
	} {
		_, _, err := runBuilderRemote(fake.remote(args...)...)
		wantExit(t, err, exitRefused)
	}
}

func TestBuilderTemplatesImport(t *testing.T) {
	t.Parallel()

	icon := builderTestIcon(t)

	// alice already has collections named Lab and lab (2).
	listing := strings.Replace(builderTestTemplates, `{"id": "c-lab", "owner"`,
		`{"id": "c-lab2", "owner": "alice", "source": "own", "name": "lab (2)", "description": "", `+
			`"templateIds": [], "version": 1, "etag": "\"1\"", "serverWide": false}, {"id": "c-lab", "owner"`, 1)

	if listing == builderTestTemplates {
		t.Fatal("the listing names no collection c-lab")
	}

	fake := newBuilderFake(t, map[string][]builderFakeAnswer{
		routeTemplates: builderOK(listing),
		routeAddIcon: {
			{status: http.StatusConflict, when: `"name":"taken-icon"`,
				body: `{"message": "the icon name taken-icon is taken by an icon bob uploaded", "cause": ""}`},
			{status: http.StatusCreated, body: `{"name": "hist-icon"}`},
		},
		routeAddItems: {{status: http.StatusCreated, body: `{"created": [{"id": "n1", "etag": "\"1\""}, ` +
			`{"id": "n2", "etag": "\"1\""}], "collection": {"id": "n3", "etag": "\"1\""}}`}},
	})

	content, err := json.Marshal(bdoc.TemplateFile{
		Schema:      bdoc.TemplateFileSchemaURI,
		Name:        "Lab",
		Description: "Lab devices",
		Templates: []bdoc.TemplateFileTemplate{
			{Name: "Historian", Device: bdoc.TemplateDevice{
				Icon: "hist-icon", Spec: map[string]any{"general": map[string]any{"hostname": "historian"}},
			}},
			{Name: "Jump host", Description: "Bastion", Device: bdoc.TemplateDevice{
				Spec: map[string]any{"general": map[string]any{"hostname": "jump"}},
			}},
		},
		Icons: map[string]bdoc.Icon{"hist-icon": {Data: icon}, "taken-icon": {Data: icon}},
	})
	if err != nil {
		t.Fatalf("encoding the template file: %v", err)
	}

	path := filepath.Join(t.TempDir(), "lab.templates.json")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("writing the template file: %v", err)
	}

	stdout, stderr, err := runBuilderRemote(fake.remote("templates", "import", path)...)
	wantExit(t, err, 0)

	if want := "Imported 2 templates as collection Lab (3).\n"; stdout != want {
		t.Errorf("templates import wrote %q, want %q", stdout, want)
	}

	if !strings.Contains(stderr, "warning: icon taken-icon was not added: the icon name taken-icon is taken") {
		t.Errorf("standard error = %q, want a warning about the icon whose name is taken", stderr)
	}

	requests := fake.recorded()

	paths := make([]string, 0, len(requests))
	for _, request := range requests {
		paths = append(paths, request.method+" "+request.path)
	}

	if want := []string{routeTemplates, routeAddIcon, routeAddIcon, routeAddItems}; !slices.Equal(paths, want) {
		t.Fatalf("requests = %v, want %v", paths, want)
	}

	if want := `{"name":"hist-icon","data":"` + icon + `"}`; requests[1].body != want {
		t.Errorf("first icon request = %s, want %s", requests[1].body, want)
	}

	want := `{"templates":[{"name":"Historian","description":"",` +
		`"device":{"icon":"hist-icon","spec":{"general":{"hostname":"historian"}}}},` +
		`{"name":"Jump host","description":"Bastion","device":{"spec":{"general":{"hostname":"jump"}}}}],` +
		`"collection":{"name":"Lab (3)","description":"Lab devices"}}`
	if requests[3].body != want {
		t.Errorf("items request = %s, want %s", requests[3].body, want)
	}

	// --name names the collection, numbered apart all the same.
	stdout, _, err = runBuilderRemote(fake.remote("templates", "import", path, "--name", "OT")...)
	wantExit(t, err, 0)

	if want := "Imported 2 templates as collection OT.\n"; stdout != want {
		t.Errorf("templates import --name OT wrote %q, want %q: only the caller's own collections count", stdout, want)
	}

	// A file of one template and no icon: the summary says so, and nothing
	// is written to standard error.
	solo := filepath.Join(t.TempDir(), "solo.templates.yaml")
	soloFile := "$schema: " + bdoc.TemplateFileSchemaURI + "\nname: Solo\ntemplates:\n" +
		"  - name: Jump host\n    device:\n      spec:\n        general:\n          hostname: jump\n"

	if err := os.WriteFile(solo, []byte(soloFile), 0o600); err != nil {
		t.Fatalf("writing the template file: %v", err)
	}

	stdout, stderr, err = runBuilderRemote(fake.remote("templates", "import", solo)...)
	wantExit(t, err, 0)

	if want := "Imported 1 template as collection Solo.\n"; stdout != want || stderr != "" {
		t.Errorf("templates import of one template wrote %q and %q to standard error, want %q and nothing", stdout, stderr, want)
	}

	// A file that is not a template file is refused before any request.
	bad := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(bad, []byte("name: Lab\ntemplates: []\n"), 0o600); err != nil {
		t.Fatalf("writing the bad file: %v", err)
	}

	before := len(fake.recorded())

	_, _, err = runBuilderRemote(fake.remote("templates", "import", bad)...)
	wantExit(t, err, exitRefused)

	if after := len(fake.recorded()); after != before {
		t.Errorf("a refused file made %d requests, want none", after-before)
	}
}
