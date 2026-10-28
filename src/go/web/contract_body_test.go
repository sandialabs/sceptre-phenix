package web

import (
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

// loadContractSpec is openapi.yml as kin-openapi loads it, read once.
//
//nolint:gochecknoglobals // the spec, loaded once for every contract test
var loadContractSpec = sync.OnceValues(func() (*openapi3.T, error) {
	return openapi3.NewLoader().LoadFromFile("public/docs/openapi.yml")
})

// contractSpec returns openapi.yml as kin-openapi loads it.
func contractSpec(t *testing.T) *openapi3.T {
	t.Helper()

	doc, err := loadContractSpec()
	if err != nil {
		t.Fatalf("loading openapi.yml: %v", err)
	}

	return doc
}

// specOperation returns openapi.yml's template and operation for method on the concrete path, query ignored,
// or nil; it prefers the template of fewest parameters, as gorilla matches a literal segment first.
func specOperation(doc *openapi3.T, method, path string) (string, *openapi3.Operation) {
	path, _, _ = strings.Cut(path, "?")

	root := true
	if rest, ok := strings.CutPrefix(path, "/api/v1"); ok {
		root, path = false, rest
	}

	var (
		best     string
		bestOp   *openapi3.Operation
		bestVars = -1
	)

	for template, item := range doc.Paths {
		if itemAtRoot := len(item.Servers) > 0 && !strings.HasSuffix(item.Servers[0].URL, "/api/v1"); itemAtRoot != root {
			continue
		}

		op := item.GetOperation(strings.ToUpper(method))
		if op == nil {
			continue
		}

		quoted := regexp.QuoteMeta(pathParams.ReplaceAllString(template, "{}"))
		if !regexp.MustCompile("^" + strings.ReplaceAll(quoted, `\{\}`, `[^/]+`) + "$").MatchString(path) {
			continue
		}

		if vars := strings.Count(template, "{"); bestVars == -1 || vars < bestVars {
			best, bestOp, bestVars = template, op, vars
		}
	}

	return best, bestOp
}

// wantDocumentedBody fails the test unless resp's body matches the JSON schema openapi.yml gives its status,
// if any, with no field the schema leaves out.
func wantDocumentedBody(t *testing.T, method, path string, resp *http.Response) {
	t.Helper()

	template, op := specOperation(contractSpec(t), method, path)
	if op == nil {
		t.Errorf("openapi.yml has no operation for %s %s", method, path)

		return
	}

	ref := op.Responses.Get(resp.StatusCode)
	if ref == nil {
		ref = op.Responses.Default()
	}

	if ref == nil || ref.Value == nil {
		return
	}

	media := ref.Value.Content.Get(mimeJSON)
	if media == nil || media.Schema == nil || method == http.MethodHead {
		return
	}

	what := method + " " + template + " " + strconv.Itoa(resp.StatusCode)

	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, mimeJSON) {
		t.Errorf("%s: Content-Type = %q, want %s as openapi.yml documents", what, ct, mimeJSON)

		return
	}

	raw := readBody(t, resp)

	// for the caller to read again
	resp.Header.Del("Content-Encoding")
	resp.Body = io.NopCloser(strings.NewReader(raw))

	var body any
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Errorf("%s: body is not JSON: %v", what, err)

		return
	}

	if err := media.Schema.Value.VisitJSON(body); err != nil {
		t.Errorf("%s: body does not match openapi.yml: %v", what, err)
	}

	for _, field := range undocumentedFields("", media.Schema.Value, body) {
		t.Errorf("%s: body sets %s, which openapi.yml does not document", what, field)
	}
}
