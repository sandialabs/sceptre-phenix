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
	"phenix/store/recordtest/memrecord"
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

	// builderIconCharset is how a refused icon name ends.
	builderIconCharset = `must be 1 to 64 letters, digits, "_", "@", "." or "-"`
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

// postIcon adds an icon, as the user with builderConfigsRole, which renames
// and deletes only the icons its user uploaded.
func (h *builderHarness) postIcon(user, name string, data []byte) *httptest.ResponseRecorder {
	h.t.Helper()

	return h.do(builderRequest{
		method: http.MethodPost, path: builderIconsRoute, body: builderIconBody(h.t, name, data), user: user,
		role: builderConfigsRole(),
	})
}

// iconRequest makes a request of one icon, as the user with role, and checks
// its headers.
func (h *builderHarness) iconRequest(method, name, body, user string, role *rbac.Role) *httptest.ResponseRecorder {
	h.t.Helper()

	recorder := h.do(builderRequest{method: method, path: builderIconsRoute + "/" + name, body: body, user: user, role: role})

	assertIconHeaders(h.t, method+" "+name, recorder)

	return recorder
}

// icons returns the icon library, as the list route answers the user with
// role.
func (h *builderHarness) icons(user string, role *rbac.Role) builderIconListResponse {
	h.t.Helper()

	recorder := h.do(builderRequest{method: http.MethodGet, path: builderIconsRoute, user: user, role: role})
	if recorder.Code != http.StatusOK {
		h.t.Fatalf("listing icons: status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
	}

	var list builderIconListResponse

	h.decode(recorder, &list)

	return list
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
		t.Errorf("%s: ETag = %q, want none", what, got)
	}
}

// addBuilderIcon adds an icon as alice, with builderConfigsRole, asserts it was
// created, and returns it as the answer gives it.
func addBuilderIcon(t *testing.T, harness *builderHarness, name string, upload []byte) builderIconResponse {
	t.Helper()

	created := harness.postIcon(builderTestOwner, name, upload)
	assertIconHeaders(t, "a new icon", created)

	if created.Code != http.StatusCreated {
		t.Fatalf("adding an icon: status = %d, want %d: %s", created.Code, http.StatusCreated, created.Body)
	}

	var icon builderIconResponse

	harness.decode(created, &icon)

	return icon
}

func TestBuilderIconLibrary(t *testing.T) {
	harness := newBuilderHarness(t)
	user := builderConfigsRole()

	// A raw read, so an empty library is seen to be a list and not null.
	empty := harness.do(builderRequest{method: http.MethodGet, path: builderIconsRoute, user: builderTestOwner, role: user})
	assertIconHeaders(t, "the empty list", empty)

	if got, want := strings.TrimSpace(empty.Body.String()),
		`{"icons":[],"maxIcons":64,"maxBytes":1048576,"usedBytes":0,"usedIcons":0}`; empty.Code != http.StatusOK || got != want {
		t.Fatalf("the empty list = %d %s, want %s", empty.Code, got, want)
	}

	upload := builderIconPNG(t, 3, 2, 1)
	data := base64.StdEncoding.EncodeToString(upload)
	icon := addBuilderIcon(t, harness, "PLC", upload)

	want := builderIconResponse{
		Name: "PLC", ID: bdoc.IconID(upload), Owner: builderTestOwner, Width: 3, Height: 2, Bytes: len(upload),
		Created: icon.Created, Updated: icon.Created, Aliases: []string{}, Data: data, CanRename: true, CanDelete: true,
	}

	if fmt.Sprint(icon) != fmt.Sprint(want) || icon.Created.IsZero() {
		t.Fatalf("the new icon = %+v, want %+v", icon, want)
	}

	// A document accepts the icon under its name.
	if issues := bdoc.ValidateIcons(map[string]bdoc.Icon{icon.Name: {Data: icon.Data}}, "icons"); len(issues) != 0 {
		t.Fatalf("a document refuses the icon: %v", issues)
	}
}

