package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/util/mm"
	"phenix/web/middleware"
	"phenix/web/weberror"
)

// configsErrorBody is the part of a JSON Error body that the configs shard
// tests compare.
type configsErrorBody struct {
	Message  string            `json:"message"`
	Code     weberror.Code     `json:"code"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// configsStatusCase is a request through the router as an admin over the
// contract fixture, and the status and body it wants. An empty message is not
// compared.
type configsStatusCase struct {
	method, path, body string
	header             []string
	prepare            func(t *testing.T)
	status             int
	message            string
}

// configsMultipart returns a multipart form body with one file part, field,
// named filename, and the Content-Type that names its boundary.
func configsMultipart(t *testing.T, field, filename, content string) (string, string) {
	t.Helper()

	var (
		body   bytes.Buffer
		writer = multipart.NewWriter(&body)
	)

	part, err := writer.CreateFormFile(field, filename)
	if err != nil {
		t.Fatalf("CreateFormFile(%q, %q) error = %v", field, filename, err)
	}

	if _, err := part.Write([]byte(content)); err != nil {
		t.Fatalf("writing multipart part %q: %v", field, err)
	}

	if err := writer.Close(); err != nil {
		t.Fatalf("closing multipart writer: %v", err)
	}

	return body.String(), writer.FormDataContentType()
}

// configsTopology is the contract fixture's demo topology under another name.
func configsTopology(name string) string {
	return strings.Replace(revisionTestTopology, `"name":"demo"`, `"name":"`+name+`"`, 1)
}

// checkConfigsStatus makes tc's request and checks its status, its Error
// body, and that openapi.yml documents the status for the operation.
func checkConfigsStatus(t *testing.T, spec openAPISpec, tc configsStatusCase) configsErrorBody {
	t.Helper()

	useContractFixture(t)
	server := serveContractAs(t, adminRole(), nil).URL

	if tc.prepare != nil {
		tc.prepare(t)
	}

	resp := do(t, tc.method, server+tc.path, tc.body, tc.header...)
	if resp.StatusCode != tc.status {
		t.Fatalf("%s %s = %d, want %d: %s", tc.method, tc.path, resp.StatusCode, tc.status, readBody(t, resp))
	}

	wantDocumentedBody(t, tc.method, tc.path, resp)

	var got configsErrorBody
	if err := json.Unmarshal([]byte(readBody(t, resp)), &got); err != nil {
		t.Fatalf("%s %s body is not JSON: %v", tc.method, tc.path, err)
	}

	want := configsErrorBody{Message: tc.message, Code: weberror.CodeFor(tc.status), Metadata: got.Metadata}
	if tc.message == "" {
		want.Message = got.Message
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("%s %s body mismatch (-want +got):\n%s", tc.method, tc.path, diff)
	}

	if got.Message == "" {
		t.Errorf("%s %s body message = %q, want one", tc.method, tc.path, got.Message)
	}

	if !spec.documents(tc.method, tc.path, tc.status) {
		t.Errorf("openapi.yml documents %d for %s %s = false, want true", tc.status, tc.method, tc.path)
	}

	return got
}

// The config routes answer a name clash with 409, a missing upload part with 400, an unsupported Content-Type with 415,
// an unknown kind in the path with 404 and an unsupported Accept with 406, as openapi.yml documents.
func TestConfigsStatusConfigRoutes(t *testing.T) {
	jsonType := []string{"Content-Type", mimeJSON}
	noUpload, noUploadType := configsMultipart(t, "other", "demo.json", revisionTestTopology)

	for name, tc := range map[string]configsStatusCase{
		"create of existing config": {
			method: "POST", path: "/api/v1/configs", body: revisionTestTopology, header: jsonType,
			status: http.StatusConflict, message: "config with same name already exists",
		},
		"create without fileupload part": {
			method: "POST", path: "/api/v1/configs", body: noUpload, header: []string{"Content-Type", noUploadType},
			status: http.StatusBadRequest, message: "the request has no fileupload file",
		},
		"create with malformed multipart body": {
			method: "POST", path: "/api/v1/configs", body: "not a form",
			header: []string{"Content-Type", "multipart/form-data; boundary=x"},
			status: http.StatusBadRequest, message: "unable to access uploaded file",
		},
		"update without fileupload part": {
			method: "PUT", path: contractConfigPath, body: noUpload, header: []string{"Content-Type", noUploadType},
			status: http.StatusBadRequest, message: "the request has no fileupload file",
		},
		"create of unsupported content type": {
			method: "POST", path: "/api/v1/configs", body: revisionTestTopology, header: []string{"Content-Type", "text/plain"},
			status:  http.StatusUnsupportedMediaType,
			message: "unknown content type provided when creating config: text/plain",
		},
		"update of unsupported content type": {
			method: "PUT", path: contractConfigPath, body: revisionTestTopology, header: []string{"Content-Type", "text/plain"},
			status:  http.StatusUnsupportedMediaType,
			message: "unknown content type provided when updating config: text/plain",
		},
		"get of unknown kind": {
			method: "GET", path: "/api/v1/configs/bogus/x",
			status: http.StatusNotFound, message: "unknown config kind bogus",
		},
		"update of unknown kind": {
			method: "PUT", path: "/api/v1/configs/bogus/x", body: revisionTestTopology, header: jsonType,
			status: http.StatusNotFound, message: "unknown config kind bogus",
		},
		"delete of unknown kind": {
			method: "DELETE", path: "/api/v1/configs/bogus/x",
			status: http.StatusNotFound, message: "unknown config kind bogus",
		},
		"get with unsupported Accept": {
			method: "GET", path: contractConfigPath, header: []string{"Accept", "text/xml"},
			status:  http.StatusNotAcceptable,
			message: "unknown accept content type provided when getting config: text/xml",
		},
		"list of unknown kind": {
			method: "GET", path: "/api/v1/configs?kind=bogus", status: http.StatusBadRequest,
		},
	} {
		t.Run(name, func(t *testing.T) { checkConfigsStatus(t, loadOpenAPI(t), tc) })
	}
}

// PUT of an Experiment config without a spec is a 400 schema failure, not a
// panic that drops the connection.
func TestConfigsStatusExperimentWithoutSpec(t *testing.T) {
	got := checkConfigsStatus(t, loadOpenAPI(t), configsStatusCase{
		method: "PUT", path: "/api/v1/configs/experiment/" + contractExp,
		body:   `{"apiVersion":"phenix.sandia.gov/v1","kind":"Experiment","metadata":{"name":"` + contractExp + `"}}`,
		header: []string{"Content-Type", mimeJSON},
		status: http.StatusBadRequest,
	})

	if validation := got.Metadata["validation"]; !strings.Contains(validation, "spec") {
		t.Errorf("PUT experiment without spec metadata.validation = %q, want the missing spec named", validation)
	}
}

// The config routes take a JSON or YAML media type with parameters or in
// another case, and read a body without a Content-Type as JSON.
func TestConfigsStatusMediaTypeParameters(t *testing.T) {
	for name, tc := range map[string]struct {
		method, path, body, contentType string
		status                          int
	}{
		"create with charset": {
			method: "POST", path: "/api/v1/configs", body: configsTopology("charset"),
			contentType: "application/json; charset=utf-8", status: http.StatusCreated,
		},
		"create in upper case": {
			method: "POST", path: "/api/v1/configs", body: configsTopology("upper"),
			contentType: "APPLICATION/JSON", status: http.StatusCreated,
		},
		"create without content type": {
			method: "POST", path: "/api/v1/configs", body: configsTopology("untyped"), status: http.StatusCreated,
		},
		"update of YAML with charset": {
			method: "PUT", path: contractConfigPath, body: revisionTestTopology,
			contentType: "application/x-yaml;charset=UTF-8", status: http.StatusNoContent,
		},
	} {
		t.Run(name, func(t *testing.T) {
			useContractFixture(t)
			server := serveContractAs(t, adminRole(), nil).URL

			var header []string
			if tc.contentType != "" {
				header = []string{"Content-Type", tc.contentType}
			}

			resp := do(t, tc.method, server+tc.path, tc.body, header...)
			if resp.StatusCode != tc.status {
				t.Errorf("%s %s with Content-Type %q = %d, want %d: %s",
					tc.method, tc.path, tc.contentType, resp.StatusCode, tc.status, readBody(t, resp))
			}
		})
	}
}

// The disk routes answer a malformed size and an unusable upload name as client errors, and do not show the
// server's path in the message.
func TestConfigsStatusDiskRoutes(t *testing.T) {
	upload := func(filename string) (string, []string) {
		body, contentType := configsMultipart(t, "file", filename, "image")

		return body, []string{"Content-Type", contentType}
	}

	parent, parentHeader := upload("..")
	self, selfHeader := upload(".")
	folder, folderHeader := upload(contractExp)
	long, longHeader := upload(strings.Repeat("a", 300) + ".qc2")

	for name, tc := range map[string]configsStatusCase{
		"resize to malformed size": {
			method: "POST", path: "/api/v1/disks/resize?disk=" + contractDisk + "&size=abc", status: http.StatusBadRequest,
			message: `invalid disk size "abc": must be an optionally signed integer followed by one of K, M, G, T, P, E`,
		},
		"upload named parent folder": {
			method: "POST", path: "/api/v1/disks", body: parent, header: parentHeader,
			status: http.StatusBadRequest, message: `Error uploading: ".." is not a usable file name`,
		},
		"upload named files directory": {
			method: "POST", path: "/api/v1/disks", body: self, header: selfHeader,
			status: http.StatusBadRequest, message: `Error uploading: "." is not a usable file name`,
		},
		"upload named as a folder": {
			method: "POST", path: "/api/v1/disks", body: folder, header: folderHeader,
			status: http.StatusConflict, message: `Error uploading: a folder named "` + contractExp + `" exists`,
		},
		"upload with too long a name": {
			method: "POST", path: "/api/v1/disks", body: long, header: longHeader,
			status: http.StatusBadRequest, message: "Error uploading: the file name is too long",
		},
	} {
		t.Run(name, func(t *testing.T) { checkConfigsStatus(t, loadOpenAPI(t), tc) })
	}
}

// The disk action handlers read their parameters from the query string, so a
// route without query matchers gets a 400 that names a missing parameter.
func TestConfigsStatusDiskQueryParameters(t *testing.T) {
	for name, tc := range map[string]struct {
		handler func(http.ResponseWriter, *http.Request) error
		target  string
		want    configsErrorBody
	}{
		"resize reads disk and size": {
			handler: ResizeDisk, target: "/api/v1/disks/resize?disk=" + contractDisk + "&size=abc",
			want: configsErrorBody{
				Code: weberror.CodeInvalidRequest,
				Message: `invalid disk size "abc": must be an optionally signed integer followed by one of ` +
					"K, M, G, T, P, E",
			},
		},
		"commit without disk": {
			handler: CommitDisk, target: "/api/v1/disks/commit",
			want: configsErrorBody{Code: weberror.CodeInvalidRequest, Message: "not a disk image phenix manages: no disk given"},
		},
		"clone without new": {
			handler: CloneDisk, target: "/api/v1/disks/clone?disk=" + contractDisk,
			want: configsErrorBody{
				Code: weberror.CodeInvalidRequest, Message: "not a disk image phenix manages: no new image given",
			},
		},
		"rebase without unsafe": {
			handler: RebaseDisk, target: "/api/v1/disks/rebase?disk=" + contractDisk,
			want: configsErrorBody{Code: weberror.CodeInvalidRequest, Message: `unsafe "" is not true or false`},
		},
	} {
		t.Run(name, func(t *testing.T) {
			useContractFixture(t)

			ctx := context.WithValue(t.Context(), middleware.ContextKeyRole, adminRole())
			req := httptest.NewRequestWithContext(ctx, http.MethodPost, tc.target, nil)
			recorder := httptest.NewRecorder()

			weberror.ErrorHandler(tc.handler).ServeHTTP(recorder, req)

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("POST %s = %d, want %d: %s", tc.target, recorder.Code, http.StatusBadRequest, recorder.Body)
			}

			var got configsErrorBody
			if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
				t.Fatalf("POST %s body is not JSON: %v", tc.target, err)
			}

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("POST %s body mismatch (-want +got):\n%s", tc.target, diff)
			}
		})
	}
}

// GET /hosts and GET /topologies answer a failed minimega or store read with a
// documented 500 and a fixed message.
func TestConfigsStatusListFailures(t *testing.T) {
	cause := errors.New("down")

	for name, tc := range map[string]configsStatusCase{
		"hosts when minimega fails": {
			method: "GET", path: "/api/v1/hosts", status: http.StatusInternalServerError,
			message: "unable to list cluster hosts",
			prepare: func(t *testing.T) {
				t.Helper()

				original := mm.DefaultMM
				t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore test double

				mm.DefaultMM = experimentScheduleMM{testMM: new(testMM), hosts: nil, err: cause} //nolint:reassign // test double
			},
		},
		"topologies when the store fails": {
			method: "GET", path: "/api/v1/topologies", status: http.StatusInternalServerError,
			message: "unable to list topologies",
			prepare: func(t *testing.T) {
				t.Helper()

				failTestStore(t, "list", "Topology", cause)
			},
		},
	} {
		t.Run(name, func(t *testing.T) { checkConfigsStatus(t, loadOpenAPI(t), tc) })
	}
}

// POST /builder/drafts names the new draft under the server's base path, as
// the config and job Location headers do.
func TestConfigsStatusBuilderDraftLocation(t *testing.T) {
	harness := newBuilderHarness(t)

	original := o.basePath
	o.basePath = "/phenix/"

	t.Cleanup(func() { o.basePath = original })

	var draft builderDraftResponse

	recorder := harness.doDecode(builderRequest{
		method: http.MethodPost, path: "/builder/drafts", user: builderAlice,
		body: `{"title":"based","document":` + string(builderDocument(t, "topo")) + `}`,
	}, http.StatusCreated, &draft)

	if got, want := recorder.Header().Get("Location"), "/phenix/api/v1/builder/drafts/"+draft.ID; got != want {
		t.Errorf("POST /builder/drafts Location = %q, want %q", got, want)
	}
}
