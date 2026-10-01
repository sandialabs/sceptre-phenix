package web

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/gorilla/mux"

	"phenix/store"
	"phenix/types"
	"phenix/web/middleware"
	"phenix/web/weberror"
)

// validationTestTopologyYAML is a topology whose second node's drive has no
// image; the image key sits one level too high, under hardware.
const validationTestTopologyYAML = `apiVersion: phenix.sandia.gov/v1
kind: Topology
metadata:
  name: demo
spec:
  nodes:
  - type: VirtualMachine
    general:
      hostname: host-00
    hardware:
      os_type: linux
      drives:
      - image: ubuntu.qc2
  - type: VirtualMachine
    general:
      hostname: ADServer
    hardware:
      os_type: windows
      image: win10.qc2
      drives:
      - interface: ide
`

// validationTestTopologyJSON is the broken node alone, as the single-line JSON
// the UI config editor sends.
const validationTestTopologyJSON = `{"apiVersion":"phenix.sandia.gov/v1","kind":"Topology","metadata":{"name":"demo"},` +
	`"spec":{"nodes":[{"type":"VirtualMachine","general":{"hostname":"ADServer"},` +
	`"hardware":{"os_type":"windows","image":"win10.qc2","drives":[{"interface":"ide"}]}}]}}`

// Patterns for the validator's own text on the documents, in which "*" stands
// for any text: the pointers and the missing keys are phenix's, the rest is
// kin-openapi's.
const (
	validationTestYAMLRaw = `config validation failed: *"/nodes/1/hardware/drives/0/image"*"image"*"/nodes/1/external"*"external"*`
	validationTestJSONRaw = `config validation failed: *"/nodes/0/hardware/drives/0/image"*"image"*"/nodes/0/external"*"external"*`
)

// validationTestYAMLLines returns patterns for the explained form of
// validationTestYAMLRaw.
func validationTestYAMLLines() []string {
	return []string{
		`nodes[1] "ADServer" (line 21): *"image"* (at hardware.drives[0].image)`,
		`  hint: "image:" is on line 19 under nodes[1].hardware, but the schema expects it at nodes[1].hardware.drives[0].image`,
		`nodes[1] "ADServer" (line 14): *"external"* (at external)`,
	}
}

// validationTestJSONLines returns patterns for the explained form of
// validationTestJSONRaw; a single-line source gets no line numbers.
func validationTestJSONLines() []string {
	return []string{
		`nodes[0] "ADServer": *"image"* (at hardware.drives[0].image)`,
		`  hint: "image:" is under nodes[0].hardware, but the schema expects it at nodes[0].hardware.drives[0].image`,
		`nodes[0] "ADServer": *"external"* (at external)`,
	}
}

// matchLine reports whether line matches pattern, in which "*" stands for any
// text, such as the validator's reason.
func matchLine(line, pattern string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return line == pattern
	}

	prefix, suffix := parts[0], parts[len(parts)-1]
	if len(line) < len(prefix)+len(suffix) || !strings.HasPrefix(line, prefix) || !strings.HasSuffix(line, suffix) {
		return false
	}

	rest := line[len(prefix) : len(line)-len(suffix)]

	for _, part := range parts[1 : len(parts)-1] {
		i := strings.Index(rest, part)
		if i < 0 {
			return false
		}

		rest = rest[i+len(part):]
	}

	return true
}

// validationTestBody is one way a client sends the invalid topology. raw and
// lines are patterns for the validator's text and the explained lines.
type validationTestBody struct {
	name        string
	contentType string
	body        []byte
	raw         string
	lines       []string
}

// validationTestUpload builds the multipart body the UI upload form sends for
// one file, and its content type.
func validationTestUpload(t *testing.T, filename, content string) ([]byte, string) {
	t.Helper()

	var body bytes.Buffer

	form := multipart.NewWriter(&body)

	part, err := form.CreateFormFile("fileupload", filename)
	if err != nil {
		t.Fatalf("creating form file: %v", err)
	}

	if _, err := part.Write([]byte(content)); err != nil {
		t.Fatalf("writing form file: %v", err)
	}

	if err := form.Close(); err != nil {
		t.Fatalf("closing form: %v", err)
	}

	return body.Bytes(), form.FormDataContentType()
}

// validationTestBodies returns the invalid topology as a JSON body and a YAML
// body, then as a file upload of each: one body per branch the handlers
// parse. The workflow configs endpoint takes only the first two.
func validationTestBodies(t *testing.T) []validationTestBody {
	t.Helper()

	jsonUpload, jsonUploadType := validationTestUpload(t, "topology.json", validationTestTopologyJSON)
	yamlUpload, yamlUploadType := validationTestUpload(t, "topology.yml", validationTestTopologyYAML)

	return []validationTestBody{
		{
			name: "JSON body", contentType: mimeJSON, body: []byte(validationTestTopologyJSON),
			raw: validationTestJSONRaw, lines: validationTestJSONLines(),
		},
		{
			name: "YAML body", contentType: mimeYAML, body: []byte(validationTestTopologyYAML),
			raw: validationTestYAMLRaw, lines: validationTestYAMLLines(),
		},
		{
			name: "JSON file upload", contentType: jsonUploadType, body: jsonUpload,
			raw: validationTestJSONRaw, lines: validationTestJSONLines(),
		},
		{
			name: "YAML file upload", contentType: yamlUploadType, body: yamlUpload,
			raw: validationTestYAMLRaw, lines: validationTestYAMLLines(),
		},
	}
}