// TestBuilderIconNameIsTaken asserts the same bytes under the same name, in
// any case, are the icon the library has, and other bytes are refused,
// naming who has the name.
func TestBuilderIconNameIsTaken(t *testing.T) {
	harness := newBuilderHarness(t)
	user := builderConfigsRole()
	upload := builderIconPNG(t, 3, 2, 1)

	addBuilderIcon(t, harness, "PLC", upload)

	again := harness.postIcon(builderTestPeer, "plc", upload)
	assertIconHeaders(t, "an icon the library has", again)

	var same builderIconResponse

	harness.decode(again, &same)

	if again.Code != http.StatusOK || same.Name != "PLC" || same.Owner != builderTestOwner || same.CanRename || same.CanDelete {
		t.Fatalf("adding the same bytes = %d %+v, want 200 and alice's icon, which bob may not change", again.Code, same)
	}

	taken := harness.postIcon(builderTestPeer, "Plc", builderIconPNG(t, 3, 2, 2))
	assertIconHeaders(t, "a taken name", taken)

	if taken.Code != http.StatusConflict ||
		builderMessage(t, taken) != `icon name "Plc" is taken by an icon alice uploaded; choose another name` {
		t.Fatalf("other bytes under a taken name = %d %s, want 409", taken.Code, taken.Body)
	}

	if got := harness.iconRequest(http.MethodGet, "plc", "", builderTestPeer, user); got.Code != http.StatusOK {
		t.Fatalf("reading the icon by its name in another case = %d %s", got.Code, got.Body)
	}
}

// TestBuilderIconRenameAndDelete asserts a renamed icon keeps its old name
// as an alias, that a delete through either name removes the icon and both
// names, and that each change is logged without the image.
func TestBuilderIconRenameAndDelete(t *testing.T) {
	harness := newBuilderHarness(t)
	logs := plogtest.Capture(t)
	user := builderConfigsRole()
	upload := builderIconPNG(t, 3, 2, 1)
	data := base64.StdEncoding.EncodeToString(upload)
	icon := addBuilderIcon(t, harness, "PLC", upload)

	renamed := harness.iconRequest(http.MethodPut, "plc", `{"name":"plc-2"}`, builderTestOwner, user)

	var moved builderIconResponse

	harness.decode(renamed, &moved)

	if renamed.Code != http.StatusOK || moved.Name != "plc-2" || !slices.Equal(moved.Aliases, []string{"PLC"}) ||
		moved.ID != icon.ID || !moved.Updated.After(moved.Created) {
		t.Fatalf("renaming the icon = %d %s", renamed.Code, renamed.Body)
	}

	// The old name keeps naming it.
	var byAlias builderIconResponse

	harness.decode(harness.iconRequest(http.MethodGet, "PLC", "", builderTestPeer, user), &byAlias)

	if byAlias.Name != "plc-2" || byAlias.Data != data {
		t.Fatalf("the old name names %+v, want the renamed icon", byAlias)
	}

	list := harness.icons(builderTestOwner, user)

	if len(list.Icons) != 1 || list.Icons[0].Name != "plc-2" || list.MaxIcons != bapi.MaxLibraryIcons ||
		list.MaxBytes != bapi.MaxLibraryIconBytes || list.UsedBytes != len(upload) || list.UsedIcons != 1 {
		t.Fatalf("the list = %+v, want the icon and alice's usage", list)
	}

	if peer := harness.icons(builderTestPeer, user); peer.UsedIcons != 0 || peer.UsedBytes != 0 || len(peer.Icons) != 1 {
		t.Fatalf("bob's list = %+v, want the icon and none of his own", peer)
	}

	// Deleting through the old name deletes the icon and both its names.
	deleted := harness.iconRequest(http.MethodDelete, "PLC", "", builderTestOwner, user)

	if deleted.Code != http.StatusNoContent || deleted.Body.Len() != 0 {
		t.Fatalf("deleting the icon = %d %q, want an empty 204", deleted.Code, deleted.Body)
	}

	if list := harness.icons(builderTestOwner, user); len(list.Icons) != 0 || list.UsedBytes != 0 ||
		harness.store.Count(bapi.NamespaceIcons) != 0 {
		t.Fatalf("the list after the delete = %+v, want it empty", list)
	}

	gone := harness.iconRequest(http.MethodDelete, "plc-2", "", builderTestOwner, user)
	if gone.Code != http.StatusNotFound || builderMessage(t, gone) != "icon not found" {
		t.Fatalf("deleting it again = %d %s, want 404 icon not found", gone.Code, gone.Body)
	}

	assertIconChangesLogged(t, logs, data)
}

