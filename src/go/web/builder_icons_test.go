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

	// builderNoSniff, builderNoStore and builderIconCSP are the headers of
	// Builder responses, written out so a changed constant fails the test.
	builderNoSniff = "nosniff"
	builderNoStore = "no-store"
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

	return h.iconRequestIfMatch(t, method, name, body, "", role)
}

// iconRequestIfMatch is [builderHarness.iconRequest] with an If-Match header.
func (h *builderHarness) iconRequestIfMatch(
	t *testing.T,
	method, name, body, ifMatch string,
	role *rbac.Role,
) *httptest.ResponseRecorder {
	t.Helper()

	recorder := h.do(builderRequest{
		method: method, path: builderIconsRoute + "/" + name, body: body, user: builderAlice, role: role, ifMatch: ifMatch,
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

	if got := created.Header().Get("ETag"); got != icon.ETag || got != bapi.RevisionETag(icon.Revision) {
		t.Errorf("a new icon: ETag = %q, want the tag %q of revision %d", got, icon.ETag, icon.Revision)
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

	wantEmpty := map[string]any{"icons": []any{}, "maxIcons": float64(2000), "etag": empty.Header().Get("ETag")}
	if diff := cmp.Diff(wantEmpty, emptyList); diff != "" {
		t.Errorf("GET %s of an empty library mismatch (-want +got):\n%s", builderIconsRoute, diff)
	}

	upload := builderIconPNG(t, 3, 2, 1)
	data := base64.StdEncoding.EncodeToString(upload)
	icon := addBuilderIcon(t, harness, "PLC", upload)

	want := builderIconResponse{
		builderIconSummary: builderIconSummary{
			Name: "PLC", ID: bdoc.IconID(upload), Owner: builderAlice, Width: 3, Height: 2, Bytes: len(upload),
			Created: icon.Created, Updated: icon.Created, CreatedBy: builderAlice, UpdatedBy: builderAlice,
			Aliases: []string{}, CanRename: true, CanDelete: true,
			Revision: icon.Revision, ETag: bapi.RevisionETag(icon.Revision),
		},
		Data: data,
	}

	if diff := cmp.Diff(want, icon, cmp.AllowUnexported(builderIconResponse{})); diff != "" {
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

// TestBuilderIconListHasNoImages asserts the list of the library holds no
// image and answers 304 while it is the same, and one icon gives its image.
func TestBuilderIconListHasNoImages(t *testing.T) {
	harness := newBuilderHarness(t)
	user := builderUserRole()
	upload := builderIconPNG(t, 3, 2, 1)
	added := addBuilderIcon(t, harness, "plc", upload)

	list := func(ifNoneMatch string) *httptest.ResponseRecorder {
		t.Helper()

		return harness.do(builderRequest{
			method: http.MethodGet, path: builderIconsRoute, user: builderAlice, role: user, ifNoneMatch: ifNoneMatch,
		})
	}

	first := list("")
	if err := builderStatusError(first, http.StatusOK); err != nil {
		t.Fatalf("GET %s: %v", builderIconsRoute, err)
	}

	var raw struct {
		Icons []map[string]any `json:"icons"`
		ETag  string           `json:"etag"`
	}

	if err := json.Unmarshal(first.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decoding GET %s: %v", builderIconsRoute, err)
	}

	etag := first.Header().Get("ETag")
	if etag == "" || raw.ETag != etag {
		t.Errorf("GET %s: etag = %q, ETag header = %q, want the same tag in both", builderIconsRoute, raw.ETag, etag)
	}

	if len(raw.Icons) != 1 {
		t.Fatalf("GET %s: %d icons, want 1", builderIconsRoute, len(raw.Icons))
	}

	if _, ok := raw.Icons[0]["data"]; ok {
		t.Errorf("GET %s: icon plc has data, want no image in the list", builderIconsRoute)
	}

	want := map[string]any{"id": added.ID, "revision": float64(added.Revision), "etag": added.ETag}
	got := map[string]any{"id": raw.Icons[0]["id"], "revision": raw.Icons[0]["revision"], "etag": raw.Icons[0]["etag"]}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("GET %s: icon plc mismatch (-want +got):\n%s", builderIconsRoute, diff)
	}

	// The same list, named in any form of its tag, is not sent again. The
	// 304 also tells the browser to keep no copy.
	for _, tag := range []string{etag, "W/" + etag, `"other", ` + etag, "*"} {
		same := list(tag)
		header := same.Header()

		if same.Code != http.StatusNotModified || same.Body.Len() != 0 || header.Get("ETag") != etag ||
			header.Get("Cache-Control") != builderNoStore {
			t.Errorf("GET %s with If-None-Match %s = %d %q, ETag %q, Cache-Control %q; want 304, no body, ETag %s, %s",
				builderIconsRoute, tag, same.Code, same.Body, header.Get("ETag"), header.Get("Cache-Control"), etag, builderNoStore)
		}
	}

	// A rename changes the list, and its tag.
	harness.as(builderAlice, user).mustDo("rename", builderRequest{
		method: http.MethodPut, path: builderIconsRoute + "/plc", body: `{"name":"plc-2"}`,
	}, http.StatusOK)

	changed := list(etag)
	if err := builderStatusError(changed, http.StatusOK); err != nil {
		t.Errorf("GET %s with the tag before the rename: %v", builderIconsRoute, err)
	}

	if got := changed.Header().Get("ETag"); got == etag || got == "" {
		t.Errorf("GET %s after the rename: ETag = %q, want a new tag", builderIconsRoute, got)
	}

	// One icon gives its image, and 304 for the tag of its revision.
	var icon builderIconResponse

	read := harness.doDecode(builderRequest{
		method: http.MethodGet, path: builderIconsRoute + "/plc", user: builderAlice, role: user,
	}, http.StatusOK, &icon)

	if icon.Data != base64.StdEncoding.EncodeToString(upload) || icon.Name != "plc-2" {
		t.Errorf("GET %s/plc = %s with data of %d bytes, want plc-2 with its image", builderIconsRoute, icon.Name, len(icon.Data))
	}

	again := harness.do(builderRequest{
		method: http.MethodGet, path: builderIconsRoute + "/plc", user: builderAlice, role: user,
		ifNoneMatch: read.Header().Get("ETag"),
	})
	if again.Code != http.StatusNotModified || again.Body.Len() != 0 || again.Header().Get("Cache-Control") != builderNoStore {
		t.Errorf("GET %s/plc with If-None-Match of its tag = %d %q, Cache-Control %q; want 304, no body, %s",
			builderIconsRoute, again.Code, again.Body, again.Header().Get("Cache-Control"), builderNoStore)
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

	const message = `icon name "Plc" belongs to an icon alice uploaded. Choose another name`

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

	if diff := cmp.Diff(want, moved, cmp.AllowUnexported(builderIconResponse{}),
		cmpopts.IgnoreFields(builderIconSummary{}, "Updated", "Revision", "ETag")); diff != "" {
		t.Errorf("PUT %s/plc (plc-2) mismatch (-want +got):\n%s", builderIconsRoute, diff)
	}

	if moved.ETag == icon.ETag {
		t.Errorf("PUT %s/plc (plc-2): etag = %s, want a new one", builderIconsRoute, moved.ETag)
	}

	wantList := builderIconListResponse{Icons: []builderIconSummary{moved.builderIconSummary}, MaxIcons: bapi.MaxIcons}
	if diff := cmp.Diff(wantList, harness.icons(builderAlice, user), cmpopts.IgnoreFields(builderIconListResponse{}, "ETag")); diff != "" {
		t.Errorf("GET %s after the rename mismatch (-want +got):\n%s", builderIconsRoute, diff)
	}

	// A delete needs the current tag. The tag of the icon before its rename
	// is refused with the current one.
	missing := harness.iconRequest(t, http.MethodDelete, "PLC", "", user)
	if err := builderStatusError(missing, http.StatusBadRequest); err != nil {
		t.Errorf("DELETE %s/PLC without If-Match: %v", builderIconsRoute, err)
	}

	stale := harness.iconRequestIfMatch(t, http.MethodDelete, "PLC", "", icon.ETag, user)
	if err := builderStatusError(stale, http.StatusPreconditionFailed); err != nil {
		t.Errorf("DELETE %s/PLC with the tag before the rename: %v", builderIconsRoute, err)
	}

	if got := stale.Header().Get("ETag"); got != moved.ETag {
		t.Errorf("DELETE %s/PLC with the tag before the rename: ETag = %q, want %q", builderIconsRoute, got, moved.ETag)
	}

	if got := builderRefusal(t, stale).Code; got != string(bdoc.CodeRequestStale) {
		t.Errorf("DELETE %s/PLC with the tag before the rename: code = %q, want %q", builderIconsRoute, got, bdoc.CodeRequestStale)
	}

	deleted := harness.iconRequestIfMatch(t, http.MethodDelete, "PLC", "", moved.ETag, user)
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
		required   = "the request needs icon data"
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
		{name: "an icon nobody has", icon: "scada", body: `{"name":"x"}`, status: http.StatusNotFound, message: "phenix cannot find the icon"},
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

	if diff := cmp.Diff(want, icon, cmp.AllowUnexported(builderIconResponse{}),
		cmpopts.IgnoreFields(builderIconSummary{}, "Updated", "Revision", "ETag")); diff != "" {
		t.Errorf("PUT %s/plc (a change of case) mismatch (-want +got):\n%s", builderIconsRoute, diff)
	}
}

// TestBuilderIconRenameAliasLimit renames an icon 16 times, the most old
// names an icon keeps. The next rename to a new name gets 422.
func TestBuilderIconRenameAliasLimit(t *testing.T) {
	harness := newBuilderHarness(t)
	user := builderUserRole()

	addBuilderIcon(t, harness, "plc-0", builderIconPNG(t, 1, 1, 1))

	const limit = 16

	for i := 1; i <= limit; i++ {
		body := fmt.Sprintf(`{"name":"plc-%d"}`, i)
		if recorder := harness.iconRequest(t, http.MethodPut, fmt.Sprintf("plc-%d", i-1), body, user); recorder.Code != http.StatusOK {
			t.Fatalf("PUT %s/plc-%d: status = %d, want %d: %s", builderIconsRoute, i-1, recorder.Code, http.StatusOK, recorder.Body)
		}
	}

	recorder := harness.iconRequest(t, http.MethodPut, "plc-16", `{"name":"plc-new"}`, user)

	if recorder.Code != http.StatusUnprocessableEntity {
		t.Errorf("PUT %s/plc-16: status = %d, want %d: %s",
			builderIconsRoute, recorder.Code, http.StatusUnprocessableEntity, recorder.Body)
	}

	want := "icon plc-16 already has 16 old names, the most an icon keeps. Upload the image under the new name instead. " +
		"Deleting the icon also deletes its old names. Diagrams that use these names then stop showing the icon"
	if got := builderRefusal(t, recorder).Message; got != want {
		t.Errorf("PUT %s/plc-16: message = %q, want %q", builderIconsRoute, got, want)
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
				recorder := harness.iconRequestIfMatch(t, method, icon, `{"name":"renamed"}`, `"1"`, builderUserRole())

				if recorder.Code != http.StatusNotFound {
					t.Errorf("%s %s: status = %d, want %d: %s", method, icon, recorder.Code, http.StatusNotFound, recorder.Body)
				}

				if got := builderRefusal(t, recorder).Message; got != "phenix cannot find the icon" {
					t.Errorf("%s %s: message = %q, want %q", method, icon, got, "phenix cannot find the icon")
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

// TestBuilderResponseHeaders asserts every Builder answer is nosniff and
// no-store, the icon library's also carry its CSP, and no other route is
// touched.
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
			name := op.method + " " + op.template + " as nobody"
			if user != "" {
				name = op.method + " " + op.template + " as " + user
			}

			t.Run(name, func(t *testing.T) {
				recorder := harness.do(builderRequest{method: op.method, path: path, body: `{}`, user: user})
				what := fmt.Sprintf("%s %s as %q (%d)", op.method, op.template, user, recorder.Code)
				header := recorder.Header()

				switch {
				case strings.HasPrefix(op.template, builderIconsRoute):
					icons++

					want := map[string]string{
						"X-Content-Type-Options": builderNoSniff, "Cache-Control": builderNoStore,
						"Content-Security-Policy": builderIconCSP, "ETag": "",
					}

					// The list of the library carries the tag of the list.
					if op.template == builderIconsRoute && recorder.Code == http.StatusOK {
						want["ETag"] = header.Get("ETag")
						if want["ETag"] == "" {
							t.Errorf("%s: ETag is empty, want the tag of the list", what)
						}
					}

					if diff := cmp.Diff(want, builderTestHeader(header, want)); diff != "" {
						t.Errorf("%s headers mismatch (-want +got):\n%s", what, diff)
					}
				case strings.HasPrefix(op.template, "/builder/"), op.template == "/schemas/builder/v1",
					op.template == "/schemas/builder/templates/v1", op.template == "/schemas/builder/package/v1",
					op.template == builderLayerSetSchemaPath:
					builder++

					// No Content-Security-Policy outside the icon library.
					want := map[string]string{
						"X-Content-Type-Options": builderNoSniff, "Cache-Control": builderNoStore, "Content-Security-Policy": "",
					}
					if diff := cmp.Diff(want, builderTestHeader(header, want)); diff != "" {
						t.Errorf("%s headers mismatch (-want +got):\n%s", what, diff)
					}
				default:
					others++

					if len(header.Values("X-Content-Type-Options")) != 0 || len(header.Values("Content-Security-Policy")) != 0 ||
						len(header.Values("Cache-Control")) != 0 {
						t.Errorf("%s: a route outside the Builder carries its headers: %v", what, header)
					}
				}
			})
		}
	}

	// Two users times each method: five icon operations, and at least 39
	// others. No route has OPTIONS (see TestBuilderOptionsRunNoHandler).
	if icons != 2*5 || builder < 2*39 || others != 2 {
		t.Errorf("checked %d icon, %d other Builder and %d other responses, want %d, at least %d and %d",
			icons, builder, others, 2*5, 2*39, 2)
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
}

// TestBuilderRouterAnswerHeaders asserts the router's own answers under the
// Builder's paths carry the Builder headers, those outside them carry none,
// and so does what a later middleware answers in place of a route.
func TestBuilderRouterAnswerHeaders(t *testing.T) {
	harness := newBuilderHarness(t)

	// A route of the same router that is not a Builder route, registered
	// after the Builder routes as [Start] registers the rest.
	harness.api.Handle("/configs/{kind}/{name}", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).Methods("GET")

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
				"Cache-Control":           map[bool]string{true: builderNoStore}[tt.builder],
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

	want := map[string]string{
		"X-Content-Type-Options": builderNoSniff, "Cache-Control": builderNoStore, "Content-Security-Policy": builderIconCSP,
	}
	if diff := cmp.Diff(want, builderTestHeader(refused.Header(), want)); diff != "" {
		t.Errorf("GET %s refused before the route headers mismatch (-want +got):\n%s", builderIconsRoute, diff)
	}
}
