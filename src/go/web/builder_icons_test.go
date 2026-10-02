package web

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
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

	"github.com/gorilla/mux"

	bapi "phenix/api/builder"
	"phenix/store"
	bdoc "phenix/types/builder"
	"phenix/util/plog/plogtest"
	"phenix/web/rbac"
	"phenix/web/weberror"
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

// postIcon adds an icon to the user's library.
func (h *builderHarness) postIcon(user, name string, data []byte) *httptest.ResponseRecorder {
	h.t.Helper()

	return h.do(builderRequest{
		method: http.MethodPost, path: builderIconsRoute, body: builderIconBody(h.t, name, data), user: user,
	})
}

// icons returns the user's icon library, as the list route answers.
func (h *builderHarness) icons(user string) builderIconListResponse {
	h.t.Helper()

	recorder := h.do(builderRequest{method: http.MethodGet, path: builderIconsRoute, user: user})
	if recorder.Code != http.StatusOK {
		h.t.Fatalf("listing icons: status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
	}

	// The owner is never sent: it is always the caller.
	if strings.Contains(recorder.Body.String(), `"owner"`) {
		h.t.Fatalf("the icon list names an owner: %s", recorder.Body)
	}

	var list builderIconListResponse

	h.decode(recorder, &list)

	return list
}

// builderIconPath returns the path of an icon, which holds the hex digits
// of its ID.
func builderIconPath(id string) string {
	_, digits, _ := strings.Cut(id, ":")

	return builderIconsRoute + "/" + digits
}

// builderMessage returns the message of an error response.
func builderMessage(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()

	var body weberror.WebError

	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding the error: %v: %s", err, recorder.Body)
	}

	return body.Message
}

// assertIconHeaders asserts a response of the icon library carries both
// security headers, and is JSON whenever it has a body.
func assertIconHeaders(t *testing.T, what string, recorder *httptest.ResponseRecorder) {
	t.Helper()

	header := recorder.Header()

	if got := header.Get("X-Content-Type-Options"); got != builderNoSniff {
		t.Errorf("%s: X-Content-Type-Options = %q, want %q", what, got, builderNoSniff)
	}

	if got := header.Get("Content-Security-Policy"); got != builderIconCSP {
		t.Errorf("%s: Content-Security-Policy = %q, want %q", what, got, builderIconCSP)
	}

	if got := header.Get("Content-Type"); recorder.Body.Len() != 0 && got != mimeJSON {
		t.Errorf("%s: Content-Type = %q, want %q", what, got, mimeJSON)
	}

	if got := header.Get("ETag"); got != "" {
		t.Errorf("%s: ETag = %q, want none: an icon never changes", what, got)
	}
}