// assertIconChangesLogged asserts the log says once who added, renamed and
// deleted an icon, and never holds the image, whose base64 is data.
func assertIconChangesLogged(t *testing.T, logs *plogtest.Logs, data string) {
	t.Helper()

	for _, message := range []string{"added builder icon", "renamed builder icon", "deleted builder icon"} {
		if len(logs.Records(t, plogtest.Message(message))) != 1 {
			t.Errorf("the log does not say %q once: %s", message, logs)
		}
	}

	if strings.Contains(logs.String(), data) || strings.Contains(logs.String(), data[:24]) {
		t.Errorf("the log holds the image: %s", logs)
	}
}

// TestBuilderIconLibraryIsShared asserts every user sees every icon, with
// who uploaded it, and that only its uploader or a holder of the
// builder-icons permissions renames or deletes it.
func TestBuilderIconLibraryIsShared(t *testing.T) {
	harness := newBuilderHarness(t)
	user, admin := builderConfigsRole(), builderIconAdmin()

	if recorder := harness.postIcon(builderTestOwner, "mine", builderIconPNG(t, 2, 2, 9)); recorder.Code != http.StatusCreated {
		t.Fatalf("adding an icon: status = %d, want %d", recorder.Code, http.StatusCreated)
	}

	peer := harness.icons(builderTestPeer, user)

	if len(peer.Icons) != 1 || peer.Icons[0].Name != "mine" || peer.Icons[0].Owner != builderTestOwner ||
		peer.Icons[0].CanRename || peer.Icons[0].CanDelete {
		t.Fatalf("bob's list = %+v, want alice's icon, which he may not change", peer)
	}

	if own := harness.icons(builderTestOwner, user); !own.Icons[0].CanRename || !own.Icons[0].CanDelete {
		t.Fatalf("alice's list = %+v, want her icon, which she may change", own)
	}

	if listed := harness.icons(builderTestPeer, admin); !listed.Icons[0].CanRename || !listed.Icons[0].CanDelete {
		t.Fatalf("the list of a holder of builder-icons = %+v, want the icon, which it may change", listed)
	}

	logs := plogtest.Capture(t)

	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		recorder := harness.iconRequest(method, "mine", `{"name":"theirs"}`, builderTestPeer, user)

		if recorder.Code != http.StatusForbidden {
			t.Errorf("%s of another user's icon = %d %s, want 403", method, recorder.Code, recorder.Body)
		}
	}

	if refused := logs.Records(t, plogtest.Message("builder request not allowed")); len(refused) != 2 {
		t.Errorf("logged %d refusals, want 2", len(refused))
	}

	if names := harness.icons(builderTestOwner, user); names.Icons[0].Name != "mine" || harness.store.Count(bapi.NamespaceIcons) != 1 {
		t.Fatalf("a refused request changed the library: %+v", names)
	}

	if renamed := harness.iconRequest(http.MethodPut, "mine", `{"name":"theirs"}`, builderTestPeer, admin); renamed.Code != http.StatusOK {
		t.Fatalf("renaming with builder-icons update = %d %s", renamed.Code, renamed.Body)
	}

	if deleted := harness.iconRequest(http.MethodDelete, "mine", "", builderTestPeer, admin); deleted.Code != http.StatusNoContent {
		t.Fatalf("deleting with builder-icons delete = %d %s", deleted.Code, deleted.Body)
	}

	if got := harness.store.Count(bapi.NamespaceIcons); got != 0 {
		t.Fatalf("%d icon records after the delete, want 0", got)
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
		t.Fatalf("the stored icon = %+v, want the pixels of the upload alone", icon)
	}

	if _, _, err := bdoc.ValidateIconPNG(stored); err != nil {
		t.Fatalf("the stored icon is not one a document accepts: %v", err)
	}

	// The same upload under the same name again finds the stored icon.
	if again := harness.postIcon(builderTestOwner, "loose", loose); again.Code != http.StatusOK {
		t.Fatalf("the same upload again: status = %d, want %d", again.Code, http.StatusOK)
	}

	// One record, which holds the stored bytes and nothing else of the
	// upload.
	keys := harness.store.Keys(bapi.NamespaceIcons)
	if !slices.Equal(keys, []string{"name/loose"}) {
		t.Fatalf("icon records = %q, want name/loose", keys)
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
			name: "an unknown field", body: `{"name":"plc","data":"` + valid + `","owner":"bob"}`,
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
			name: "no name", body: `{"data":"` + valid + `"}`,
			status: http.StatusUnprocessableEntity, message: `icon name "" ` + builderIconCharset,
		},
		{
			name: "a name of 65 bytes", body: `{"name":"` + strings.Repeat("n", 65) + `","data":"` + valid + `"}`,
			status: http.StatusUnprocessableEntity, message: `icon name "` + strings.Repeat("n", 64) + `..." ` + builderIconCharset,
		},
		{
			name: "a name with a control character", body: `{"name":"a\u0000b","data":"` + valid + `"}`,
			status: http.StatusUnprocessableEntity, message: `icon name "a\x00b" ` + builderIconCharset,
		},
		{
			name: "a name with a space", body: `{"name":"plc icon","data":"` + valid + `"}`,
			status: http.StatusUnprocessableEntity, message: `icon name "plc icon" ` + builderIconCharset,
		},
		{
			name: "a name of one dot", body: `{"name":".","data":"` + valid + `"}`,
			status: http.StatusUnprocessableEntity, message: `icon name "." must not be "." or ".."`,
		},
		{
			name:   "an SVG",
			body:   named(encode([]byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`))),
			status: http.StatusUnprocessableEntity, message: notPNG,
		},
		{
			name: "a GIF", body: named(encode([]byte("GIF89a\x01\x00\x01\x00\x00\x00\x00;"))),
			status: http.StatusUnprocessableEntity, message: notPNG,
		},
		{name: "a PNG cut short", body: named(encode(pixel[:len(pixel)-16])), status: http.StatusUnprocessableEntity, message: notPNG},
		{
			name: "97 pixels wide", body: named(encode(builderIconPNG(t, 97, 1, 4))),
			status: http.StatusUnprocessableEntity, message: "icon is 97 x 1 pixels; the limit is 96 x 96",
		},
		{
			name: "97 pixels high", body: named(encode(builderIconPNG(t, 1, 97, 4))),
			status: http.StatusUnprocessableEntity, message: "icon is 1 x 97 pixels; the limit is 96 x 96",
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

			// Neither the answer nor the log repeats the image that was sent.
			for _, sent := range []string{"alert", "onload", valid} {
				if strings.Contains(recorder.Body.String(), sent) || strings.Contains(logs.String(), sent) {
					t.Fatalf("the answer or the log repeats %q of the request: %s | %s", sent, recorder.Body, logs)
				}
			}
		})
	}
}

// TestBuilderIconRename asserts the answers of a rename: the new name, also
// a change of case; a taken name; a name that is none; a request that is
// not one.
func TestBuilderIconRename(t *testing.T) {
	harness := newBuilderHarness(t)
	user := builderConfigsRole()

	for name, seed := range map[string]int{"plc": 1, "hmi": 2} {
		if recorder := harness.postIcon(builderTestOwner, name, builderIconPNG(t, 1, 1, seed)); recorder.Code != http.StatusCreated {
			t.Fatalf("adding %s: status = %d", name, recorder.Code)
		}
	}

	for _, tt := range []struct {
		name, icon, body string
		status           int
		message          string
	}{
		{
			name: "a taken name", icon: "plc", body: `{"name":"HMI"}`, status: http.StatusConflict,
			message: `icon name "HMI" is taken by an icon alice uploaded; choose another name`,
		},
		{
			name: "a name that is none", icon: "plc", body: `{"name":"p l c"}`, status: http.StatusUnprocessableEntity,
			message: `icon name "p l c" ` + builderIconCharset,
		},
		{name: "no name", icon: "plc", body: `{}`, status: http.StatusUnprocessableEntity, message: `icon name "" ` + builderIconCharset},
		{name: "an unknown field", icon: "plc", body: `{"name":"x","owner":"bob"}`, status: http.StatusBadRequest, message: "request body is not a valid Builder request"},
		{
			name: "a body above its limit", icon: "plc", body: `{"name":"` + strings.Repeat("n", builderIconRenameBytes) + `"}`,
			status: http.StatusRequestEntityTooLarge, message: "request body is larger than 4096 bytes",
		},
		{name: "an icon nobody has", icon: "scada", body: `{"name":"x"}`, status: http.StatusNotFound, message: "icon not found"},
	} {
		recorder := harness.iconRequest(http.MethodPut, tt.icon, tt.body, builderTestOwner, user)

		if recorder.Code != tt.status || builderMessage(t, recorder) != tt.message {
			t.Errorf("%s: %d %s, want %d %q", tt.name, recorder.Code, recorder.Body, tt.status, tt.message)
		}
	}

	if keys := harness.store.Keys(bapi.NamespaceIcons); !slices.Equal(keys, []string{"name/hmi", "name/plc"}) {
		t.Fatalf("refused renames changed the records to %q", keys)
	}

	cased := harness.iconRequest(http.MethodPut, "plc", `{"name":"PLC"}`, builderTestOwner, user)

	var icon builderIconResponse

	harness.decode(cased, &icon)

	if cased.Code != http.StatusOK || icon.Name != "PLC" || len(icon.Aliases) != 0 {
		t.Fatalf("a change of case = %d %s", cased.Code, cased.Body)
	}
}

func TestBuilderIconLibraryIsBounded(t *testing.T) {
	harness := newBuilderHarness(t)

	for i := range bapi.MaxLibraryIcons {
		if _, _, err := harness.service.AddIcon(
			context.Background(), builderTestOwner, fmt.Sprintf("icon-%d", i), builderIconPNG(t, 1, 1, i),
		); err != nil {
			t.Fatalf("adding icon %d: %v", i, err)
		}
	}

	recorder := harness.postIcon(builderTestOwner, "one-too-many", builderIconPNG(t, 1, 1, bapi.MaxLibraryIcons))

	if recorder.Code != http.StatusUnprocessableEntity ||
		builderMessage(t, recorder) != "icon library is full for you: each user may upload at most 64 icons" {
		t.Fatalf("the 65th icon = %d %s, want 422 and the library full for alice", recorder.Code, recorder.Body)
	}

	if list := harness.icons(builderTestOwner, builderConfigsRole()); len(list.Icons) != bapi.MaxLibraryIcons ||
		list.UsedIcons != bapi.MaxLibraryIcons {
		t.Fatalf("the library lists %d icons, %d of alice's, want %d", len(list.Icons), list.UsedIcons, bapi.MaxLibraryIcons)
	}

	// Another user's uploads are their own.
	if recorder := harness.postIcon(builderTestPeer, "theirs", builderIconPNG(t, 1, 1, 999)); recorder.Code != http.StatusCreated {
		t.Fatalf("bob's icon = %d %s, want 201", recorder.Code, recorder.Body)
	}

	// The byte limit: icons of the most bytes fill a user's share well
	// before it holds the most icons.
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

		recorder := bytesHarness.postIcon(builderTestOwner, fmt.Sprintf("large-%d", seed), large)
		if recorder.Code == http.StatusCreated {
			continue
		}

		if recorder.Code != http.StatusUnprocessableEntity ||
			builderMessage(t, recorder) != "icon library is full for you: the icons each user uploads may take at most 1048576 bytes" {
			t.Fatalf("icon %d = %d %s, want 422 and the library full of alice's bytes", seed, recorder.Code, recorder.Body)
		}

		refused = true
	}

	list := bytesHarness.icons(builderTestOwner, builderConfigsRole())

	if !refused || len(list.Icons) >= bapi.MaxLibraryIcons || list.UsedBytes > list.MaxBytes ||
		list.UsedBytes+len(large) <= list.MaxBytes {
		t.Fatalf("refused = %v: the library holds %d icons and %d bytes, and one of %d more bytes was sent",
			refused, len(list.Icons), list.UsedBytes, len(large))
	}
}

func TestBuilderIconNotFound(t *testing.T) {
	harness := newBuilderHarness(t)
	upload := builderIconPNG(t, 1, 1, 5)

	if recorder := harness.postIcon(builderTestOwner, "kept", upload); recorder.Code != http.StatusCreated {
		t.Fatalf("adding an icon: status = %d", recorder.Code)
	}

	_, digits, _ := strings.Cut(bdoc.IconID(upload), ":")

	for _, icon := range []string{
		"missing",
		digits,
		"sha256:" + digits,
		"kept%20",
		"a%20b",
		bapi.OwnerScope(builderTestOwner),
	} {
		for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
			recorder := harness.iconRequest(method, icon, `{"name":"renamed"}`, builderTestOwner, builderIconAdmin())

			if recorder.Code != http.StatusNotFound || builderMessage(t, recorder) != "icon not found" {
				t.Errorf("%s %s = %d %s, want 404 icon not found", method, icon, recorder.Code, recorder.Body)
			}

			// The answer never repeats the path.
			if len(icon) > 8 && strings.Contains(recorder.Body.String(), icon[:8]) {
				t.Errorf("%s %s: the answer repeats the path: %s", method, icon, recorder.Body)
			}
		}
	}

	if list := harness.icons(builderTestOwner, builderConfigsRole()); len(list.Icons) != 1 || list.Icons[0].Name != "kept" {
		t.Fatalf("the library lists %+v, want the one that was kept", list.Icons)
	}
}

// TestBuilderIconPermissions asks each route of the icon library as each
// caller of a permission matrix: each route takes the configs permission of
// its verb, and nothing else does.
func TestBuilderIconPermissions(t *testing.T) {
	upload := builderIconPNG(t, 1, 1, 6)
	other := builderIconPNG(t, 1, 1, 7)

	role := func(verbs ...string) *rbac.Role {
		return builderShareRole(verbs)
	}

	// The statuses of list, get, create, rename and delete: none allows no
	// request, and only allows the one at index with status.
	none := slices.Repeat([]int{http.StatusForbidden}, 5)
	only := func(index, status int) []int {
		want := slices.Clone(none)
		want[index] = status

		return want
	}

	runBuilderVerbMatrix(t, []builderVerbCaller{
		{name: "no identity", anonymous: true, want: none},
		{name: "no permission", role: role(), want: none},
		{name: "everything but configs", role: builderAllButConfigsRole("builder-icons", builderShareConfigVerbs), want: none},
		{name: "configs list", role: role("list"), want: only(0, http.StatusOK)},
		{name: "configs get", role: role("get"), want: only(1, http.StatusOK)},
		{name: "configs create", role: role("create"), want: only(2, http.StatusCreated)},
		{name: "configs update", role: role("update"), want: only(3, http.StatusOK)},
		{name: "configs delete", role: role("delete"), want: only(4, http.StatusNoContent)},
	}, func(t *testing.T) (*builderHarness, []builderRequest) {
		t.Helper()

		harness := newBuilderHarness(t)

		// The library holds the caller's icon, so a request that is allowed
		// finds one the caller may change.
		if _, _, err := harness.service.AddIcon(context.Background(), builderTestOwner, "plc", upload); err != nil {
			t.Fatalf("adding the icon: %v", err)
		}

		return harness, []builderRequest{
			{method: http.MethodGet, path: builderIconsRoute},
			{method: http.MethodGet, path: builderIconsRoute + "/plc"},
			{method: http.MethodPost, path: builderIconsRoute, body: builderIconBody(t, "other", other)},
			{method: http.MethodPut, path: builderIconsRoute + "/plc", body: `{"name":"plc-2"}`},
			{method: http.MethodDelete, path: builderIconsRoute + "/plc"},
		}
	}, assertIconHeaders)
}

// TestBuilderIconOutOfSpace asserts adding or renaming an icon etcd refused
// for lack of space says so plainly.
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

	harness.store.BeforeCreate = func(namespace, key string) error {
		return fmt.Errorf("creating record %s/%s in Etcd: %w", namespace, key, store.ErrNoSpace)
	}

	renamed := harness.iconRequest(http.MethodPut, "plc", `{"name":"plc-2"}`, builderTestOwner, builderConfigsRole())

	if renamed.Code != http.StatusInsufficientStorage || builderMessage(t, renamed) != store.ErrNoSpace.Error() {
		t.Fatalf("renaming an icon = %d %s, want 507 and the store's message", renamed.Code, renamed.Body)
	}
}

// TestBuilderIconStartupCleanup asserts a server start removes the icon
// records of the per-user layout of earlier builds, and keeps the library.
func TestBuilderIconStartupCleanup(t *testing.T) {
	fake := memrecord.New()

	service, err := bapi.New(bapi.WithStore(fake))
	if err != nil {
		t.Fatalf("bapi.New returned error: %v", err)
	}

	if _, _, err := service.AddIcon(context.Background(), builderTestOwner, "kept", builderIconPNG(t, 1, 1, 3)); err != nil {
		t.Fatalf("adding an icon: %v", err)
	}

	legacy := bapi.OwnerScope(builderTestOwner) + "/" + strings.Repeat("b", 64)

	if _, err := fake.CreateRecord(bapi.NamespaceIcons, legacy, []byte(`{"id":"x"}`)); err != nil {
		t.Fatalf("planting a record of the per-user layout: %v", err)
	}

	logs := plogtest.Capture(t)
	_, api := newBuilderRouter()

	if err := registerBuilderRoutes(api,
		withBuilderService(service),
		withBuilderConfigs(
			func(string) (store.Configs, error) { return store.Configs{}, nil },
			func(string) (*store.Config, error) { return nil, store.ErrNotExist },
		),
	); err != nil {
		t.Fatalf("registerBuilderRoutes returned error: %v", err)
	}

	if keys := fake.Keys(bapi.NamespaceIcons); !slices.Equal(keys, []string{"name/kept"}) {
		t.Fatalf("after the start the icon records are %q, want name/kept alone", keys)
	}

	if len(logs.Records(t, plogtest.Message("removed builder icon records of the per-user layout"))) != 1 {
		t.Fatalf("the start does not log the removal: %s", logs)
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
			case strings.HasPrefix(op.template, "/builder/"), op.template == "/schemas/builder/v1",
				op.template == "/schemas/builder/templates/v1", op.template == "/schemas/builder/package/v1":
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

	// Both users, for each method of each route: the icon library has five
	// operations and their OPTIONS, the rest of the Builder 23 and theirs
	// (the cursor's two methods share one).
	if icons != 2*10 || builder < 2*45 || others != 2 {
		t.Fatalf("checked %d icon, %d other Builder and %d other responses", icons, builder, others)
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
		{method: http.MethodGet, template: "/builder/drafts/{owner}/{draft}"},
		{method: http.MethodGet, template: "/builder/templates"},
		{method: http.MethodPost, template: "/builder/templates/{owner}/items"},
		{method: http.MethodGet, template: "/builder/icons/{icon}"},
	} {
		if !slices.Contains(operations, want) {
			t.Errorf("%s %s was not checked: it is not a registered route", want.method, want.template)
		}
	}

	// A request under the Builder's paths that matches no route, or no
	// method of a route, is answered by the router, with the same headers;
	// one outside them, with neither.
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
		{http.MethodPatch, builderIconsRoute, http.StatusMethodNotAllowed, true, true},
		{http.MethodPost, builderIconsRoute + "/x", http.StatusMethodNotAllowed, true, true},
		{http.MethodGet, builderIconsRoute + "/x/y", http.StatusNotFound, true, true},
		{http.MethodGet, "/no-such-route", http.StatusNotFound, false, false},
		{http.MethodPatch, "/configs/x/x", http.StatusMethodNotAllowed, false, false},
	} {
		recorder := harness.do(builderRequest{method: tt.method, path: tt.path, user: builderTestOwner})
		what := fmt.Sprintf("%s %s (%d)", tt.method, tt.path, recorder.Code)
		header := recorder.Header()

		if recorder.Code != tt.status {
			t.Errorf("%s: status = %d, want %d", what, recorder.Code, tt.status)
		}

		if got, want := header.Get("X-Content-Type-Options"), map[bool]string{true: builderNoSniff}[tt.builder]; got != want {
			t.Errorf("%s: X-Content-Type-Options = %q, want %q", what, got, want)
		}

		if got, want := header.Get("Content-Security-Policy"), map[bool]string{true: builderIconCSP}[tt.iconsCSP]; got != want {
			t.Errorf("%s: Content-Security-Policy = %q, want %q", what, got, want)
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
