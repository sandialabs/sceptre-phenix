package web

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/gorilla/mux"

	bapi "phenix/api/builder"
	bdoc "phenix/types/builder"
	"phenix/util/plog/plogtest"
	"phenix/web/rbac"
)

const (
	builderIconsRoute = "/builder/icons"

	// builderNoSniff and builderIconCSP are the headers of Builder
	// responses, written out so a changed constant fails the test.
	builderNoSniff = "nosniff"
	builderIconCSP = "default-src 'none'; frame-ancestors 'none'"
)

// builderIconPNG returns a PNG of the given size whose color depends on
// seed, as the standard library encodes one: a document accepts it as it is.
func builderIconPNG(t *testing.T, width, height, seed int) []byte {
	t.Helper()

	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	img.SetNRGBA(0, 0, color.NRGBA{R: uint8(seed), G: uint8(seed >> 8), B: 9, A: 255})

	var out bytes.Buffer

	if err := png.Encode(&out, img); err != nil {
		t.Fatalf("encoding a PNG: %v", err)
	}

	return out.Bytes()
}

// builderIconBody returns the body of a request adding an icon.
func builderIconBody(t *testing.T, name string, data []byte) string {
	t.Helper()

	body, err := json.Marshal(builderIconUpload{Name: name, Data: base64.StdEncoding.EncodeToString(data)})
	if err != nil {
		t.Fatalf("encoding the request: %v", err)
	}

	return string(body)
}

// postIcon adds an icon as user, with builderUserRole.
func (h *builderHarness) postIcon(user, name string, data []byte) *httptest.ResponseRecorder {
	h.t.Helper()

	return h.do(builderRequest{
		method: http.MethodPost, path: builderIconsRoute, body: builderIconBody(h.t, name, data), user: user,
		role: builderUserRole(),
	})
}

// iconRequest makes a request of one icon as alice with role, and reports an
// answer that is not JSON to t.
func (h *builderHarness) iconRequest(t *testing.T, method, name, body string, role *rbac.Role) *httptest.ResponseRecorder {
	t.Helper()

	recorder := h.do(builderRequest{
		method: method, path: builderIconsRoute + "/" + name, body: body, user: builderAlice, role: role,
	})

	if err := builderJSONError(recorder); err != nil {
		t.Errorf("%s %s: %v", method, name, err)
	}

	return recorder
}

// icons returns the icon library as the list route answers user with role.
func (h *builderHarness) icons(user string, role *rbac.Role) builderIconListResponse {
	h.t.Helper()

	var list builderIconListResponse

	h.doDecode(builderRequest{method: http.MethodGet, path: builderIconsRoute, user: user, role: role}, http.StatusOK, &list)

	return list
}

// addBuilderIcon adds an icon as alice and returns it as the answer gives it.
func addBuilderIcon(t *testing.T, harness *builderHarness, name string, upload []byte) builderIconResponse {
	t.Helper()

	var icon builderIconResponse

	created := harness.doDecode(builderRequest{
		method: http.MethodPost, path: builderIconsRoute, body: builderIconBody(t, name, upload), user: builderAlice,
		role: builderUserRole(),
	}, http.StatusCreated, &icon)

	if err := builderJSONError(created); err != nil {
		t.Errorf("a new icon: %v", err)
	}

	if got := created.Header().Get("ETag"); got != "" {
		t.Errorf("a new icon: ETag = %q, want none", got)
	}

	return icon
}