func TestBuilderIconLibrary(t *testing.T) {
	harness := newBuilderHarness(t)
	logs := plogtest.Capture(t)

	// A raw read, so an empty library is seen to be a list and not null.
	empty := harness.do(builderRequest{method: http.MethodGet, path: builderIconsRoute, user: builderTestOwner})
	assertIconHeaders(t, "the empty list", empty)

	if got, want := strings.TrimSpace(empty.Body.String()),
		`{"icons":[],"maxIcons":64,"maxBytes":1048576,"usedBytes":0}`; empty.Code != http.StatusOK || got != want {
		t.Fatalf("the empty list = %d %s, want %s", empty.Code, got, want)
	}

	upload := builderIconPNG(t, 3, 2, 1)
	data := base64.StdEncoding.EncodeToString(upload)

	created := harness.postIcon(builderTestOwner, "  plc  ", upload)
	assertIconHeaders(t, "a new icon", created)

	if created.Code != http.StatusCreated {
		t.Fatalf("adding an icon: status = %d, want %d: %s", created.Code, http.StatusCreated, created.Body)
	}

	if strings.Contains(created.Body.String(), `"owner"`) {
		t.Fatalf("the icon names an owner: %s", created.Body)
	}

	var icon builderIconResponse

	harness.decode(created, &icon)

	want := builderIconResponse{
		ID: bdoc.IconID(upload), Name: "plc", Width: 3, Height: 2, Bytes: len(upload), Created: icon.Created, Data: data,
	}

	if icon != want || icon.Created.IsZero() {
		t.Fatalf("the new icon = %+v, want %+v", icon, want)
	}

	// The icon is one a document accepts under that ID.
	if issues := bdoc.ValidateIcons(map[string]bdoc.Icon{icon.ID: {Name: icon.Name, Data: icon.Data}}, "icons"); len(issues) != 0 {
		t.Fatalf("a document refuses the icon: %v", issues)
	}

	// The same bytes again are the icon the library has, under its name.
	again := harness.postIcon(builderTestOwner, "another name", upload)
	assertIconHeaders(t, "an icon the library has", again)

	var same builderIconResponse

	harness.decode(again, &same)

	if again.Code != http.StatusOK || !same.Created.Equal(icon.Created) || same.ID != icon.ID || same.Name != "plc" {
		t.Fatalf("adding the same bytes = %d %+v, want 200 and %+v", again.Code, same, icon)
	}

	list := harness.icons(builderTestOwner)

	if len(list.Icons) != 1 || list.Icons[0].ID != icon.ID || list.Icons[0].Data != data ||
		list.MaxIcons != bapi.MaxLibraryIcons || list.MaxBytes != bapi.MaxLibraryIconBytes || list.UsedBytes != len(upload) {
		t.Fatalf("the list = %+v, want the icon and %d used bytes", list, len(upload))
	}

	deleted := harness.do(builderRequest{method: http.MethodDelete, path: builderIconPath(icon.ID), user: builderTestOwner})
	assertIconHeaders(t, "a deleted icon", deleted)

	if deleted.Code != http.StatusNoContent || deleted.Body.Len() != 0 {
		t.Fatalf("deleting the icon = %d %q, want an empty 204", deleted.Code, deleted.Body)
	}

	if list := harness.icons(builderTestOwner); len(list.Icons) != 0 || list.UsedBytes != 0 {
		t.Fatalf("the list after the delete = %+v, want it empty", list)
	}

	gone := harness.do(builderRequest{method: http.MethodDelete, path: builderIconPath(icon.ID), user: builderTestOwner})
	if gone.Code != http.StatusNotFound || builderMessage(t, gone) != "icon not found" {
		t.Fatalf("deleting it again = %d %s, want 404 icon not found", gone.Code, gone.Body)
	}

	// Who added and deleted which icon is logged; the image never is.
	if !strings.Contains(logs.String(), "added builder icon") || !strings.Contains(logs.String(), "deleted builder icon") {
		t.Errorf("the log does not say an icon was added and deleted: %s", logs)
	}

	if strings.Contains(logs.String(), data) || strings.Contains(logs.String(), data[:24]) {
		t.Errorf("the log holds the image: %s", logs)
	}
}

// TestBuilderIconLibraryIsTheCallersOwn asserts a library belongs to one
// user: another user, even one whose role holds every permission there is,
// on other users' drafts too, neither sees nor deletes its icons.
func TestBuilderIconLibraryIsTheCallersOwn(t *testing.T) {
	harness := newBuilderHarness(t)
	upload := builderIconPNG(t, 2, 2, 9)
	path := builderIconPath(bdoc.IconID(upload))

	if recorder := harness.postIcon(builderTestOwner, "mine", upload); recorder.Code != http.StatusCreated {
		t.Fatalf("adding an icon: status = %d, want %d", recorder.Code, http.StatusCreated)
	}

	if peer := harness.icons(builderTestPeer); len(peer.Icons) != 0 || peer.UsedBytes != 0 {
		t.Fatalf("another user's list = %+v, want it empty", peer)
	}

	foreign := harness.do(builderRequest{method: http.MethodDelete, path: path, user: builderTestPeer})
	assertIconHeaders(t, "deleting another user's icon", foreign)

	if foreign.Code != http.StatusNotFound || builderMessage(t, foreign) != "icon not found" {
		t.Fatalf("deleting another user's icon = %d %s, want 404 icon not found", foreign.Code, foreign.Body)
	}

	// The same image in the peer's library is the peer's own icon, with the
	// same ID, and deleting one leaves the other.
	if recorder := harness.postIcon(builderTestPeer, "theirs", upload); recorder.Code != http.StatusCreated {
		t.Fatalf("the peer adding the same image: status = %d, want %d", recorder.Code, http.StatusCreated)
	}

	if recorder := harness.do(builderRequest{
		method: http.MethodDelete, path: path, user: builderTestOwner,
	}); recorder.Code != http.StatusNoContent {
		t.Fatalf("deleting the icon: status = %d, want %d", recorder.Code, http.StatusNoContent)
	}

	if list := harness.icons(builderTestOwner); len(list.Icons) != 0 {
		t.Fatalf("the list after the delete = %+v, want it empty", list)
	}

	if peer := harness.icons(builderTestPeer); len(peer.Icons) != 1 || peer.Icons[0].Name != "theirs" {
		t.Fatalf("the peer's list after the delete = %+v, want its icon", peer)
	}
}