// validationTestRequest builds a request whose context carries an allow-all
// role, like the global-admin role on the unix socket.
func validationTestRequest(t *testing.T, method, target, contentType string, body []byte, vars map[string]string) *http.Request {
	t.Helper()

	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	req = mux.SetURLVars(req, vars)

	ctx := context.WithValue(req.Context(), middleware.ContextKeyRole, workflowTestRole(true))
	ctx = context.WithValue(ctx, middleware.ContextKeyUser, "test-user")

	return req.WithContext(ctx)
}

// installValidationTestStore installs a store mock that fails the test on any
// call it does not expect, and restores the real store afterwards.
func installValidationTestStore(t *testing.T) *store.MockStore {
	t.Helper()

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	original := store.DefaultStore
	t.Cleanup(func() { store.DefaultStore = original }) //nolint:reassign // restore test double

	m := store.NewMockStore(ctrl)
	store.DefaultStore = m //nolint:reassign // monkey patching for test

	return m
}

// assertValidationWebError checks the 400 a handler returns for an invalid
// config: the first explained line as the message, all of them in
// metadata.validation and the validator's own text in metadata.validation-raw.
// raw and lines are patterns for [matchLine].
func assertValidationWebError(t *testing.T, err error, raw string, lines []string) {
	t.Helper()

	var werr *weberror.WebError
	if !errors.As(err, &werr) {
		t.Fatalf("expected a *weberror.WebError, got %v", err)
	}

	if werr.Status != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", werr.Status, http.StatusBadRequest)
	}

	if !matchLine(werr.Message, lines[0]) {
		t.Errorf("message:\n got %s\nwant %s", werr.Message, lines[0])
	}

	if !matchLine(werr.Cause, raw) {
		t.Errorf("cause:\n got %s\nwant %s", werr.Cause, raw)
	}

	got := strings.Split(werr.UserMetadata["validation"], "\n")
	if len(got) != len(lines) || !slices.EqualFunc(got, lines, matchLine) {
		t.Errorf("metadata.validation:\n got %q\nwant %q", got, lines)
	}

	if got := werr.UserMetadata["validation-raw"]; !matchLine(got, raw) {
		t.Errorf("metadata.validation-raw:\n got %s\nwant %s", got, raw)
	}
}

func TestCreateConfigExplainsValidationErrors(t *testing.T) {
	for _, tt := range validationTestBodies(t) {
		t.Run(tt.name, func(t *testing.T) {
			installValidationTestStore(t) // validation fails before any store call

			req := validationTestRequest(t, http.MethodPost, "/api/v1/configs", tt.contentType, tt.body, nil)

			assertValidationWebError(t, CreateConfig(httptest.NewRecorder(), req), tt.raw, tt.lines)
		})
	}
}

func TestUpdateConfigExplainsValidationErrors(t *testing.T) {
	for _, tt := range validationTestBodies(t) {
		t.Run(tt.name, func(t *testing.T) {
			m := installValidationTestStore(t)
			key, _ := store.NewConfig("topology/demo")

			// config.Update reads the stored config before validating the new one.
			m.EXPECT().Get(gomock.Eq(key)).Return(nil)

			req := validationTestRequest(
				t,
				http.MethodPut,
				"/api/v1/configs/topology/demo",
				tt.contentType,
				tt.body,
				map[string]string{"kind": "topology", "name": "demo"},
			)

			assertValidationWebError(t, UpdateConfig(httptest.NewRecorder(), req), tt.raw, tt.lines)
		})
	}
}

func TestWorkflowUpsertConfigExplainsValidationErrors(t *testing.T) {
	// The workflow endpoint takes JSON and YAML bodies, not uploads.
	bodies := validationTestBodies(t)[:2]

	for _, exists := range []bool{false, true} {
		for _, tt := range bodies {
			t.Run(fmt.Sprintf("%s, exists=%t", tt.name, exists), func(t *testing.T) {
				t.Setenv("BRANCH_NAME", "") // the handler sets it; t.Setenv restores it

				m := installValidationTestStore(t)
				key, _ := store.NewConfig("topology/demo")

				if exists {
					// The handler checks the config exists and validates the
					// schema before config.Update, so its own read never happens.
					m.EXPECT().Get(gomock.Eq(key)).Return(nil)
				} else {
					m.EXPECT().Get(gomock.Eq(key)).
						Return(fmt.Errorf("%w: key demo does not exist in bucket Topology", store.ErrNotExist))
				}

				req := validationTestRequest(
					t,
					http.MethodPost,
					"/api/v1/workflow/configs/main",
					tt.contentType,
					tt.body,
					map[string]string{"branch": "main"},
				)

				assertValidationWebError(t, WorkflowUpsertConfig(httptest.NewRecorder(), req), tt.raw, tt.lines)
			})
		}
	}
}

// TestValidationWebErrorExplainsUnwrappedErrors covers an error the config
// API has not wrapped once more, such as types.ValidateConfigSpec's own.
func TestValidationWebErrorExplainsUnwrappedErrors(t *testing.T) {
	c, err := store.NewConfigFromYAML([]byte(validationTestTopologyYAML))
	if err != nil {
		t.Fatalf("parsing config: %v", err)
	}

	err = validationWebError([]byte(validationTestTopologyYAML), types.ValidateConfigSpec(*c))

	assertValidationWebError(t, err, validationTestYAMLRaw, validationTestYAMLLines())
}