func TestBuilderIconLibrary(t *testing.T) {
	harness := newBuilderHarness(t)
	user := builderUserRole()

	// A raw read, so an empty library is seen to be a list and not null.
	empty := harness.do(builderRequest{method: http.MethodGet, path: builderIconsRoute, user: builderAlice, role: user})

	if empty.Code != http.StatusOK {
		t.Errorf("GET %s of an empty library: status = %d, want %d", builderIconsRoute, empty.Code, http.StatusOK)
	}

	var emptyList map[string]any
	if err := json.Unmarshal(empty.Body.Bytes(), &emptyList); err != nil {
		t.Fatalf("decoding the empty list %s: %v", empty.Body, err)
	}

	if diff := cmp.Diff(map[string]any{"icons": []any{}, "maxIcons": float64(2000)}, emptyList); diff != "" {
		t.Errorf("GET %s of an empty library mismatch (-want +got):\n%s", builderIconsRoute, diff)
	}

	upload := builderIconPNG(t, 3, 2, 1)
	data := base64.StdEncoding.EncodeToString(upload)
	icon := addBuilderIcon(t, harness, "PLC", upload)

	want := builderIconResponse{
		Name: "PLC", ID: bdoc.IconID(upload), Owner: builderAlice, Width: 3, Height: 2, Bytes: len(upload),
		Created: icon.Created, Updated: icon.Created, CreatedBy: builderAlice, UpdatedBy: builderAlice,
		Aliases: []string{}, Data: data, CanRename: true, CanDelete: true,
	}

	if diff := cmp.Diff(want, icon); diff != "" {
		t.Errorf("POST %s (PLC) mismatch (-want +got):\n%s", builderIconsRoute, diff)
	}

	if icon.Created.IsZero() {
		t.Errorf("POST %s (PLC): created = %v, want a time", builderIconsRoute, icon.Created)
	}

	// A document accepts the icon under its name.
	if issues := bdoc.ValidateIcons(map[string]bdoc.Icon{icon.Name: {Data: icon.Data}}, "icons"); len(issues) != 0 {
		t.Errorf("ValidateIcons(the new icon) = %v, want no issues", issues)
	}
}

// TestBuilderIconNameIsTaken refuses other bytes under a taken name with 409.
func TestBuilderIconNameIsTaken(t *testing.T) {
	harness := newBuilderHarness(t)

	addBuilderIcon(t, harness, "PLC", builderIconPNG(t, 3, 2, 1))

	taken := harness.postIcon(builderBob, "Plc", builderIconPNG(t, 3, 2, 2))

	if taken.Code != http.StatusConflict {
		t.Errorf("POST %s (other bytes as Plc): status = %d, want %d: %s", builderIconsRoute, taken.Code, http.StatusConflict, taken.Body)
	}

	const message = `icon name "Plc" is taken by an icon alice uploaded. Choose another name`

	if got := builderRefusal(t, taken).Message; got != message {
		t.Errorf("POST %s (other bytes as Plc): message = %q, want %q", builderIconsRoute, got, message)
	}
}

// TestBuilderIconRenameAndDelete renames and deletes an icon, and logs each
// change without the image.
func TestBuilderIconRenameAndDelete(t *testing.T) {
	harness := newBuilderHarness(t)
	logs := plogtest.Capture(t)
	user := builderUserRole()
	upload := builderIconPNG(t, 3, 2, 1)
	icon := addBuilderIcon(t, harness, "PLC", upload)

	var moved builderIconResponse

	harness.as(builderAlice, user).doDecode(builderRequest{
		method: http.MethodPut, path: builderIconsRoute + "/plc", body: `{"name":"plc-2"}`,
	}, http.StatusOK, &moved)

	want := icon
	want.Name, want.Aliases = "plc-2", []string{"PLC"}

	if diff := cmp.Diff(want, moved, cmpopts.IgnoreFields(builderIconResponse{}, "Updated")); diff != "" {
		t.Errorf("PUT %s/plc (plc-2) mismatch (-want +got):\n%s", builderIconsRoute, diff)
	}

	wantList := builderIconListResponse{Icons: []builderIconResponse{moved}, MaxIcons: bapi.MaxIcons}
	if diff := cmp.Diff(wantList, harness.icons(builderAlice, user)); diff != "" {
		t.Errorf("GET %s after the rename mismatch (-want +got):\n%s", builderIconsRoute, diff)
	}

	deleted := harness.iconRequest(t, http.MethodDelete, "PLC", "", user)
	if deleted.Code != http.StatusNoContent {
		t.Errorf("DELETE %s/PLC: status = %d, want %d", builderIconsRoute, deleted.Code, http.StatusNoContent)
	}

	if deleted.Body.Len() != 0 {
		t.Errorf("DELETE %s/PLC: body = %q, want none", builderIconsRoute, deleted.Body)
	}

	if err := iconChangesLogError(t, logs, base64.StdEncoding.EncodeToString(upload)); err != nil {
		t.Errorf("logging the icon changes: %v", err)
	}
}