func TestBuilderIconUploadIsNormalized(t *testing.T) {
	harness := newBuilderHarness(t)

	const secret = "<script>alert(document.domain)</script>"

	strict := builderIconPNG(t, 4, 4, 2)
	loose := append(bytes.Clone(strict), secret...)

	recorder := harness.postIcon(builderTestOwner, "loose", loose)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusCreated, recorder.Body)
	}

	var icon builderIconResponse

	harness.decode(recorder, &icon)

	stored, err := base64.StdEncoding.Strict().DecodeString(icon.Data)
	if err != nil {
		t.Fatalf("the returned data is not base64: %v", err)
	}

	if bytes.Contains(stored, []byte("script")) || icon.ID != bdoc.IconID(stored) || icon.ID == bdoc.IconID(loose) ||
		icon.Bytes != len(stored) || icon.Width != 4 || icon.Height != 4 {
		t.Fatalf("the stored icon = %+v, want the pixels of the upload alone, under their own ID", icon)
	}

	if _, _, err := bdoc.ValidateIconPNG(stored); err != nil {
		t.Fatalf("the stored icon is not one a document accepts: %v", err)
	}

	// The same upload again finds the stored icon.
	if again := harness.postIcon(builderTestOwner, "", loose); again.Code != http.StatusOK {
		t.Fatalf("the same upload again: status = %d, want %d", again.Code, http.StatusOK)
	}

	// One record, which holds the stored bytes and nothing else of the
	// upload.
	keys := harness.store.Keys(bapi.NamespaceIcons)
	if len(keys) != 1 {
		t.Fatalf("icon records = %q, want one", keys)
	}

	record, err := harness.store.GetRecord(bapi.NamespaceIcons, keys[0])
	if err != nil {
		t.Fatalf("reading the icon record: %v", err)
	}

	var kept bapi.LibraryIcon

	if err := json.Unmarshal(record.Value, &kept); err != nil {
		t.Fatalf("decoding the icon record: %v", err)
	}

	if kept.Data != icon.Data || bytes.Contains(record.Value, []byte("script")) {
		t.Fatalf("the record does not hold exactly the stored icon")
	}
}

