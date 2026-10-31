package web

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	bapi "phenix/api/builder"
	bdoc "phenix/types/builder"
	"phenix/util/plog/plogtest"
)

// builderTemplateFileIcon is the 1 by 1 PNG, in base64, the test template file carries.
const builderTemplateFileIcon = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="

// newBuilderTemplateFilesHarness returns a harness that read two template
// files (the second with a custom icon) and a file that is not one.
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

// TestBuilderTemplatesListPreloadedCollections asserts every caller lists the
// template files' collections as read-only, with IDs of their own, and their icon as the server's.
func TestBuilderTemplatesListPreloadedCollections(t *testing.T) {
	harness := newBuilderTemplateFilesHarness(t)

	viewer := builderRole(builderPolicy([]string{"configs"}, []string{"*", "*/*"}, []string{"list"}))

	for name, request := range map[string]builderRequest{
		"alice":         {method: http.MethodGet, path: builderTemplatesRoute, user: builderAlice},
		"bob as viewer": {method: http.MethodGet, path: builderTemplatesRoute, user: builderBob, role: viewer},
	} {
		t.Run(name, func(t *testing.T) {
			recorder := harness.do(request)
			if recorder.Code != http.StatusOK {
				t.Fatalf("GET %s as %s: status = %d, want %d: %s", request.path, request.user, recorder.Code, http.StatusOK, recorder.Body)
			}

			var list builderTemplateLibraryResponse

			decodeBuilderAnswer(t, recorder, &list)

			if len(list.Preloaded) != 2 {
				t.Fatalf("GET %s as %s: preloaded = %+v, want the two template files", request.path, request.user, list.Preloaded)
			}

			// The IDs are the server's own: the want takes them from the listing.
			nlr, sandia := list.Preloaded[0], list.Preloaded[1]
			nlrIDs := builderPreloadedTemplateIDs(nlr, 2)
			sandiaIDs := builderPreloadedTemplateIDs(sandia, 1)

			want := []builderPreloadedCollection{
				{
					Collection: builderPreloadedCollectionOf(nlr.Collection.ID, "NLR Node Templates", "", nlrIDs),
					Templates: []builderTemplateResponse{
						builderPreloadedTemplateOf(nlrIDs[0], "PLC", nlr.Collection.ID, bdoc.TemplateDevice{
							Spec: map[string]any{"general": map[string]any{"hostname": "plc"}},
						}),
						builderPreloadedTemplateOf(nlrIDs[1], "HMI", nlr.Collection.ID, bdoc.TemplateDevice{
							Spec: map[string]any{"general": map[string]any{"hostname": "hmi"}},
						}),
					},
				},
				{
					Collection: builderPreloadedCollectionOf(sandia.Collection.ID, "Sandia Node Templates", "Sandia's devices", sandiaIDs),
					Templates: []builderTemplateResponse{
						builderPreloadedTemplateOf(sandiaIDs[0], "RTU", sandia.Collection.ID, bdoc.TemplateDevice{
							Icon: "rtu-icon", Spec: map[string]any{"general": map[string]any{"hostname": "rtu"}},
						}),
					},
				},
			}

			if diff := cmp.Diff(want, list.Preloaded); diff != "" {
				t.Errorf("GET %s as %s: preloaded mismatch (-want +got):\n%s", request.path, request.user, diff)
			}

			for _, entry := range list.Preloaded {
				if id := entry.Collection.ID; !strings.HasPrefix(id, "server-") {
					t.Errorf("GET %s as %s: collection ID = %q, want a server- ID", request.path, request.user, id)
				}

				for _, template := range entry.Templates {
					if slices.Contains(builderBuiltinIDs, template.ID) {
						t.Errorf("GET %s as %s: template %s has the ID of a built-in template", request.path, request.user, template.Name)
					}
				}
			}

			// The library and the built-in templates are listed apart from them.
			if len(list.Collections) != 0 || len(list.Templates) != 0 || len(list.Builtins) != len(builderBuiltinIDs) {
				t.Errorf(
					"GET %s as %s: library = %d templates and %d collections, and %d built-in templates, want only the %d built-in ones",
					request.path,
					request.user,
					len(list.Templates),
					len(list.Collections),
					len(list.Builtins),
					len(builderBuiltinIDs),
				)
			}
		})
	}

	icon, err := harness.service.GetIcon(context.Background(), "rtu-icon")
	if err != nil {
		t.Fatalf("GetIcon(rtu-icon) returned error: %v", err)
	}

	if icon.Owner != bapi.ServerIconOwner {
		t.Errorf("GetIcon(rtu-icon).Owner = %q, want %q", icon.Owner, bapi.ServerIconOwner)
	}

	if icon.Data != builderTemplateFileIcon {
		t.Errorf("GetIcon(rtu-icon).Data = %q, want the template file's icon %q", icon.Data, builderTemplateFileIcon)
	}
}

// builderPreloadedTemplateIDs returns the IDs of the first count templates of
// entry, "" for each it does not list.
func builderPreloadedTemplateIDs(entry builderPreloadedCollection, count int) []string {
	ids := make([]string, count)

	for i := range min(count, len(entry.Templates)) {
		ids[i] = entry.Templates[i].ID
	}

	return ids
}

// builderPreloadedCollectionOf returns the listing of a read-only server
// collection.
func builderPreloadedCollectionOf(id, name, description string, templateIDs []string) builderTemplateCollectionResponse {
	return builderTemplateCollectionResponse{
		ID: id, Source: builderTemplatePreloaded, Name: name, Description: description, TemplateIDs: templateIDs,
		Version: 1, ETag: `"1"`,
	}
}

