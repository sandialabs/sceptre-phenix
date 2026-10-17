package web

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

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

	for _, request := range []builderRequest{
		{method: http.MethodGet, path: builderTemplatesRoute, user: builderAlice},
		{method: http.MethodGet, path: builderTemplatesRoute, user: builderBob, role: viewer},
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

			if collection.Source != builderTemplatePreloaded || collection.CreatedBy != "" || collection.Version != 1 ||
				collection.ETag != `"1"` || !strings.HasPrefix(collection.ID, "server-") {
				t.Errorf("collection %+v is not a read-only server collection", collection)
			}

			ids := make([]string, 0, len(entry.Templates))

			for j := range entry.Templates {
				template := &entry.Templates[j]

				ids = append(ids, template.ID)

				if template.Source != builderTemplatePreloaded || template.CreatedBy != "" || template.Version != 1 ||
					!slices.Equal(template.Collections, []string{collection.ID}) || slices.Contains(builderBuiltinIDs, template.ID) {
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

		// The library and the built-in templates are listed apart from them.
		if len(list.Collections) != 0 || len(list.Templates) != 0 || len(list.Builtins) != len(builderBuiltinIDs) {
			t.Errorf("library = %d templates and %d collections, and %d built-in templates, want only the built-in ones",
				len(list.Templates), len(list.Collections), len(list.Builtins))
		}
	}

	icon, err := harness.service.GetIcon(context.Background(), "rtu-icon")
	if err != nil || icon.Owner != bapi.ServerIconOwner || icon.Data != builderTemplateFileIcon {
		t.Fatalf("GetIcon(rtu-icon) = %+v, %v; want the file's icon, owned by no user", icon, err)
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
		recorder := harness.iconRequest(request.method, "rtu-icon", request.body, builderUserRole())
		if recorder.Code != http.StatusForbidden {
			t.Errorf("%s rtu-icon: status = %d, want %d: %s", request.method, recorder.Code, http.StatusForbidden, recorder.Body)
		}
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
		t.Fatalf("listing = %d server collections and %d built-in templates, want none and the built-in templates",
			len(list.Preloaded), len(list.Builtins))
	}

	records := logs.Records(t, plogtest.Message("builder template directory cannot be read"))
	if len(records) != 1 || records[0]["level"] != "WARN" || records[0]["directory"] != path {
		t.Fatalf("the log of a directory that is a file = %v, want one warning naming it", records)
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
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := builderLibraryPath(tt.rest)
			recorder := harness.library(tt.method, path, builderAlice, tt.body, tt.ifMatch)

			if recorder.Code != http.StatusConflict {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusConflict, recorder.Body)
			}

			refusal := builderRefusal(t, recorder)

			if refusal.Code != string(bdoc.CodeTemplateItemReadOnly) {
				t.Errorf("code = %q, want %q", refusal.Code, bdoc.CodeTemplateItemReadOnly)
			}

			if !strings.Contains(refusal.Message, "is read from a template file of the phenix server") ||
				!strings.Contains(refusal.Message, "into the library to change") {
				t.Fatalf("message = %q, want it to say the item is the server's and how to change a copy", refusal.Message)
			}
		})
	}

	// Nothing changed, in the library or in the server's collections.
	after := harness.templates(builderAlice)

	if len(after.Collections) != 0 || len(after.Templates) != 0 {
		t.Fatalf("the library changed: %d templates, %d collections", len(after.Templates), len(after.Collections))
	}

	if len(after.Preloaded) != 2 || after.Preloaded[0].Templates[0].Name != "PLC" {
		t.Fatalf("the server's collections changed: %+v", after.Preloaded)
	}

	// A copy in the library is an ordinary template.
	copied := harness.post("/items", builderJSON(t, builderTemplateCreateRequest{
		Templates:  []builderTemplateContent{builderTemplateContentOf("PLC", "")},
		Collection: nil,
	}))
	if copied.Code != http.StatusCreated {
		t.Fatalf("copying: status = %d: %s", copied.Code, copied.Body)
	}
}
