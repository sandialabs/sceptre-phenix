package web

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	bapi "phenix/api/builder"
	bdoc "phenix/types/builder"
	"phenix/util/plog/plogtest"
	"phenix/web/rbac"
)

// builderTemplateFileIcon is a 1 by 1 PNG, in base64, which the test
// template file carries.
const builderTemplateFileIcon = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="

// newBuilderTemplateFilesHarness returns a harness whose server read the
// template files of a directory at start: two collections, the second
// carrying a custom icon, and a file that is not a template file.
func newBuilderTemplateFilesHarness(t *testing.T) *builderHarness {
	t.Helper()

	directory := t.TempDir()

	files := map[string]string{
		"nlr.yaml": "$schema: " + bdoc.TemplateFileSchemaURI + "\n" +
			"name: NLR Node Templates\n" +
			"templates:\n" +
			"  - name: PLC\n" +
			"    device:\n" +
			"      spec:\n" +
			"        general:\n" +
			"          hostname: plc\n" +
			"  - name: HMI\n" +
			"    device:\n" +
			"      spec:\n" +
			"        general:\n" +
			"          hostname: hmi\n",
		"sandia.json": `{"$schema": "` + bdoc.TemplateFileSchemaURI + `", "name": "Sandia Node Templates",` +
			` "description": "Sandia's devices", "templates": [{"name": "RTU", "device": {"icon": "rtu-icon",` +
			` "spec": {"general": {"hostname": "rtu"}}}}], "icons": {"rtu-icon": {"data": "` +
			builderTemplateFileIcon + `"}}}`,
		"broken.yaml": "name: no schema\n",
	}

	for name, text := range files {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(text), 0o600); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}

	return newBuilderHarnessWith(t, []builderOption{withBuilderTemplateFiles(directory)})
}

// TestBuilderTemplatesListPreloadedCollections asserts the listing gives
// every caller the collections the server read from its template files, as
// their own read-only source, and that the icon a file carries was added to
// the icon library as the server's.
func TestBuilderTemplatesListPreloadedCollections(t *testing.T) {
	harness := newBuilderTemplateFilesHarness(t)

	viewer := builderRole(builderPolicy([]string{"configs"}, []string{"*", "*/*"}, []string{"list"}))

	for _, request := range []builderRequest{
		{method: http.MethodGet, path: builderTemplatesRoute, user: builderTestOwner},
		{method: http.MethodGet, path: builderTemplatesRoute, user: builderTestPeer, role: &viewer},
	} {
		recorder := harness.do(request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("listing as %s: status = %d: %s", request.user, recorder.Code, recorder.Body)
		}

		var list builderTemplateLibraryResponse

		harness.decode(recorder, &list)

		if len(list.Preloaded) != 2 {
			t.Fatalf("preloaded = %+v, want the two template files", list.Preloaded)
		}

		nlr, sandia := &list.Preloaded[0], &list.Preloaded[1]

		if nlr.Collection.Name != "NLR Node Templates" || sandia.Collection.Name != "Sandia Node Templates" ||
			sandia.Collection.Description != "Sandia's devices" {
			t.Fatalf("collections = %+v, %+v", nlr.Collection, sandia.Collection)
		}

		for i := range list.Preloaded {
			entry := &list.Preloaded[i]
			collection := &entry.Collection

			if collection.Source != builderTemplatePreloaded || collection.Owner != "" || collection.Version != 1 ||
				collection.ETag != `"1"` || collection.ServerWide || !strings.HasPrefix(collection.ID, "server-") {
				t.Errorf("collection %+v is not a read-only server collection", collection)
			}

			ids := make([]string, 0, len(entry.Templates))

			for j := range entry.Templates {
				template := &entry.Templates[j]

				ids = append(ids, template.ID)

				if template.Source != builderTemplatePreloaded || template.Owner != "" ||
					!slices.Equal(template.Collections, []string{collection.ID}) || template.Shares != nil {
					t.Errorf("template %+v is not a read-only template of %s", template, collection.ID)
				}
			}

			if !slices.Equal(ids, collection.TemplateIDs) {
				t.Errorf("collection %s names %q, and lists templates %q", collection.ID, collection.TemplateIDs, ids)
			}
		}

		if names := []string{nlr.Templates[0].Name, nlr.Templates[1].Name}; !slices.Equal(names, []string{"PLC", "HMI"}) {
			t.Errorf("NLR templates = %q, want PLC then HMI", names)
		}

		if sandia.Templates[0].Device.Icon != "rtu-icon" {
			t.Errorf("RTU names icon %q, want rtu-icon", sandia.Templates[0].Device.Icon)
		}

		// The caller's own library is listed as before, apart from them.
		if len(list.Collections) != 0 || len(list.Templates) != len(builderBuiltinIDs) {
			t.Errorf("own items = %d templates and %d collections, want the built-in templates only",
				len(list.Templates), len(list.Collections))
		}
	}

	icon, err := harness.service.GetIcon(context.Background(), "rtu-icon")
	if err != nil || icon.Owner != bapi.ServerIconOwner || icon.Data != builderTemplateFileIcon {
		t.Fatalf("GetIcon(rtu-icon) = %+v, %v; want the file's icon, owned by no user", icon, err)
	}
}