// iconChangesLogError reports a log that does not say once who added, renamed
// and deleted an icon, or holds the image, whose base64 is data.
func iconChangesLogError(t *testing.T, logs *plogtest.Logs, data string) error {
	t.Helper()

	var errs []error

	for _, message := range []string{"added builder icon", "renamed builder icon", "deleted builder icon"} {
		if got := len(logs.Records(t, plogtest.Message(message))); got != 1 {
			errs = append(errs, fmt.Errorf("the log says %q %d times, want once: %s", message, got, logs))
		}
	}

	if strings.Contains(logs.String(), data) || strings.Contains(logs.String(), data[:24]) {
		errs = append(errs, fmt.Errorf("the log holds the image: %s", logs))
	}

	return errors.Join(errs...)
}

// TestBuilderIconLibraryIsBounded refuses an icon beyond the library limit
// with 422.
func TestBuilderIconLibraryIsBounded(t *testing.T) {
	harness := newBuilderHarness(t)

	for i := range bapi.MaxIcons {
		data := builderIconPNG(t, 1, 1, i)
		name := fmt.Sprintf("icon-%04d", i)

		value, err := json.Marshal(bapi.LibraryIcon{
			Kind: "icon", Name: name, ID: bdoc.IconID(data), Owner: builderAlice, Width: 1, Height: 1,
			Bytes: len(data), Aliases: []string{}, Data: base64.StdEncoding.EncodeToString(data),
		})
		if err != nil {
			t.Fatalf("encoding icon %d: %v", i, err)
		}

		if _, err := harness.store.CreateRecord(bapi.NamespaceIcons, "name/"+name, value); err != nil {
			t.Fatalf("planting icon %d: %v", i, err)
		}
	}

	recorder := harness.postIcon(builderBob, "one-too-many", builderIconPNG(t, 1, 1, bapi.MaxIcons))

	if recorder.Code != http.StatusUnprocessableEntity {
		t.Errorf("POST %s (the 2001st icon): status = %d, want %d: %s",
			builderIconsRoute, recorder.Code, http.StatusUnprocessableEntity, recorder.Body)
	}

	const message = "icon library is full: it holds at most 2000 icons"

	if got := builderRefusal(t, recorder).Message; got != message {
		t.Errorf("POST %s (the 2001st icon): message = %q, want %q", builderIconsRoute, got, message)
	}
}

// TestBuilderIconUploadIsNormalized stores the pixels of a loose PNG alone,
// and finds them again for the same upload.
func TestBuilderIconUploadIsNormalized(t *testing.T) {
	harness := newBuilderHarness(t)
	loose := append(builderIconPNG(t, 4, 4, 2), "<script>alert(document.domain)</script>"...)

	var icon builderIconResponse

	harness.doDecode(builderRequest{
		method: http.MethodPost, path: builderIconsRoute, body: builderIconBody(t, "loose", loose), user: builderAlice,
	}, http.StatusCreated, &icon)

	stored, err := base64.StdEncoding.DecodeString(icon.Data)
	if err != nil {
		t.Fatalf("decoding the stored icon %+v: %v", icon, err)
	}

	if bytes.Contains(stored, []byte("script")) {
		t.Errorf("POST %s (a loose PNG) stored %q, want the pixels of the upload alone", builderIconsRoute, stored)
	}

	if want := bdoc.IconID(stored); icon.ID != want {
		t.Errorf("POST %s (a loose PNG): id = %q, want %q of the stored pixels", builderIconsRoute, icon.ID, want)
	}

	if icon.ID == bdoc.IconID(loose) {
		t.Errorf("POST %s (a loose PNG): id = %q, want other than the id of the upload", builderIconsRoute, icon.ID)
	}

	if again := harness.postIcon(builderAlice, "loose", loose); again.Code != http.StatusOK {
		t.Errorf("POST %s (the same upload again): status = %d, want %d", builderIconsRoute, again.Code, http.StatusOK)
	}
}