// builderPreloadedTemplateOf returns the listing of a read-only template of
// the server collection collection.
func builderPreloadedTemplateOf(id, name, collection string, device bdoc.TemplateDevice) builderTemplateResponse {
	return builderTemplateResponse{
		ID: id, Source: builderTemplatePreloaded, Name: name, Device: device, Version: 1, ETag: `"1"`,
		Collections: []string{collection},
	}
}

// TestBuilderTemplateFileIconsAreReadOnly asserts no Builder user renames or
// deletes an icon the server added from a template file.
func TestBuilderTemplateFileIconsAreReadOnly(t *testing.T) {
	harness := newBuilderTemplateFilesHarness(t)

	for _, request := range []struct{ method, body string }{
		{http.MethodPut, `{"name":"rtu-renamed"}`},
		{http.MethodDelete, ""},
	} {
		t.Run(request.method, func(t *testing.T) {
			recorder := harness.iconRequestIfMatch(t, request.method, "rtu-icon", request.body, `"1"`, builderUserRole())
			if recorder.Code != http.StatusForbidden {
				t.Errorf("%s rtu-icon: status = %d, want %d: %s", request.method, recorder.Code, http.StatusForbidden, recorder.Body)
			}
		})
	}
}

// TestBuilderStartsWhenTheTemplateDirectoryIsAFile asserts a template
// directory that is a file only logs a warning, and the routes still serve.
func TestBuilderStartsWhenTheTemplateDirectoryIsAFile(t *testing.T) {
	logs := plogtest.Capture(t)
	path := filepath.Join(t.TempDir(), "templates")

	if err := os.WriteFile(path, []byte("name: not a directory\n"), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}

	harness := newBuilderHarnessWith(t, []builderOption{withBuilderTemplateFiles(path)})

	list := harness.templates(builderAlice)
	if len(list.Preloaded) != 0 || len(list.Builtins) != len(builderBuiltinIDs) {
		t.Errorf("templates(alice) = %d server collections and %d built-in templates, want none and %d",
			len(list.Preloaded), len(list.Builtins), len(builderBuiltinIDs))
	}

	records := logs.Records(t, plogtest.Message("builder template directory cannot be read"))
	want := []map[string]any{{"level": "WARN", "directory": path}}
	onlyLevelAndDirectory := cmpopts.IgnoreMapEntries(func(key string, _ any) bool { return key != "level" && key != "directory" })

	if diff := cmp.Diff(want, records, onlyLevelAndDirectory); diff != "" {
		t.Errorf("log of a template directory that is a file mismatch (-want +got):\n%s", diff)
	}
}

// TestBuilderTemplatesRefuseChangingPreloadedItems asserts no write route
// changes a template file's items, and the refusal says why.
func TestBuilderTemplatesRefuseChangingPreloadedItems(t *testing.T) {
	harness := newBuilderTemplateFilesHarness(t)

	preloaded := harness.templates(builderAlice).Preloaded
	collection := preloaded[0].Collection.ID
	template := preloaded[0].Templates[0].ID
	content := builderJSON(t, builderTemplateContentOf("Changed", ""))
	ids := func(templates, collections []string) string {
		refs := func(ids []string) []builderTemplateRef {
			tagged := make([]builderTemplateRef, 0, len(ids))
			for _, id := range ids {
				tagged = append(tagged, builderTemplateRef{ID: id, ETag: `"1"`})
			}

			return tagged
		}

		return builderJSON(t, builderTemplateSelection{Templates: refs(templates), Collections: refs(collections)})
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
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := builderLibraryPath(tt.rest)
			recorder := harness.library(tt.method, path, builderAlice, tt.body, tt.ifMatch)

			if recorder.Code != http.StatusConflict {
				t.Fatalf("%s %s: status = %d, want %d: %s", tt.method, path, recorder.Code, http.StatusConflict, recorder.Body)
			}

			refusal := builderRefusal(t, recorder)

			if refusal.Code != string(bdoc.CodeTemplateItemReadOnly) {
				t.Errorf("%s %s: code = %q, want %q", tt.method, path, refusal.Code, bdoc.CodeTemplateItemReadOnly)
			}

			// The message says the item is the server's and how to change a copy.
			for _, want := range []string{"comes from a template file of the phenix server", "into the library to change"} {
				if !strings.Contains(refusal.Message, want) {
					t.Errorf("%s %s: message = %q, want it to say %q", tt.method, path, refusal.Message, want)
				}
			}
		})
	}

	// Nothing changed, in the library or in the server's collections.
	after := harness.templates(builderAlice)

	if len(after.Collections) != 0 || len(after.Templates) != 0 {
		t.Errorf("templates(alice) after the refusals = %d templates and %d collections, want none",
			len(after.Templates), len(after.Collections))
	}

	if len(after.Preloaded) != 2 || after.Preloaded[0].Templates[0].Name != "PLC" {
		t.Errorf("templates(alice).Preloaded after the refusals = %+v, want the two template files as read", after.Preloaded)
	}

	// A copy in the library is an ordinary template.
	copied := harness.post("/items", builderJSON(t, builderTemplateCreateRequest{
		Templates:  []builderTemplateContent{builderTemplateContentOf("PLC", "")},
		Collection: nil,
	}))
	if copied.Code != http.StatusCreated {
		t.Errorf("POST /items of a copy of PLC: status = %d, want %d: %s", copied.Code, http.StatusCreated, copied.Body)
	}
}