func TestBuilderIconRequests(t *testing.T) {
	const (
		notRequest = "request body is not a valid Builder request"
		notBase64  = "icon data is not base64"
		required   = "icon data is required"
		notPNG     = "icon is not a PNG image"
		badName    = "icon name must be at most 64 bytes and contain no control characters"
	)

	pixel := builderIconPNG(t, 1, 1, 3)
	valid := base64.StdEncoding.EncodeToString(pixel)
	encode := func(data []byte) string { return base64.StdEncoding.EncodeToString(data) }

	tests := []struct {
		name    string
		body    string
		status  int
		message string
	}{
		{name: "not JSON", body: `<svg/>`, status: http.StatusBadRequest, message: notRequest},
		{name: "an array", body: `[]`, status: http.StatusBadRequest, message: notRequest},
		{
			name: "an unknown field", body: `{"data":"` + valid + `","owner":"bob"}`,
			status: http.StatusBadRequest, message: notRequest,
		},
		{
			name: "a declared type", body: `{"data":"` + valid + `","type":"image/svg+xml"}`,
			status: http.StatusBadRequest, message: notRequest,
		},
		{
			name: "two values", body: `{"data":"` + valid + `"}{}`,
			status: http.StatusBadRequest, message: "request body carries more than one JSON value",
		},
		{name: "data that is a number", body: `{"data":1}`, status: http.StatusBadRequest, message: notRequest},
		{name: "no data", body: `{"name":"plc"}`, status: http.StatusBadRequest, message: required},
		{name: "empty data", body: `{"data":""}`, status: http.StatusBadRequest, message: required},
		{name: "data that is markup", body: `{"data":"<svg onload=alert(1)>"}`, status: http.StatusBadRequest, message: notBase64},
		{name: "data without its padding", body: `{"data":"YWI"}`, status: http.StatusBadRequest, message: notBase64},
		{
			name: "data with a line break", body: `{"data":"` + valid[:8] + `\n` + valid[8:] + `"}`,
			status: http.StatusBadRequest, message: notBase64,
		},
		{
			name: "data in the URL alphabet", body: `{"data":"` + base64.URLEncoding.EncodeToString([]byte{0xfb, 0xff, 0xfe}) + `"}`,
			status: http.StatusBadRequest, message: notBase64,
		},
		{
			name: "a data URL", body: `{"data":"data:image/png;base64,` + valid + `"}`,
			status: http.StatusBadRequest, message: notBase64,
		},
		{
			name: "a name of 65 bytes", body: `{"name":"` + strings.Repeat("n", 65) + `","data":"` + valid + `"}`,
			status: http.StatusUnprocessableEntity, message: badName,
		},
		{
			name: "a name with a control character", body: `{"name":"a\u0000b","data":"` + valid + `"}`,
			status: http.StatusUnprocessableEntity, message: badName,
		},
		{
			name:   "an SVG",
			body:   `{"data":"` + encode([]byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)) + `"}`,
			status: http.StatusUnprocessableEntity, message: notPNG,
		},
		{
			name: "a GIF", body: `{"data":"` + encode([]byte("GIF89a\x01\x00\x01\x00\x00\x00\x00;")) + `"}`,
			status: http.StatusUnprocessableEntity, message: notPNG,
		},
		{
			name: "a PNG cut short", body: `{"data":"` + encode(pixel[:len(pixel)-16]) + `"}`,
			status: http.StatusUnprocessableEntity, message: notPNG,
		},
		{
			name: "97 pixels wide", body: `{"data":"` + encode(builderIconPNG(t, 97, 1, 4)) + `"}`,
			status: http.StatusUnprocessableEntity, message: "icon is 97 x 1 pixels; the limit is 96 x 96",
		},
		{
			name: "97 pixels high", body: `{"data":"` + encode(builderIconPNG(t, 1, 97, 4)) + `"}`,
			status: http.StatusUnprocessableEntity, message: "icon is 1 x 97 pixels; the limit is 96 x 96",
		},
		{
			name: "one byte above the upload limit", body: `{"data":"` + encode(make([]byte, bapi.MaxIconUploadBytes+1)) + `"}`,
			status: http.StatusRequestEntityTooLarge, message: "icon is larger than 65536 bytes",
		},
		{
			name: "a body above its limit", body: `{"data":"` + strings.Repeat("A", builderIconRequestBytes) + `"}`,
			status: http.StatusRequestEntityTooLarge, message: "request body is larger than 131072 bytes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			harness := newBuilderHarness(t)
			logs := plogtest.Capture(t)

			recorder := harness.do(builderRequest{
				method: http.MethodPost, path: builderIconsRoute, body: tt.body, user: builderTestOwner,
			})

			assertIconHeaders(t, tt.name, recorder)

			if recorder.Code != tt.status {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, tt.status, recorder.Body)
			}

			if got := builderMessage(t, recorder); got != tt.message {
				t.Fatalf("message = %q, want %q", got, tt.message)
			}

			if got := harness.store.Count(bapi.NamespaceIcons); got != 0 {
				t.Fatalf("a refused upload left %d records", got)
			}

			// Neither the answer nor the log repeats what was sent.
			for _, sent := range []string{"alert", "onload", valid} {
				if strings.Contains(recorder.Body.String(), sent) || strings.Contains(logs.String(), sent) {
					t.Fatalf("the answer or the log repeats %q of the request: %s | %s", sent, recorder.Body, logs)
				}
			}
		})
	}
}