// TestBuilderTemplateFileIconsBelongToNoUser asserts an icon the server
// added from a template file is listed with no owner and is no account's:
// an account named phenix may neither rename nor delete it without the
// builder-icons permissions, and is counted none of its bytes; with them, it
// may do both.
func TestBuilderTemplateFileIconsBelongToNoUser(t *testing.T) {
	harness := newBuilderTemplateFilesHarness(t)

	const account = "phenix"

	listed := func(role *rbac.Role) builderIconResponse {
		t.Helper()

		list := harness.icons(account, role)

		if list.UsedIcons != 0 || list.UsedBytes != 0 {
			t.Errorf("%s is counted %d icons of %d bytes, want none", account, list.UsedIcons, list.UsedBytes)
		}

		for _, icon := range list.Icons {
			if icon.Name == "rtu-icon" {
				return icon
			}
		}

		t.Fatalf("the library lists no rtu-icon: %+v", list.Icons)

		return builderIconResponse{}
	}

	if icon := listed(builderIconUser()); icon.Owner != "" || icon.CanRename || icon.CanDelete {
		t.Fatalf("rtu-icon is listed to %s as %+v, want no owner, and neither rename nor delete", account, icon)
	}

	for _, request := range []struct{ method, body string }{
		{http.MethodPut, `{"name":"rtu-renamed"}`},
		{http.MethodDelete, ""},
	} {
		recorder := harness.iconRequest(request.method, "rtu-icon", request.body, account, builderIconUser())
		if recorder.Code != http.StatusForbidden {
			t.Errorf("%s rtu-icon as %s: status = %d, want %d: %s",
				request.method, account, recorder.Code, http.StatusForbidden, recorder.Body)
		}
	}

	// The holders of the builder-icons permissions rename and delete it.
	if icon := listed(builderIconAdmin()); !icon.CanRename || !icon.CanDelete {
		t.Fatalf("rtu-icon is listed to an icon administrator as %+v, want rename and delete", icon)
	}

	renamed := harness.iconRequest(http.MethodPut, "rtu-icon", `{"name":"rtu-renamed"}`, account, builderIconAdmin())
	if renamed.Code != http.StatusOK {
		t.Fatalf("renaming as an icon administrator: status = %d: %s", renamed.Code, renamed.Body)
	}

	var answer builderIconResponse

	harness.decode(renamed, &answer)

	if answer.Name != "rtu-renamed" || answer.Owner != "" {
		t.Errorf("the renamed icon = %+v, want rtu-renamed, still with no owner", answer)
	}

	deleted := harness.iconRequest(http.MethodDelete, "rtu-renamed", "", account, builderIconAdmin())
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("deleting as an icon administrator: status = %d: %s", deleted.Code, deleted.Body)
	}
}

// TestBuilderStartsWhenTheTemplateDirectoryIsAFile asserts a template
// directory path that holds a regular file, which cannot be opened as a
// directory, keeps the Builder from nothing: its routes serve, the server
// has no collections of its own, and a warning says why.
func TestBuilderStartsWhenTheTemplateDirectoryIsAFile(t *testing.T) {
	logs := plogtest.Capture(t)
	path := filepath.Join(t.TempDir(), "templates")

	if err := os.WriteFile(path, []byte("name: not a directory\n"), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}

	harness := newBuilderHarnessWith(t, []builderOption{withBuilderTemplateFiles(path)})

	list := harness.templates(builderTestOwner)
	if len(list.Preloaded) != 0 || len(list.Templates) != len(builderBuiltinIDs) {
		t.Fatalf("listing = %d server collections and %d templates, want none and the built-in templates",
			len(list.Preloaded), len(list.Templates))
	}

	records := logs.Records(t, plogtest.Message("builder template directory cannot be read"))
	if len(records) != 1 || records[0]["level"] != "WARN" || records[0]["directory"] != path {
		t.Fatalf("the log of a directory that is a file = %v, want one warning naming it", records)
	}
}