func TestBuilderIconRequests(t *testing.T) {
	const (
		notRequest = "request body is not a valid Builder request"
		notBase64  = "icon data is not base64"
		required   = "icon data is required"
	)

	pixel := builderIconPNG(t, 1, 1, 3)
	valid := base64.StdEncoding.EncodeToString(pixel)
	encode := func(data []byte) string { return base64.StdEncoding.EncodeToString(data) }
	named := func(data string) string { return `{"name":"plc","data":"` + data + `"}` }

	tests := []struct {
		name    string
		body    string
		status  int
		message string
	}{
		{name: "not JSON", body: `<svg/>`, status: http.StatusBadRequest, message: notRequest},
		{name: "an array", body: `[]`, status: http.StatusBadRequest, message: notRequest},
		{
			name: "an unknown field", body: `{"name":"plc","data":"` + valid + `","extra":1}`,
			status: http.StatusBadRequest, message: notRequest,
		},
		{
			name: "a declared type", body: `{"name":"plc","data":"` + valid + `","type":"image/svg+xml"}`,
			status: http.StatusBadRequest, message: notRequest,
		},
		{
			name: "two values", body: named(valid) + `{}`,
			status: http.StatusBadRequest, message: "request body carries more than one JSON value",
		},
		{name: "data that is a number", body: `{"name":"plc","data":1}`, status: http.StatusBadRequest, message: notRequest},
		{name: "a name that is a number", body: `{"name":1,"data":"` + valid + `"}`, status: http.StatusBadRequest, message: notRequest},
		{name: "no data", body: `{"name":"plc"}`, status: http.StatusBadRequest, message: required},
		{name: "empty data", body: named(""), status: http.StatusBadRequest, message: required},
		{name: "data that is markup", body: named("<svg onload=alert(1)>"), status: http.StatusBadRequest, message: notBase64},
		{name: "data without its padding", body: named("YWI"), status: http.StatusBadRequest, message: notBase64},
		{name: "data with a line break", body: named(valid[:8] + `\n` + valid[8:]), status: http.StatusBadRequest, message: notBase64},
		{
			name: "data in the URL alphabet", body: named(base64.URLEncoding.EncodeToString([]byte{0xfb, 0xff, 0xfe})),
			status: http.StatusBadRequest, message: notBase64,
		},
		{name: "a data URL", body: named("data:image/png;base64," + valid), status: http.StatusBadRequest, message: notBase64},
		{
			name:   "an SVG",
			body:   named(encode([]byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`))),
			status: http.StatusUnprocessableEntity, message: "icon is not a PNG image",
		},
		{
			name: "one byte above the upload limit", body: named(encode(make([]byte, bapi.MaxIconUploadBytes+1))),
			status: http.StatusRequestEntityTooLarge, message: "icon is larger than 65536 bytes",
		},
		{
			name: "a body above its limit", body: named(strings.Repeat("A", builderIconRequestBytes)),
			status: http.StatusRequestEntityTooLarge, message: "request body is larger than 131072 bytes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			harness := newBuilderHarness(t)
			logs := plogtest.Capture(t)

			recorder := harness.do(builderRequest{
				method: http.MethodPost, path: builderIconsRoute, body: tt.body, user: builderAlice,
			})

			if err := builderJSONError(recorder); err != nil {
				t.Errorf("POST %s: %v", builderIconsRoute, err)
			}

			if recorder.Code != tt.status {
				t.Errorf("POST %s: status = %d, want %d: %s", builderIconsRoute, recorder.Code, tt.status, recorder.Body)
			}

			if got := builderRefusal(t, recorder).Message; got != tt.message {
				t.Errorf("POST %s: message = %q, want %q", builderIconsRoute, got, tt.message)
			}

			if got := harness.store.Count(bapi.NamespaceIcons); got != 0 {
				t.Errorf("POST %s: a refused upload left %d records, want 0", builderIconsRoute, got)
			}

			// Neither the answer nor the log repeats the image that was sent.
			for _, sent := range []string{"alert", "onload", valid} {
				if strings.Contains(recorder.Body.String(), sent) || strings.Contains(logs.String(), sent) {
					t.Errorf("POST %s: the answer or the log repeats %q of the request: %s | %s",
						builderIconsRoute, sent, recorder.Body, logs)
				}
			}
		})
	}
}

// TestBuilderIconRename answers a request that is not one, a body above its
// limit and an unknown icon, and renames to a change of case.
func TestBuilderIconRename(t *testing.T) {
	harness := newBuilderHarness(t)
	user := builderUserRole()

	added := addBuilderIcon(t, harness, "plc", builderIconPNG(t, 1, 1, 1))

	for _, tt := range []struct {
		name, icon, body string
		status           int
		message          string
	}{
		{name: "an unknown field", icon: "plc", body: `{"name":"x","extra":1}`, status: http.StatusBadRequest, message: "request body is not a valid Builder request"},
		{
			name: "a body above its limit", icon: "plc", body: `{"name":"` + strings.Repeat("n", builderIconRenameBytes) + `"}`,
			status: http.StatusRequestEntityTooLarge, message: "request body is larger than 4096 bytes",
		},
		{name: "an icon nobody has", icon: "scada", body: `{"name":"x"}`, status: http.StatusNotFound, message: "icon not found"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			recorder := harness.iconRequest(t, http.MethodPut, tt.icon, tt.body, user)

			if recorder.Code != tt.status {
				t.Errorf("PUT %s/%s: status = %d, want %d: %s", builderIconsRoute, tt.icon, recorder.Code, tt.status, recorder.Body)
			}

			if got := builderRefusal(t, recorder).Message; got != tt.message {
				t.Errorf("PUT %s/%s: message = %q, want %q", builderIconsRoute, tt.icon, got, tt.message)
			}
		})
	}

	var icon builderIconResponse

	harness.as(builderAlice, user).doDecode(builderRequest{
		method: http.MethodPut, path: builderIconsRoute + "/plc", body: `{"name":"PLC"}`,
	}, http.StatusOK, &icon)

	want := added
	want.Name = "PLC"

	if diff := cmp.Diff(want, icon, cmpopts.IgnoreFields(builderIconResponse{}, "Updated")); diff != "" {
		t.Errorf("PUT %s/plc (a change of case) mismatch (-want +got):\n%s", builderIconsRoute, diff)
	}
}

func TestBuilderIconNotFound(t *testing.T) {
	harness := newBuilderHarness(t)
	upload := builderIconPNG(t, 1, 1, 5)

	if recorder := harness.postIcon(builderAlice, "kept", upload); recorder.Code != http.StatusCreated {
		t.Fatalf("adding an icon: status = %d", recorder.Code)
	}

	_, digits, _ := strings.Cut(bdoc.IconID(upload), ":")

	for _, icon := range []string{
		"missing",
		digits,
		"sha256:" + digits,
		"kept%20",
		"a%20b",
	} {
		for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
			t.Run(method+" "+icon, func(t *testing.T) {
				recorder := harness.iconRequest(t, method, icon, `{"name":"renamed"}`, builderUserRole())

				if recorder.Code != http.StatusNotFound {
					t.Errorf("%s %s: status = %d, want %d: %s", method, icon, recorder.Code, http.StatusNotFound, recorder.Body)
				}

				if got := builderRefusal(t, recorder).Message; got != "icon not found" {
					t.Errorf("%s %s: message = %q, want %q", method, icon, got, "icon not found")
				}

				// The answer never repeats the path.
				if len(icon) > 8 && strings.Contains(recorder.Body.String(), icon[:8]) {
					t.Errorf("%s %s: the answer repeats the path: %s", method, icon, recorder.Body)
				}
			})
		}
	}

	listed := harness.icons(builderAlice, builderUserRole()).Icons

	names := make([]string, 0, len(listed))
	for _, icon := range listed {
		names = append(names, icon.Name)
	}

	if want := []string{"kept"}; !slices.Equal(names, want) {
		t.Errorf("GET %s lists %q, want %q", builderIconsRoute, names, want)
	}
}

// TestBuilderResponseHeaders asserts every Builder answer is nosniff, the icon
// library's also carry its CSP, and no other route is touched.
func TestBuilderResponseHeaders(t *testing.T) {
	harness := newBuilderHarness(t)

	// A route of the same router that is not a Builder route, registered
	// after the Builder routes as [Start] registers the rest.
	harness.api.Handle("/configs/{kind}/{name}", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).Methods("GET")

	type operation struct {
		method, template string
	}

	var operations []operation

	err := harness.api.Walk(func(route *mux.Route, _ *mux.Router, _ []*mux.Route) error {
		template, templateErr := route.GetPathTemplate()
		methods, methodsErr := route.GetMethods()

		if templateErr != nil || methodsErr != nil {
			return nil //nolint:nilerr // routes without a path or methods are not operations
		}

		for _, method := range methods {
			operations = append(operations, operation{method: method, template: strings.TrimPrefix(template, "/api/v1")})
		}

		return nil
	})
	if err != nil {
		t.Fatalf("walking the routes: %v", err)
	}

	var (
		variable = regexp.MustCompile(`\{[^}]+\}`)
		builder  int
		icons    int
		others   int
	)

	for _, op := range operations {
		path := variable.ReplaceAllString(op.template, "x")

		// With every permission, and with no identity at all: the headers
		// do not depend on how the request is answered.
		for _, user := range []string{builderAlice, ""} {
			recorder := harness.do(builderRequest{method: op.method, path: path, body: `{}`, user: user})
			what := fmt.Sprintf("%s %s as %q (%d)", op.method, op.template, user, recorder.Code)
			header := recorder.Header()

			switch {
			case strings.HasPrefix(op.template, builderIconsRoute):
				icons++

				want := map[string]string{
					"X-Content-Type-Options": builderNoSniff, "Content-Security-Policy": builderIconCSP, "ETag": "",
				}
				if diff := cmp.Diff(want, builderTestHeader(header, want)); diff != "" {
					t.Errorf("%s headers mismatch (-want +got):\n%s", what, diff)
				}
			case strings.HasPrefix(op.template, "/builder/"), op.template == "/schemas/builder/v1",
				op.template == "/schemas/builder/templates/v1", op.template == "/schemas/builder/package/v1",
				op.template == builderLayerSetSchemaPath:
				builder++

				// No Content-Security-Policy outside the icon library.
				want := map[string]string{"X-Content-Type-Options": builderNoSniff, "Content-Security-Policy": ""}
				if diff := cmp.Diff(want, builderTestHeader(header, want)); diff != "" {
					t.Errorf("%s headers mismatch (-want +got):\n%s", what, diff)
				}
			default:
				others++

				if len(header.Values("X-Content-Type-Options")) != 0 || len(header.Values("Content-Security-Policy")) != 0 {
					t.Errorf("%s: a route outside the Builder carries its headers: %v", what, header)
				}
			}
		}
	}

	// Two users times each method: five icon operations and their OPTIONS,
	// and at least 45 others.
	if icons != 2*10 || builder < 2*45 || others != 2 {
		t.Errorf("checked %d icon, %d other Builder and %d other responses, want %d, at least %d and %d",
			icons, builder, others, 2*10, 2*45, 2)
	}

	// The routes that answer with what a user uploaded or drew are among
	// them, whichever file registers them.
	for _, want := range []operation{
		{method: http.MethodPost, template: "/builder/legacy"},
		{method: http.MethodPost, template: "/builder/generate"},
		{method: http.MethodPost, template: "/builder/export/topology"},
		{method: http.MethodPost, template: "/builder/package"},
		{method: http.MethodPost, template: "/builder/package/resolve"},
		{method: http.MethodGet, template: "/builder/documents/{document}"},
		{method: http.MethodGet, template: "/builder/drafts/{draft}"},
		{method: http.MethodGet, template: "/builder/templates"},
		{method: http.MethodPost, template: "/builder/templates/items"},
		{method: http.MethodGet, template: "/builder/icons/{icon}"},
	} {
		if !slices.Contains(operations, want) {
			t.Errorf("%s %s was not checked: it is not a registered route", want.method, want.template)
		}
	}

	// The router's own answers under the Builder's paths carry the headers;
	// those outside them carry neither.
	for _, tt := range []struct {
		method, path      string
		status            int
		builder, iconsCSP bool
	}{
		{http.MethodGet, "/builder/no-such-route", http.StatusNotFound, true, false},
		{http.MethodPost, "/builder/published", http.StatusNotFound, true, false},
		{http.MethodPatch, "/builder/drafts", http.StatusMethodNotAllowed, true, false},
		{http.MethodPut, "/schemas/builder/v1", http.StatusMethodNotAllowed, true, false},
		{http.MethodPut, "/schemas/builder/templates/v1", http.StatusMethodNotAllowed, true, false},
		{http.MethodPut, "/schemas/builder/package/v1", http.StatusMethodNotAllowed, true, false},
		{http.MethodPut, builderLayerSetSchemaPath, http.StatusMethodNotAllowed, true, false},
		{http.MethodPatch, builderIconsRoute, http.StatusMethodNotAllowed, true, true},
		{http.MethodPost, builderIconsRoute + "/x", http.StatusMethodNotAllowed, true, true},
		{http.MethodGet, builderIconsRoute + "/x/y", http.StatusNotFound, true, true},
		{http.MethodGet, "/no-such-route", http.StatusNotFound, false, false},
		{http.MethodPatch, "/configs/x/x", http.StatusMethodNotAllowed, false, false},
	} {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			recorder := harness.do(builderRequest{method: tt.method, path: tt.path, user: builderAlice})

			if recorder.Code != tt.status {
				t.Errorf("%s %s: status = %d, want %d", tt.method, tt.path, recorder.Code, tt.status)
			}

			want := map[string]string{
				"X-Content-Type-Options":  map[bool]string{true: builderNoSniff}[tt.builder],
				"Content-Security-Policy": map[bool]string{true: builderIconCSP}[tt.iconsCSP],
			}
			if diff := cmp.Diff(want, builderTestHeader(recorder.Header(), want)); diff != "" {
				t.Errorf("%s %s headers mismatch (-want +got):\n%s", tt.method, tt.path, diff)
			}
		})
	}

	// The headers are also on what a later middleware, such as
	// authentication, answers in place of the route.
	harness.api.Use(func(http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "missing phenix auth token header", http.StatusUnauthorized)
		})
	})

	refused := harness.do(builderRequest{method: http.MethodGet, path: builderIconsRoute, user: builderAlice})

	if refused.Code != http.StatusUnauthorized {
		t.Errorf("GET %s refused before the route: status = %d, want %d", builderIconsRoute, refused.Code, http.StatusUnauthorized)
	}

	want := map[string]string{"X-Content-Type-Options": builderNoSniff, "Content-Security-Policy": builderIconCSP}
	if diff := cmp.Diff(want, builderTestHeader(refused.Header(), want)); diff != "" {
		t.Errorf("GET %s refused before the route headers mismatch (-want +got):\n%s", builderIconsRoute, diff)
	}
}