func TestBuilderIconLibraryIsBounded(t *testing.T) {
	harness := newBuilderHarness(t)

	for i := range bapi.MaxLibraryIcons {
		if _, _, err := harness.service.AddIcon(
			context.Background(), builderTestOwner, fmt.Sprintf("icon %d", i), builderIconPNG(t, 1, 1, i),
		); err != nil {
			t.Fatalf("adding icon %d: %v", i, err)
		}
	}

	recorder := harness.postIcon(builderTestOwner, "one too many", builderIconPNG(t, 1, 1, bapi.MaxLibraryIcons))

	if recorder.Code != http.StatusUnprocessableEntity ||
		builderMessage(t, recorder) != "icon library is full: at most 64 icons" {
		t.Fatalf("the 65th icon = %d %s, want 422 and the library full", recorder.Code, recorder.Body)
	}

	if list := harness.icons(builderTestOwner); len(list.Icons) != bapi.MaxLibraryIcons {
		t.Fatalf("the library lists %d icons, want %d", len(list.Icons), bapi.MaxLibraryIcons)
	}

	// The byte limit: icons of the most bytes fill a library well before it
	// holds the most icons.
	bytesHarness := newBuilderHarness(t)

	var (
		large   []byte
		refused bool
	)

	for seed := 0; seed < bapi.MaxLibraryIcons && !refused; seed++ {
		noise := image.NewNRGBA(image.Rect(0, 0, bdoc.MaxIconPixels, bdoc.MaxIconPixels))
		for i := range noise.Pix {
			noise.Pix[i] = byte((i*31 + seed*17 + i/7*seed) % 251)
		}

		var out bytes.Buffer

		if err := (&png.Encoder{CompressionLevel: png.NoCompression, BufferPool: nil}).Encode(&out, noise); err != nil {
			t.Fatalf("encoding a PNG: %v", err)
		}

		large = out.Bytes()

		recorder := bytesHarness.postIcon(builderTestOwner, "large", large)
		if recorder.Code == http.StatusCreated {
			continue
		}

		if recorder.Code != http.StatusUnprocessableEntity ||
			builderMessage(t, recorder) != "icon library is full: at most 1048576 bytes" {
			t.Fatalf("icon %d = %d %s, want 422 and the library full of bytes", seed, recorder.Code, recorder.Body)
		}

		refused = true
	}

	list := bytesHarness.icons(builderTestOwner)

	if !refused || len(list.Icons) >= bapi.MaxLibraryIcons || list.UsedBytes > list.MaxBytes ||
		list.UsedBytes+len(large) <= list.MaxBytes {
		t.Fatalf("refused = %v: the library holds %d icons and %d bytes, and one of %d more bytes was sent",
			refused, len(list.Icons), list.UsedBytes, len(large))
	}
}