// TestBuilderTemplatesRefuseChangingPreloadedItems asserts no write route
// changes or deletes a template or a collection the server read from a
// template file, and that the refusal says why.
func TestBuilderTemplatesRefuseChangingPreloadedItems(t *testing.T) {
	harness := newBuilderTemplateFilesHarness(t)
	harness.setUser(builderTestOwner, "2026-01-01T00:00:00Z")
	harness.setUser(builderTestPeer, "2026-01-02T00:00:00Z")

	preloaded := harness.templates(builderTestOwner).Preloaded
	collection := preloaded[0].Collection.ID
	template := preloaded[0].Templates[0].ID
	content := builderJSON(t, builderTemplateContentOf("Changed", ""))
	ids := func(templates, collections []string) string {
		return builderJSON(t, builderTemplateSelection{Templates: templates, Collections: collections})
	}

	for _, tt := range []struct {
		name, method, rest, body, ifMatch string
	}{
		{"replace a template", http.MethodPut, "/items/" + template, content, `"1"`},
		{"replace a collection", http.MethodPut, "/collections/" + collection, `{"name":"x","templateIds":[]}`, `"1"`},
		{
			"put a template into a collection", http.MethodPost, "/collections",
			`{"name":"Mine","templateIds":["` + template + `"]}`, "",
		},
		{"delete a template", http.MethodPost, "/delete", ids([]string{template}, nil), ""},
		{"delete a collection", http.MethodPost, "/delete", ids(nil, []string{collection}), ""},
		{
			"share a collection", http.MethodPost, "/share",
			`{"collections":["` + collection + `"],"add":["` + builderTestPeer + `"]}`, "",
		},
		{
			"publish a template", http.MethodPost, "/publish",
			`{"templates":["` + template + `"],"serverWide":true}`, "",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := builderLibraryPath(builderTestOwner, tt.rest)
			recorder := harness.library(tt.method, path, builderTestOwner, tt.body, tt.ifMatch)

			if recorder.Code != http.StatusConflict {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusConflict, recorder.Body)
			}

			var refusal struct {
				Message string `json:"message"`
			}

			if err := json.Unmarshal(recorder.Body.Bytes(), &refusal); err != nil {
				t.Fatalf("decoding the refusal: %v", err)
			}

			if !strings.Contains(refusal.Message, "is read from a template file of the phenix server") ||
				!strings.Contains(refusal.Message, "copy it to your library") {
				t.Fatalf("message = %q, want it to say the item is the server's and how to change a copy", refusal.Message)
			}
		})
	}

	// Nothing changed: the caller's library holds what it held, and the
	// server's collections are as they were read.
	after := harness.templates(builderTestOwner)

	if len(after.Collections) != 0 || len(after.Templates) != len(builderBuiltinIDs) {
		t.Fatalf("the library changed: %d templates, %d collections", len(after.Templates), len(after.Collections))
	}

	if len(after.Preloaded) != 2 || after.Preloaded[0].Templates[0].Name != "PLC" {
		t.Fatalf("the server's collections changed: %+v", after.Preloaded)
	}

	// A copy in the caller's library is an ordinary template.
	copied := harness.post("/items", builderJSON(t, builderTemplateCreateRequest{
		Templates:  []builderTemplateContent{builderTemplateContentOf("PLC", "")},
		Collection: nil,
	}))
	if copied.Code != http.StatusCreated {
		t.Fatalf("copying: status = %d: %s", copied.Code, copied.Body)
	}
}

// TestBuilderTemplateFileSchemaRoute asserts the template file schema is
// served under the permission of the document schema, with the Builder's
// response headers.
func TestBuilderTemplateFileSchemaRoute(t *testing.T) {
	harness := newBuilderHarness(t)

	recorder := harness.do(builderRequest{method: http.MethodGet, path: builderTemplateSchemaPath, user: builderTestOwner})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
	}

	if got := recorder.Header().Get("X-Content-Type-Options"); got != builderNoSniff {
		t.Errorf("X-Content-Type-Options = %q, want %q", got, builderNoSniff)
	}

	if got := recorder.Header().Get("Content-Type"); got != mimeJSON {
		t.Errorf("Content-Type = %q, want %q", got, mimeJSON)
	}

	want, err := bdoc.TemplateFileSchemaJSON()
	if err != nil {
		t.Fatalf("TemplateFileSchemaJSON returned error: %v", err)
	}

	if recorder.Body.String() != string(want) {
		t.Fatal("the route does not serve the template file schema")
	}

	// What it serves holds the definitions a template device refers to.
	var served struct {
		Defs map[string]json.RawMessage `json:"$defs"`
	}

	if err := json.Unmarshal(recorder.Body.Bytes(), &served); err != nil {
		t.Fatalf("decoding the schema: %v", err)
	}

	for _, name := range []string{"templateDevice", "iconSize", "iconRef", "iconKey", "hexColor"} {
		if _, held := served.Defs[name]; !held {
			t.Errorf("the served schema does not hold $defs/%s", name)
		}
	}

	// Without schemas get on the resource name builder.
	for _, policy := range []struct {
		resources, names []string
	}{
		{[]string{"configs"}, []string{"*"}},
		{[]string{"schemas"}, []string{"Topology"}},
	} {
		role := builderRole(builderPolicy(policy.resources, policy.names, []string{"list", "get"}))

		forbidden := harness.do(builderRequest{
			method: http.MethodGet, path: builderTemplateSchemaPath, user: builderTestOwner, role: &role,
		})
		if forbidden.Code != http.StatusForbidden {
			t.Errorf("with %v on %v: status = %d, want %d", policy.resources, policy.names, forbidden.Code,
				http.StatusForbidden)
		}
	}
}