func TestBuilderIconDeleteNotFound(t *testing.T) {
	harness := newBuilderHarness(t)
	upload := builderIconPNG(t, 1, 1, 5)

	if recorder := harness.postIcon(builderTestOwner, "kept", upload); recorder.Code != http.StatusCreated {
		t.Fatalf("adding an icon: status = %d", recorder.Code)
	}

	id := bdoc.IconID(upload)
	_, digits, _ := strings.Cut(id, ":")

	for _, icon := range []string{
		"plc",
		id,
		strings.ToUpper(digits),
		digits[:63],
		digits + "0",
		strings.Repeat("0", 64),
		strings.Repeat("g", 64),
		bapi.OwnerScope(builderTestOwner),
	} {
		recorder := harness.do(builderRequest{
			method: http.MethodDelete, path: builderIconsRoute + "/" + icon, user: builderTestOwner,
		})

		assertIconHeaders(t, icon, recorder)

		if recorder.Code != http.StatusNotFound || builderMessage(t, recorder) != "icon not found" {
			t.Errorf("DELETE %s = %d %s, want 404 icon not found", icon, recorder.Code, recorder.Body)
		}

		// The answer never repeats the path.
		if len(icon) > 8 && strings.Contains(recorder.Body.String(), icon[:8]) {
			t.Errorf("DELETE %s: the answer repeats the path: %s", icon, recorder.Body)
		}
	}

	if list := harness.icons(builderTestOwner); len(list.Icons) != 1 {
		t.Fatalf("the library lists %d icons, want the one that was kept", len(list.Icons))
	}
}

func TestBuilderIconPermissions(t *testing.T) {
	upload := builderIconPNG(t, 1, 1, 6)
	path := builderIconPath(bdoc.IconID(upload))

	role := func(verbs ...string) *rbac.Role {
		return builderShareRole(verbs)
	}

	// Every permission there is but on configs.
	noConfigs := builderRole(builderPolicy(
		[]string{builderDraftsResource, "schemas", "topologies", "experiments", "scenarios"},
		[]string{"*", "*/*"},
		builderShareConfigVerbs,
	))

	tests := []struct {
		name                 string
		role                 *rbac.Role
		anonymous            bool
		list, create, remove int
	}{
		{
			name: "no identity", anonymous: true,
			list: http.StatusForbidden, create: http.StatusForbidden, remove: http.StatusForbidden,
		},
		{
			name: "no permission", role: role(),
			list: http.StatusForbidden, create: http.StatusForbidden, remove: http.StatusForbidden,
		},
		{
			name: "everything but configs", role: &noConfigs,
			list: http.StatusForbidden, create: http.StatusForbidden, remove: http.StatusForbidden,
		},
		{
			name: "configs list", role: role("list"),
			list: http.StatusOK, create: http.StatusForbidden, remove: http.StatusForbidden,
		},
		{
			name: "configs get and update", role: role("get", "update"),
			list: http.StatusForbidden, create: http.StatusForbidden, remove: http.StatusForbidden,
		},
		{
			name: "configs create", role: role("create"),
			list: http.StatusForbidden, create: http.StatusCreated, remove: http.StatusForbidden,
		},
		{
			name: "configs delete", role: role("delete"),
			list: http.StatusForbidden, create: http.StatusForbidden, remove: http.StatusNoContent,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			harness := newBuilderHarness(t)

			// The library holds the icon, so a delete that is allowed finds it.
			if _, _, err := harness.service.AddIcon(context.Background(), builderTestOwner, "", upload); err != nil {
				t.Fatalf("adding the icon: %v", err)
			}

			user := builderTestOwner
			if tt.anonymous {
				user = ""
			}

			other := builderIconPNG(t, 1, 1, 7)

			for _, request := range []struct {
				builderRequest

				want int
			}{
				{builderRequest{method: http.MethodGet, path: builderIconsRoute}, tt.list},
				{builderRequest{method: http.MethodPost, path: builderIconsRoute, body: builderIconBody(t, "", other)}, tt.create},
				{builderRequest{method: http.MethodDelete, path: path}, tt.remove},
			} {
				request.user = user
				request.role = tt.role

				recorder := harness.do(request.builderRequest)

				assertIconHeaders(t, request.method, recorder)

				if recorder.Code != request.want {
					t.Errorf("%s %s: status = %d, want %d: %s", request.method, request.path, recorder.Code, request.want, recorder.Body)
				}
			}
		})
	}
}

// TestBuilderIconOutOfSpace asserts adding an icon etcd refused for lack of
// space says so plainly.
func TestBuilderIconOutOfSpace(t *testing.T) {
	harness := newBuilderHarness(t)

	harness.store.BeforeCreate = func(namespace, key string) error {
		return fmt.Errorf("creating record %s/%s in Etcd: %w", namespace, key, store.ErrNoSpace)
	}

	recorder := harness.postIcon(builderTestOwner, "plc", builderIconPNG(t, 1, 1, 8))
	assertIconHeaders(t, "out of space", recorder)

	if recorder.Code != http.StatusInsufficientStorage || builderMessage(t, recorder) != store.ErrNoSpace.Error() {
		t.Fatalf("adding an icon = %d %s, want 507 and the store's message", recorder.Code, recorder.Body)
	}

	harness.store.BeforeCreate = nil

	if recorder := harness.postIcon(builderTestOwner, "plc", builderIconPNG(t, 1, 1, 8)); recorder.Code != http.StatusCreated {
		t.Fatalf("adding it after freeing space: status = %d, want %d", recorder.Code, http.StatusCreated)
	}
}

// TestBuilderResponseHeaders asserts every response of every Builder route
// tells the browser not to guess a content type, that the icon library's
// also forbid loading, running and framing anything, and that no other
// route of the API router is touched.
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
		for _, user := range []string{builderTestOwner, ""} {
			recorder := harness.do(builderRequest{method: op.method, path: path, body: `{}`, user: user})
			what := fmt.Sprintf("%s %s as %q (%d)", op.method, op.template, user, recorder.Code)
			header := recorder.Header()

			switch {
			case strings.HasPrefix(op.template, builderIconsRoute):
				icons++

				assertIconHeaders(t, what, recorder)
			case strings.HasPrefix(op.template, "/builder/"), op.template == "/schemas/builder/v1":
				builder++

				if got := header.Get("X-Content-Type-Options"); got != builderNoSniff {
					t.Errorf("%s: X-Content-Type-Options = %q, want %q", what, got, builderNoSniff)
				}

				if got := header.Get("Content-Security-Policy"); got != "" {
					t.Errorf("%s: Content-Security-Policy = %q, want none outside the icon library", what, got)
				}
			default:
				others++

				if len(header.Values("X-Content-Type-Options")) != 0 || len(header.Values("Content-Security-Policy")) != 0 {
					t.Errorf("%s: a route outside the Builder carries its headers: %v", what, header)
				}
			}
		}
	}

	// Both users, for each method of each route: the icon library has three
	// operations and their OPTIONS, the rest of the Builder 23 and theirs
	// (the cursor's two methods share one).
	if icons != 2*6 || builder < 2*45 || others != 2 {
		t.Fatalf("checked %d icon, %d other Builder and %d other responses", icons, builder, others)
	}

	// The routes that answer with what a user uploaded or drew are among
	// them, whichever file registers them.
	for _, want := range []operation{
		{method: http.MethodPost, template: "/builder/legacy"},
		{method: http.MethodPost, template: "/builder/generate"},
		{method: http.MethodPost, template: "/builder/export/topology"},
		{method: http.MethodGet, template: "/builder/documents/{document}"},
		{method: http.MethodGet, template: "/builder/drafts/{owner}/{draft}"},
		{method: http.MethodGet, template: "/builder/templates"},
		{method: http.MethodPost, template: "/builder/templates/{owner}/items"},
	} {
		if !slices.Contains(operations, want) {
			t.Errorf("%s %s was not checked: it is not a registered route", want.method, want.template)
		}
	}

	// The headers are set before the request is answered, so they are also
	// on what a middleware registered later, as authentication is, answers
	// in place of the route.
	harness.api.Use(func(http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "missing phenix auth token header", http.StatusUnauthorized)
		})
	})

	refused := harness.do(builderRequest{method: http.MethodGet, path: builderIconsRoute, user: builderTestOwner})

	if refused.Code != http.StatusUnauthorized ||
		refused.Header().Get("X-Content-Type-Options") != builderNoSniff ||
		refused.Header().Get("Content-Security-Policy") != builderIconCSP {
		t.Fatalf("a request refused before the route = %d %v, want 401 with both headers", refused.Code, refused.Header())
	}
}
