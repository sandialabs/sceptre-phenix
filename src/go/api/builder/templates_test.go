package builder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"phenix/store"
	"phenix/store/recordtest/memrecord"
	"phenix/types/builder"
)

// testTemplate returns a valid template whose device is named after name.
func testTemplate(name string) builder.Template {
	return builder.Template{
		ID:          "",
		Name:        name,
		Description: "made by a test",
		Device: builder.TemplateDevice{
			IconKey: builder.IconServer,
			Spec: map[string]any{
				"type":    "VirtualMachine",
				"general": map[string]any{"hostname": "host-" + strings.Join(strings.Fields(strings.ToLower(name)), "-")},
			},
		},
	}
}

// paddedTemplate returns a valid template whose device takes about size
// bytes as JSON.
func paddedTemplate(name string, size int) builder.Template {
	template := testTemplate(name)
	template.Device.Spec["padding"] = strings.Repeat("p", size)

	return template
}

// iconTemplate returns a valid template that names a custom icon.
func iconTemplate(name, icon string) builder.Template {
	template := testTemplate(name)
	template.Device.Icon = icon

	return template
}

// mustLibrary returns the template library of the server.
func mustLibrary(t *testing.T, h *testHarness) *TemplateLibrary {
	t.Helper()

	library, err := h.service.GetLibrary(context.Background())
	if err != nil {
		t.Fatalf("GetLibrary returned error: %v", err)
	}

	return library
}

// mustUpdate applies a change to the library, as testActor.
func mustUpdate(t *testing.T, h *testHarness, change func(*TemplateLibrary) error) *TemplateLibrary {
	t.Helper()

	library, err := h.service.UpdateLibrary(context.Background(), testActor, change)
	if err != nil {
		t.Fatalf("UpdateLibrary returned error: %v", err)
	}

	return library
}

// mustAddTemplates adds templates to the library and returns their ids.
func mustAddTemplates(t *testing.T, h *testHarness, templates ...builder.Template) []string {
	t.Helper()

	var ids []string

	mustUpdate(t, h, func(library *TemplateLibrary) error {
		var err error

		ids, err = library.AddTemplates(templates, h.service.NewID)

		return err
	})

	return ids
}

// templateIDs returns the ids of the templates of a library, in order.
func templateIDs(library *TemplateLibrary) []string {
	ids := make([]string, 0, len(library.Templates))

	for i := range library.Templates {
		ids = append(ids, library.Templates[i].ID)
	}

	return ids
}

// libraryRecord returns the stored record of the library.
func libraryRecord(t *testing.T, h *testHarness) store.Record {
	t.Helper()

	record, err := h.store.GetRecord(NamespaceTemplates, LibraryKey)
	if err != nil {
		t.Fatalf("reading the library record: %v", err)
	}

	return record
}

// refusal returns the reason of a refused library change, and whether it
// is a limit.
func refusal(t *testing.T, err error) (string, bool) {
	t.Helper()

	var refused *LibraryError

	if !errors.As(err, &refused) {
		t.Fatalf("error = %v, want a LibraryError", err)
	}

	if want := map[bool]error{true: ErrTooLarge, false: ErrInvalid}[refused.Limit]; !errors.Is(err, want) {
		t.Fatalf("error = %v, want it to match %v", err, want)
	}

	return refused.Reason, refused.Limit
}

// TestLibraryStartsEmpty asserts a server with no record has an empty
// library, without the built-in templates, and that reading writes nothing.
func TestLibraryStartsEmpty(t *testing.T) {
	h := newHarness(t)
	library := mustLibrary(t, h)

	if library.Revision != 0 || !library.Updated.IsZero() || library.UpdatedBy != "" {
		t.Fatalf("a new library = %+v, want no revision and no times", library)
	}

	if library.Templates == nil || len(library.Templates) != 0 || library.Collections == nil || len(library.Collections) != 0 {
		t.Fatalf("a new library holds %q and %v, want empty lists", templateIDs(library), library.Collections)
	}

	if got := h.store.Count(NamespaceTemplates); got != 0 {
		t.Fatalf("reading wrote %d library records, want 0", got)
	}

	// A change that changes nothing writes nothing either.
	unchanged := mustUpdate(t, h, func(library *TemplateLibrary) error {
		library.Delete([]string{"server", "no-such-template"}, []string{"no-such-collection"})

		return nil
	})

	if unchanged.Revision != 0 || h.store.Count(NamespaceTemplates) != 0 {
		t.Fatalf("a change of nothing wrote a record: revision %d", unchanged.Revision)
	}
}

// TestLibraryFirstChangeCreatesTheRecord asserts the first change creates the
// one record of the library, which holds only what users added.
func TestLibraryFirstChangeCreatesTheRecord(t *testing.T) {
	h := newHarness(t)

	ids := mustAddTemplates(t, h, testTemplate("PLC"))

	if keys := h.store.Keys(NamespaceTemplates); len(keys) != 1 || keys[0] != LibraryKey {
		t.Fatalf("library records = %q, want only %q", keys, LibraryKey)
	}

	for _, namespace := range []string{NamespaceDrafts, NamespaceChunks, NamespacePublished, NamespaceIcons} {
		if got := h.store.Count(namespace); got != 0 {
			t.Errorf("%s holds %d records, want 0", namespace, got)
		}
	}

	record := libraryRecord(t, h)
	library := mustLibrary(t, h)

	if got := templateIDs(library); !slices.Equal(got, ids) {
		t.Fatalf("the library holds %q, want %q", got, ids)
	}

	if library.Revision != record.Revision || library.Updated.IsZero() || library.UpdatedBy != testActor ||
		library.Updated.Location() != time.UTC {
		t.Fatalf("the stored library = revision %d, updated %v by %q", library.Revision, library.Updated, library.UpdatedBy)
	}

	added := library.Template(ids[0])

	if added.Name != "PLC" || added.Version != 1 || added.Created.IsZero() || !added.Created.Equal(added.Updated) ||
		added.CreatedBy != testActor || added.UpdatedBy != testActor {
		t.Fatalf("the new template = %+v, want version 1 and the time and user that added it", added)
	}

	var stored map[string]json.RawMessage

	if err := json.Unmarshal(record.Value, &stored); err != nil {
		t.Fatalf("the library record is not JSON: %v", err)
	}

	for _, field := range []string{"templates", "collections", "updated", "updatedBy"} {
		if _, ok := stored[field]; !ok {
			t.Errorf("the library record has no %q", field)
		}
	}

	if len(stored) != 4 || string(stored["collections"]) != "[]" {
		t.Errorf("the library record = %s, want four fields and an empty list of collections", record.Value)
	}
}

// TestLibraryRefusesSource asserts a library template never names a source:
// adding one drops it, and a change that sets one is refused.
func TestLibraryRefusesSource(t *testing.T) {
	h := newHarness(t)

	copied := testTemplate("Copy")
	copied.Source = &builder.TemplateSource{Kind: builder.TemplateSourceBuiltin, ID: "router", Version: 1}

	ids := mustAddTemplates(t, h, copied)

	if source := mustLibrary(t, h).Template(ids[0]).Source; source != nil {
		t.Fatalf("an added template keeps the source %+v", source)
	}

	if strings.Contains(string(libraryRecord(t, h).Value), `"source"`) {
		t.Fatalf("the library record names a source: %s", libraryRecord(t, h).Value)
	}

	_, err := h.service.UpdateLibrary(context.Background(), testActor, func(library *TemplateLibrary) error {
		library.Template(ids[0]).Source = copied.Source

		return nil
	})

	if reason, limit := refusal(t, err); reason != "template 0 names a source" || limit {
		t.Fatalf("a template with a source is refused with %q", reason)
	}
}

// TestBuiltinIDsAreNotServerIDs asserts no built-in ID can be the ID of an
// item read from a template file, so a source names one of them only.
func TestBuiltinIDsAreNotServerIDs(t *testing.T) {
	for _, template := range builder.BuiltinTemplates() {
		if strings.HasPrefix(template.ID, serverIDPrefix) {
			t.Errorf("the built-in ID %q starts as the IDs of the server's items do", template.ID)
		}
	}

	if id := serverID("template", "server.yaml", "server"); !strings.HasPrefix(id, serverIDPrefix) {
		t.Fatalf("serverID returned %q, want the prefix %q", id, serverIDPrefix)
	}
}

func TestLibraryAddTemplates(t *testing.T) {
	h := newHarness(t)
	library := mustLibrary(t, h)
	earlier, err := library.AddTemplates([]builder.Template{testTemplate("Earlier")}, h.service.NewID)
	if err != nil {
		t.Fatalf("AddTemplates returned error: %v", err)
	}

	first, second := testTemplate("One"), testTemplate("Two")
	first.ID = "server"

	ids, err := library.AddTemplates([]builder.Template{first, second}, h.service.NewID)
	if err != nil {
		t.Fatalf("AddTemplates returned error: %v", err)
	}

	// Each gets a new id, whatever it came with, and they go last, in order.
	if len(ids) != 2 || ids[0] == ids[1] || ids[0] == "server" ||
		!slices.Equal(templateIDs(library), append(slices.Clone(earlier), ids...)) {
		t.Fatalf("AddTemplates gave %q in %q", ids, templateIDs(library))
	}

	if library.Template(ids[0]).Name != "One" || library.Template(ids[1]).Name != "Two" || first.ID != "server" {
		t.Fatalf("the new templates = %+v", library.Templates[1:])
	}

	if added, err := library.AddTemplates(nil, h.service.NewID); err != nil || len(added) != 0 {
		t.Fatalf("adding no template = %q, %v, want nothing", added, err)
	}

	tests := []struct {
		name      string
		templates []builder.Template
		want      string
		limit     bool
	}{
		{
			name:      "no name",
			templates: []builder.Template{testTemplate("Fine"), testTemplate(" ")},
			want:      "templates[1].name: template name is required",
		},
		{
			name:      "a long name",
			templates: []builder.Template{testTemplate(strings.Repeat("n", builder.MaxTemplateNameBytes+1))},
			want:      "templates[0].name: template name must be at most 128 bytes",
		},
		{
			name: "no spec",
			templates: []builder.Template{{
				ID: "", Name: "Bare", Description: "",
				Device: builder.TemplateDevice{IconKey: "", Icon: "", OutlineColor: "", FillColor: "", Spec: nil},
			}},
			want: "templates[0].device.spec: template device spec is required",
		},
		{
			name:      "a device over 16 KiB",
			templates: []builder.Template{paddedTemplate("Big", builder.MaxTemplateDeviceBytes)},
			want:      "templates[0].device: template device must take at most 16384 bytes as JSON, not",
		},
		{
			name:      "a custom icon that is an icon id",
			templates: []builder.Template{testTemplate("Fine"), iconTemplate("Drawn", "sha256:"+strings.Repeat("a", 64))},
			want:      `templates[1].device.icon: icon name "sha256:` + strings.Repeat("a", 57) + `..." must be 1 to 64`,
		},
		{
			name:      "a custom icon with a long name",
			templates: []builder.Template{iconTemplate("Drawn", strings.Repeat("n", 65))},
			want:      `templates[0].device.icon: icon name "` + strings.Repeat("n", 64) + `..." must be 1 to 64`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			library := mustLibrary(t, h)

			_, err := library.AddTemplates(tt.templates, h.service.NewID)

			if reason, limit := refusal(t, err); !strings.HasPrefix(reason, tt.want) || limit != tt.limit {
				t.Fatalf("refused with %q (limit %v), want %q", reason, limit, tt.want)
			}

			// A refusal leaves the library as it was.
			if got := templateIDs(library); len(got) != 0 {
				t.Fatalf("a refused change left %q", got)
			}
		})
	}

	// An id source that fails, or gives an id the library has, fails the
	// change rather than replacing a template.
	broken := errors.New("no more ids")

	if _, err := library.AddTemplates(
		[]builder.Template{testTemplate("X")}, func() (string, error) { return "", broken },
	); !errors.Is(err, broken) {
		t.Fatalf("a failing id source = %v, want its error", err)
	}

	if _, err := library.AddTemplates(
		[]builder.Template{testTemplate("X")}, func() (string, error) { return earlier[0], nil },
	); err == nil || errors.Is(err, ErrInvalid) {
		t.Fatalf("an id the library has = %v, want an error that is not the caller's", err)
	}
}

func TestLibraryReplaceTemplate(t *testing.T) {
	h := newHarness(t)
	ids := mustAddTemplates(t, h, testTemplate("PLC"), testTemplate("Other"))
	before := mustLibrary(t, h).Template(ids[0])

	h.now.Add(10)

	content := testTemplate("PLC two")
	content.ID = "ignored"
	content.Description = ""

	updated := mustUpdate(t, h, func(library *TemplateLibrary) error {
		return library.ReplaceTemplate(ids[0], content)
	})

	after := updated.Template(ids[0])

	// The content changed: one version more, and the time of the change.
	if after.ID != ids[0] || after.Name != "PLC two" || after.Description != "" || after.Version != before.Version+1 ||
		after.ETag() != `"2"` || !after.Created.Equal(before.Created) || !after.Updated.After(before.Updated) {
		t.Fatalf("the replaced template = %+v, was %+v", after, before)
	}

	if updated.Template("ignored") != nil || updated.Template(ids[1]).Version != 1 {
		t.Fatalf("replacing one template changed another: %q", templateIDs(updated))
	}

	// The same content again changes nothing, and writes nothing.
	revision := updated.Revision

	same := mustUpdate(t, h, func(library *TemplateLibrary) error {
		return library.ReplaceTemplate(ids[0], content)
	})

	if same.Revision != revision || same.Template(ids[0]).Version != after.Version ||
		libraryRecord(t, h).Revision != revision || !same.Updated.Equal(updated.Updated) {
		t.Fatalf("replacing with the same content wrote revision %d, version %d", same.Revision, same.Template(ids[0]).Version)
	}

	library := mustLibrary(t, h)

	// A built-in template is not in the library.
	for _, id := range []string{"no-such", "router"} {
		if err := library.ReplaceTemplate(id, content); !errors.Is(err, ErrNotFound) {
			t.Fatalf("replacing %s, which the library does not hold = %v, want ErrNotFound", id, err)
		}
	}

	if err := library.ReplaceTemplate("no-such", content); !errors.Is(err, ErrNotFound) {
		t.Fatalf("replacing a template the library does not hold = %v, want ErrNotFound", err)
	}

	err := library.ReplaceTemplate(ids[0], testTemplate(""))

	if reason, limit := refusal(t, err); reason != "template.name: template name is required" || limit {
		t.Fatalf("an invalid replacement is refused with %q", reason)
	}

	err = library.ReplaceTemplate(ids[0], iconTemplate("Drawn", "two words"))

	if reason, _ := refusal(t, err); !strings.HasPrefix(reason, `template.device.icon: icon name "two words" must be`) {
		t.Fatalf("an icon that is not an icon name is refused with %q", reason)
	}

	if library.Template(ids[0]).Name != "PLC two" {
		t.Fatalf("a refused replacement changed the template: %+v", library.Template(ids[0]))
	}
}

// TestLibraryCreatorAndUpdater asserts a template and a collection name who
// added them and who last changed their content.
func TestLibraryCreatorAndUpdater(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	ids := mustAddTemplates(t, h, testTemplate("PLC"), testTemplate("Other"))

	var collection string

	added := mustUpdate(t, h, func(library *TemplateLibrary) error {
		var err error

		collection, err = library.AddCollection(
			CollectionContent{Name: "Floor", Description: "", TemplateIDs: ids[:1]}, h.service.NewID,
		)

		return err
	})

	if plc := added.Template(ids[0]); plc.CreatedBy != testActor || plc.UpdatedBy != testActor {
		t.Fatalf("a new template = %+v, want it created and updated by %s", plc, testActor)
	}

	if floor := added.Collection(collection); floor.CreatedBy != testActor || floor.UpdatedBy != testActor {
		t.Fatalf("a new collection = %+v, want it created and updated by %s", floor, testActor)
	}

	// Another user changes the content: the updater is that user.
	changed, err := h.service.UpdateLibrary(ctx, testPeer, func(library *TemplateLibrary) error {
		if err := library.ReplaceTemplate(ids[0], testTemplate("PLC two")); err != nil {
			return err
		}

		return library.ReplaceCollection(collection, CollectionContent{Name: "Floor two", Description: "", TemplateIDs: ids[:1]})
	})
	if err != nil || changed.UpdatedBy != testPeer {
		t.Fatalf("UpdateLibrary as %s = updated by %q, %v", testPeer, changed.UpdatedBy, err)
	}

	for what, item := range map[string][2]string{
		"the template":     {changed.Template(ids[0]).CreatedBy, changed.Template(ids[0]).UpdatedBy},
		"the collection":   {changed.Collection(collection).CreatedBy, changed.Collection(collection).UpdatedBy},
		"an unchanged one": {changed.Template(ids[1]).CreatedBy, changed.Template(ids[1]).UpdatedBy},
	} {
		want := [2]string{testActor, testPeer}
		if what == "an unchanged one" {
			want = [2]string{testActor, testActor}
		}

		if item != want {
			t.Errorf("%s was created and updated by %q, want %q", what, item, want)
		}
	}
}

func TestLibraryCollections(t *testing.T) {
	h := newHarness(t)
	ids := mustAddTemplates(t, h, testTemplate("One"), testTemplate("Two"))

	var collection string

	created := mustUpdate(t, h, func(library *TemplateLibrary) error {
		var err error

		collection, err = library.AddCollection(CollectionContent{
			Name: "Plant floor", Description: "PLCs", TemplateIDs: []string{ids[1], ids[0]},
		}, h.service.NewID)

		return err
	})

	first := created.Collection(collection)

	if first == nil || first.Name != "Plant floor" || first.Description != "PLCs" ||
		!slices.Equal(first.TemplateIDs, []string{ids[1], ids[0]}) || first.Version != 1 || first.ETag() != `"1"` ||
		first.Created.IsZero() || !first.Created.Equal(first.Updated) {
		t.Fatalf("the new collection = %+v", first)
	}

	// A collection is not a change of its templates.
	if template := created.Template(ids[1]); template.Version != 1 {
		t.Fatalf("adding a collection changed its template: %+v", template)
	}

	h.now.Add(10)

	replaced := mustUpdate(t, h, func(library *TemplateLibrary) error {
		return library.ReplaceCollection(collection, CollectionContent{Name: "Floor", Description: "", TemplateIDs: []string{ids[0]}})
	})

	second := replaced.Collection(collection)

	if second.Name != "Floor" || second.Description != "" || !slices.Equal(second.TemplateIDs, []string{ids[0]}) ||
		second.Version != 2 || !second.Created.Equal(first.Created) || !second.Updated.After(first.Updated) {
		t.Fatalf("the replaced collection = %+v, was %+v", second, first)
	}

	// An empty collection is one, and encodes its list as a list.
	emptied := mustUpdate(t, h, func(library *TemplateLibrary) error {
		return library.ReplaceCollection(collection, CollectionContent{Name: "Floor", Description: "", TemplateIDs: nil})
	})

	if got := emptied.Collection(collection); got.Version != 3 || got.TemplateIDs == nil || len(got.TemplateIDs) != 0 ||
		!strings.Contains(string(libraryRecord(t, h).Value), `"templateIds":[]`) {
		t.Fatalf("the emptied collection = %+v", got)
	}

	same := mustUpdate(t, h, func(library *TemplateLibrary) error {
		return library.ReplaceCollection(collection, CollectionContent{Name: "Floor", Description: "", TemplateIDs: []string{}})
	})

	if same.Revision != emptied.Revision || same.Collection(collection).Version != 3 {
		t.Fatalf("replacing a collection with the same content wrote revision %d", same.Revision)
	}

	library := mustLibrary(t, h)

	err := library.ReplaceCollection("no-such", CollectionContent{Name: "X", Description: "", TemplateIDs: nil})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("replacing a collection the library does not hold = %v, want ErrNotFound", err)
	}

	if _, err := library.AddCollection(
		CollectionContent{Name: "X", Description: "", TemplateIDs: nil}, func() (string, error) { return collection, nil },
	); err == nil || errors.Is(err, ErrInvalid) {
		t.Fatalf("an id the library has = %v, want an error that is not the caller's", err)
	}
}

// TestLibraryCollectionRefusals asserts what a collection may not be made
// of or replaced with, and the words of each refusal.
func TestLibraryCollectionRefusals(t *testing.T) {
	h := newHarness(t)
	ids := mustAddTemplates(t, h, testTemplate("One"))

	var collection string

	mustUpdate(t, h, func(library *TemplateLibrary) error {
		var err error

		collection, err = library.AddCollection(CollectionContent{Name: "Floor", Description: "", TemplateIDs: nil}, h.service.NewID)

		return err
	})

	tests := []struct {
		name    string
		content CollectionContent
		want    string
	}{
		{name: "no name", content: CollectionContent{Name: " \t", Description: "", TemplateIDs: nil}, want: "collection name is required"},
		{
			name:    "a long name",
			content: CollectionContent{Name: strings.Repeat("n", 129), Description: "", TemplateIDs: nil},
			want:    "collection name must be at most 128 bytes",
		},
		{
			name:    "a control character in the name",
			content: CollectionContent{Name: "a\nb", Description: "", TemplateIDs: nil},
			want:    "collection name must not contain control characters",
		},
		{
			name:    "a long description",
			content: CollectionContent{Name: "X", Description: strings.Repeat("d", 1025), TemplateIDs: nil},
			want:    "collection description must be at most 1024 bytes",
		},
		{
			name:    "a control character in the description",
			content: CollectionContent{Name: "X", Description: "a\x7fb", TemplateIDs: nil},
			want:    "collection description must not contain control characters",
		},
		{
			name:    "a template of no library",
			content: CollectionContent{Name: "X", Description: "", TemplateIDs: []string{ids[0], "nowhere"}},
			want:    `collection names template "nowhere", which this library does not hold`,
		},
		{
			// A built-in template is read only, and never in the library.
			name:    "a built-in template",
			content: CollectionContent{Name: "X", Description: "", TemplateIDs: []string{"server"}},
			want:    `collection names template "server", which this library does not hold`,
		},
		{
			name:    "a template twice",
			content: CollectionContent{Name: "X", Description: "", TemplateIDs: []string{ids[0], ids[0]}},
			want:    `collection names template "` + ids[0] + `" more than once`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			library := mustLibrary(t, h)

			_, err := library.AddCollection(tt.content, h.service.NewID)

			if reason, limit := refusal(t, err); reason != tt.want || limit {
				t.Fatalf("AddCollection refused with %q (limit %v), want %q", reason, limit, tt.want)
			}

			err = library.ReplaceCollection(collection, tt.content)

			if reason, _ := refusal(t, err); reason != tt.want {
				t.Fatalf("ReplaceCollection refused with %q, want %q", reason, tt.want)
			}

			if len(library.Collections) != 1 || library.Collections[0].Name != "Floor" {
				t.Fatalf("a refused change left %+v", library.Collections)
			}
		})
	}
}

// TestLibraryDeletePrunesCollections asserts a deleted template leaves every
// collection that named it, and a deleted collection leaves its templates.
func TestLibraryDeletePrunesCollections(t *testing.T) {
	h := newHarness(t)
	ids := mustAddTemplates(t, h, testTemplate("One"), testTemplate("Two"), testTemplate("Three"), testTemplate("Four"))

	var both, single string

	mustUpdate(t, h, func(library *TemplateLibrary) error {
		var err error

		both, err = library.AddCollection(
			CollectionContent{Name: "Both", Description: "", TemplateIDs: []string{ids[0], ids[1], ids[3]}}, h.service.NewID,
		)
		if err != nil {
			return err
		}

		single, err = library.AddCollection(
			CollectionContent{Name: "Single", Description: "", TemplateIDs: []string{ids[2]}}, h.service.NewID,
		)

		return err
	})

	h.now.Add(10)

	var templates, collections int

	deleted := mustUpdate(t, h, func(library *TemplateLibrary) error {
		templates, collections = library.Delete([]string{ids[1], ids[3], "no-such"}, []string{single, "no-such"})

		return nil
	})

	if templates != 2 || collections != 1 {
		t.Fatalf("Delete removed %d templates and %d collections, want 2 and 1", templates, collections)
	}

	kept := deleted.Collection(both)

	if deleted.Collection(single) != nil || kept == nil || !slices.Equal(kept.TemplateIDs, []string{ids[0]}) {
		t.Fatalf("after the delete the collections = %+v", deleted.Collections)
	}

	// Losing a template is a change of the collection's content.
	if kept.Version != 2 || !kept.Updated.After(kept.Created) {
		t.Fatalf("the pruned collection = %+v, want version 2 and the time of the delete", kept)
	}

	// The templates of the deleted collection stay in the library.
	if deleted.Template(ids[2]) == nil || deleted.Template(ids[1]) != nil || deleted.Template(ids[3]) != nil {
		t.Fatalf("after the delete the library holds %q", templateIDs(deleted))
	}

	// The same delete again removes nothing and writes nothing.
	again := mustUpdate(t, h, func(library *TemplateLibrary) error {
		templates, collections = library.Delete([]string{ids[1], ids[3]}, []string{single})

		return nil
	})

	if templates != 0 || collections != 0 || again.Revision != deleted.Revision {
		t.Fatalf("the same delete again removed %d and %d at revision %d", templates, collections, again.Revision)
	}
}

// TestLibraryTemplateIcons asserts a template keeps its icon name, whether or
// not the icon exists, and the library record carries no image.
func TestLibraryTemplateIcons(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	mustAddIcon(t, h, testPeer, "plc", iconPixel(t, 1))

	ids := mustAddTemplates(t, h,
		iconTemplate("One", "plc"), iconTemplate("Two", "Not-uploaded"), testTemplate("Plain"))

	library := mustLibrary(t, h)

	if one, two := library.Template(ids[0]), library.Template(ids[1]); one.Device.Icon != "plc" ||
		two.Device.Icon != "Not-uploaded" {
		t.Fatalf("the templates name the icons %q and %q", one.Device.Icon, two.Device.Icon)
	}

	value := string(libraryRecord(t, h).Value)

	if strings.Contains(value, `"icons"`) || strings.Contains(value, "iVBOR") {
		t.Fatalf("the library record carries an image: %s", value)
	}

	// Renaming or deleting the icon leaves the templates as they are: the
	// old name keeps resolving, and then nothing does.
	if _, err := h.service.RenameIcon(ctx, testPeer, "plc", "plc-v2"); err != nil {
		t.Fatalf("RenameIcon returned error: %v", err)
	}

	if err := h.service.DeleteIcon(ctx, testPeer, "plc"); err != nil {
		t.Fatalf("DeleteIcon returned error: %v", err)
	}

	if after := mustLibrary(t, h); after.Template(ids[0]).Device.Icon != "plc" ||
		after.Revision != library.Revision {
		t.Fatalf("changing the icon library changed the template library: %+v", after.Template(ids[0]))
	}
}

// TestLibraryLimits asserts each bound of a library: its templates, its
// collections, the templates of one collection and its record.
func TestLibraryLimits(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := context.Background()

	fill := make([]builder.Template, 0, MaxLibraryTemplates)
	for i := range MaxLibraryTemplates {
		fill = append(fill, testTemplate(fmt.Sprintf("T%d", i)))
	}

	ids := mustAddTemplates(t, h, fill...)

	if library := mustLibrary(t, h); len(library.Templates) != MaxLibraryTemplates {
		t.Fatalf("the library holds %d templates, want %d", len(library.Templates), MaxLibraryTemplates)
	}

	_, err := h.service.UpdateLibrary(ctx, testActor, func(library *TemplateLibrary) error {
		_, err := library.AddTemplates([]builder.Template{testTemplate("One more")}, h.service.NewID)

		return err
	})

	if reason, limit := refusal(t, err); reason != "a library holds at most 200 templates" || !limit {
		t.Fatalf("the 201st template is refused with %q (limit %v)", reason, limit)
	}

	// A collection of every template is the largest one.
	all := ids

	mustUpdate(t, h, func(library *TemplateLibrary) error {
		for i := range MaxLibraryCollections {
			_, err := library.AddCollection(
				CollectionContent{Name: fmt.Sprintf("C%d", i), Description: "", TemplateIDs: all[:1+i%3]}, h.service.NewID,
			)
			if err != nil {
				return err
			}
		}

		return nil
	})

	_, err = h.service.UpdateLibrary(ctx, testActor, func(library *TemplateLibrary) error {
		_, err := library.AddCollection(CollectionContent{Name: "One more", Description: "", TemplateIDs: nil}, h.service.NewID)

		return err
	})

	if reason, limit := refusal(t, err); reason != "a library holds at most 50 collections" || !limit {
		t.Fatalf("the 51st collection is refused with %q (limit %v)", reason, limit)
	}

	library := mustLibrary(t, h)
	first := library.Collections[0].ID

	if err := library.ReplaceCollection(first, CollectionContent{Name: "All", Description: "", TemplateIDs: all}); err != nil {
		t.Fatalf("a collection of %d templates is refused: %v", len(all), err)
	}

	err = library.ReplaceCollection(first, CollectionContent{Name: "All", Description: "", TemplateIDs: append(slices.Clone(all), "x")})

	if reason, limit := refusal(t, err); reason != "a collection holds at most 200 templates" || !limit {
		t.Fatalf("a collection of 201 templates is refused with %q (limit %v)", reason, limit)
	}

	if len(library.Templates) != MaxLibraryTemplates || len(library.Collections) != MaxLibraryCollections {
		t.Fatalf("the limits were not reached: %d templates, %d collections", len(library.Templates), len(library.Collections))
	}
}

func TestLibraryRecordLimit(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// Each takes close to 16 KiB, so 31 of them fit in 512 KiB and 32 do not.
	const each = builder.MaxTemplateDeviceBytes - 256

	fits := make([]builder.Template, 0, 32)
	for i := range 31 {
		fits = append(fits, paddedTemplate(fmt.Sprintf("T%d", i), each))
	}

	mustAddTemplates(t, h, fits...)

	record := libraryRecord(t, h)

	if len(record.Value) > MaxMetadataBytes || len(record.Value) < MaxMetadataBytes-each {
		t.Fatalf("the library record is %d bytes, want it just below %d", len(record.Value), MaxMetadataBytes)
	}

	_, err := h.service.UpdateLibrary(ctx, testActor, func(library *TemplateLibrary) error {
		_, err := library.AddTemplates([]builder.Template{paddedTemplate("One more", each)}, h.service.NewID)

		return err
	})

	reason, limit := refusal(t, err)
	if !strings.HasPrefix(reason, "a library takes at most 524288 bytes, and this one would take ") || !limit {
		t.Fatalf("a library over 512 KiB is refused with %q (limit %v)", reason, limit)
	}

	if strings.Contains(err.Error(), "pppp") {
		t.Fatalf("the refusal repeats the library: %.200s", err)
	}

	if libraryRecord(t, h).Revision != record.Revision {
		t.Fatal("a refused change wrote the library")
	}
}

// TestUpdateLibraryRetriesOnConflict asserts a change that lost to another
// write is applied again to what that write stored.
func TestUpdateLibraryRetriesOnConflict(t *testing.T) {
	for _, first := range []bool{true, false} {
		t.Run(fmt.Sprintf("first write %v", first), func(t *testing.T) {
			h := newHarness(t)

			if !first {
				mustAddTemplates(t, h, testTemplate("Earlier"))
			}

			// Another request of the owner lands between this one's read and
			// its write.
			race := func(string, string) error {
				h.store.BeforeCreate, h.store.BeforeUpdate = nil, nil
				mustAddTemplates(t, h, testTemplate("Racer"))

				return nil
			}

			h.store.BeforeCreate, h.store.BeforeUpdate = race, race

			runs := 0

			library := mustUpdate(t, h, func(library *TemplateLibrary) error {
				runs++

				_, err := library.AddTemplates([]builder.Template{testTemplate("Mine")}, h.service.NewID)

				return err
			})

			names := make([]string, 0, len(library.Templates))
			for i := range library.Templates {
				names = append(names, library.Templates[i].Name)
			}

			if runs != 2 || !slices.Contains(names, "Racer") || names[len(names)-1] != "Mine" ||
				strings.Count(strings.Join(names, ","), "Mine") != 1 {
				t.Fatalf("after %d runs the library holds %q, want the racer's template and then mine, once", runs, names)
			}

			if stored := mustLibrary(t, h); stored.Revision != library.Revision ||
				!slices.Equal(templateIDs(stored), templateIDs(library)) {
				t.Fatalf("the stored library = %q at %d, returned %q at %d",
					templateIDs(stored), stored.Revision, templateIDs(library), library.Revision)
			}
		})
	}
}

// TestUpdateLibraryGivesUp asserts a library that changes under every
// attempt is busy after five.
func TestUpdateLibraryGivesUp(t *testing.T) {
	h := newHarness(t)
	earlier := mustAddTemplates(t, h, testTemplate("Earlier"))

	racing := false

	h.store.BeforeUpdate = func(string, string) error {
		if racing {
			return nil
		}

		racing = true
		defer func() { racing = false }()

		mustUpdate(t, h, func(library *TemplateLibrary) error {
			return library.ReplaceTemplate(earlier[0], testTemplate(fmt.Sprintf("Earlier %d", h.now.Load())))
		})

		return nil
	}

	runs := 0

	_, err := h.service.UpdateLibrary(context.Background(), testActor, func(library *TemplateLibrary) error {
		runs++

		_, err := library.AddTemplates([]builder.Template{testTemplate("Mine")}, h.service.NewID)

		return err
	})

	if !errors.Is(err, ErrBusy) || runs != maxLibraryAttempts || maxLibraryAttempts != 5 {
		t.Fatalf("after %d runs: %v, want ErrBusy after 5", runs, err)
	}

	h.store.BeforeUpdate = nil

	for _, template := range mustLibrary(t, h).Templates {
		if template.Name == "Mine" {
			t.Fatal("a change that gave up was written")
		}
	}
}

// TestUpdateLibraryConcurrent asserts changes made at the same moment are
// all kept: none overwrites another.
func TestUpdateLibraryConcurrent(t *testing.T) {
	h := newHarness(t)

	const writers = 4

	var group sync.WaitGroup

	for i := range writers {
		group.Add(1)

		go func() {
			defer group.Done()

			for {
				_, err := h.service.UpdateLibrary(context.Background(), testActor, func(library *TemplateLibrary) error {
					_, err := library.AddTemplates([]builder.Template{testTemplate(fmt.Sprintf("W%d", i))}, h.service.NewID)

					return err
				})
				if err == nil {
					return
				}

				if !errors.Is(err, ErrBusy) {
					t.Errorf("writer %d: %v", i, err)

					return
				}
			}
		}()
	}

	group.Wait()

	library := mustLibrary(t, h)

	if len(library.Templates) != writers {
		t.Fatalf("the library holds %q, want one template of each writer", templateIDs(library))
	}

	seen := map[string]bool{}
	for _, id := range templateIDs(library) {
		if seen[id] {
			t.Fatalf("template id %q is used twice", id)
		}

		seen[id] = true
	}
}

// TestUpdateLibraryStoreFailures asserts that a failed write returns the
// store's error and leaves the library as it was.
func TestUpdateLibraryStoreFailures(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	add := func(name string) (*TemplateLibrary, error) {
		return h.service.UpdateLibrary(ctx, testActor, func(library *TemplateLibrary) error {
			_, err := library.AddTemplates([]builder.Template{testTemplate(name)}, h.service.NewID)

			return err
		})
	}

	full := errors.New("disk full")
	refuse := func(string, string) error { return full }

	h.store.BeforeCreate = refuse

	if _, err := add("One"); !errors.Is(err, full) || h.store.Count(NamespaceTemplates) != 0 {
		t.Fatalf("a refused first write = %v, want the store's error and no record", err)
	}

	h.store.BeforeCreate = nil

	if _, err := add("One"); err != nil {
		t.Fatalf("UpdateLibrary returned error: %v", err)
	}

	if _, err := add("Two"); err != nil {
		t.Fatalf("UpdateLibrary returned error: %v", err)
	}

	h.store.BeforeUpdate = refuse

	if _, err := add("Three"); !errors.Is(err, full) {
		t.Fatalf("a refused update = %v, want the store's error", err)
	}

	h.store.BeforeUpdate = nil

	names := make([]string, 0, 2)
	for _, template := range mustLibrary(t, h).Templates {
		names = append(names, template.Name)
	}

	if !slices.Equal(names, []string{"One", "Two"}) {
		t.Fatalf("the library holds %q, want One and Two", names)
	}
}

// unreadableStore is a record store whose reads of one record fail.
type unreadableStore struct {
	*memrecord.Store

	err error
}

func (s *unreadableStore) GetRecord(string, string) (store.Record, error) {
	return store.Record{}, s.err
}

// TestLibraryReadFailure asserts a library that cannot be read is neither
// taken for a new one nor changed: the store's error is returned.
func TestLibraryReadFailure(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	mustAddTemplates(t, h, testTemplate("PLC"))

	revision := libraryRecord(t, h).Revision
	down := errors.New("the store is down")

	service, err := New(WithStore(&unreadableStore{Store: h.store, err: down}))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	if library, err := service.GetLibrary(ctx); !errors.Is(err, down) || library != nil {
		t.Fatalf("GetLibrary = %v, %v, want the store's error", library, err)
	}

	runs := 0

	_, err = service.UpdateLibrary(ctx, testActor, func(library *TemplateLibrary) error {
		runs++

		library.Delete(templateIDs(library), nil)

		return nil
	})

	if !errors.Is(err, down) || errors.Is(err, ErrBusy) || runs != 0 || libraryRecord(t, h).Revision != revision {
		t.Fatalf("UpdateLibrary = %v after %d runs, want the store's error and the record as it was", err, runs)
	}
}

func TestUpdateLibraryRequests(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	nothing := func(*TemplateLibrary) error { return nil }

	canceled, cancel := context.WithCancel(ctx)
	cancel()

	if _, err := h.service.UpdateLibrary(canceled, testActor, nothing); !errors.Is(err, context.Canceled) {
		t.Errorf("UpdateLibrary with a canceled context = %v", err)
	}

	if _, err := h.service.GetLibrary(canceled); !errors.Is(err, context.Canceled) {
		t.Errorf("GetLibrary with a canceled context = %v", err)
	}

	for _, user := range []string{"", strings.Repeat("u", MaxOwnerLength+1), "a\nb"} {
		if _, err := h.service.UpdateLibrary(ctx, user, nothing); !errors.Is(err, ErrInvalid) {
			t.Errorf("UpdateLibrary by actor %.20q = %v, want ErrInvalid", user, err)
		}
	}

	// What a change returns is returned as it is, and nothing is written.
	refused := errors.New("the caller's own refusal")

	_, err := h.service.UpdateLibrary(ctx, testActor, func(library *TemplateLibrary) error {
		_, err := library.AddTemplates([]builder.Template{testTemplate("PLC")}, h.service.NewID)
		if err != nil {
			return err
		}

		return refused
	})
	if err != refused || h.store.Count(NamespaceTemplates) != 0 { //nolint:errorlint // the very error, not one wrapping it
		t.Fatalf("a refused change = %v, want the change's own error and no record", err)
	}

	mustAddTemplates(t, h, testTemplate("PLC"))

	// A change that leaves the library invalid is refused, not stored.
	_, err = h.service.UpdateLibrary(ctx, testActor, func(library *TemplateLibrary) error {
		library.Templates[0].Name = ""

		return nil
	})

	if reason, limit := refusal(t, err); reason != "template 0 is not a valid template" || limit {
		t.Fatalf("an invalid library is refused with %q", reason)
	}

	// The actor of a change is recorded.
	library, err := h.service.UpdateLibrary(ctx, testPeer, func(library *TemplateLibrary) error {
		library.Delete(templateIDs(library), nil)

		return nil
	})
	if err != nil || library.UpdatedBy != testPeer || len(library.Templates) != 0 {
		t.Fatalf("a change by another actor = %+v, %v", library, err)
	}
}

// libraryValue returns a library record value built from the stored one with
// edit applied to its decoded form.
func libraryValue(t *testing.T, h *testHarness, edit func(library map[string]any)) []byte {
	t.Helper()

	var library map[string]any

	if err := json.Unmarshal(libraryRecord(t, h).Value, &library); err != nil {
		t.Fatalf("decoding the library record: %v", err)
	}

	edit(library)

	value, err := json.Marshal(library)
	if err != nil {
		t.Fatalf("encoding the library record: %v", err)
	}

	return value
}

// damagedLibrarySecret is in what the damaged records of
// [damagedLibraryCases] hold, so an error that repeats a record is seen.
const damagedLibrarySecret = "s3cret-content"

// damagedLibraryIcon is the custom icon the first template of the library of
// TestLibraryRefusesDamagedRecords names.
const damagedLibraryIcon = "plc"

// damagedLibraryCase is one record that is, or is not, a library this
// package stores: raw, or the stored record with edit applied.
type damagedLibraryCase struct {
	name  string
	raw   string
	edit  func(library map[string]any)
	want  string
	valid bool
}

// damagedLibraryCases returns the damaged records that replace a library of two
// templates, the first with a custom icon, in one collection.
func damagedLibraryCases(t *testing.T) []damagedLibraryCase {
	t.Helper()

	const secret = damagedLibrarySecret

	item := func(library map[string]any, list string, index int) map[string]any {
		items, _ := library[list].([]any)
		entry, _ := items[index].(map[string]any)

		return entry
	}

	return []damagedLibraryCase{
		{name: "not JSON", raw: `{"templates":`, want: "is not valid JSON"},
		{
			name: "trailing content",
			raw:  `{"templates":[],"collections":[],"updated":"2026-10-01T12:00:00Z","updatedBy":"alice"} {}`,
			want: "trailing content",
		},
		{name: "an unknown field", edit: func(library map[string]any) { library["extra"] = secret }, want: "is not valid JSON"},
		{
			name: "an actor with a control character",
			edit: func(library map[string]any) { library["updatedBy"] = "a\nb" },
			want: "the library has an unusable actor",
		},
		{
			name: "a template with no name",
			edit: func(library map[string]any) { item(library, "templates", 0)["name"] = "" },
			want: "template 0 is not a valid template",
		},
		{
			name: "a template with an invalid color",
			edit: func(library map[string]any) {
				device, _ := item(library, "templates", 0)["device"].(map[string]any)
				device["fillColor"] = secret
			},
			want: "template 0 is not a valid template",
		},
		{
			name: "a template with an id that is none",
			edit: func(library map[string]any) { item(library, "templates", 0)["id"] = "../" + secret },
			want: "template 0 has an invalid identifier",
		},
		{
			name: "two templates with one id",
			edit: func(library map[string]any) {
				item(library, "templates", 1)["id"] = item(library, "templates", 0)["id"]
			},
			want: "template 1 repeats an identifier",
		},
		{
			name: "a template at version 0",
			edit: func(library map[string]any) { item(library, "templates", 0)["version"] = 0 },
			want: "template 0 has version 0",
		},
		{
			name: "a template whose icon is not an icon name",
			edit: func(library map[string]any) {
				device, _ := item(library, "templates", 0)["device"].(map[string]any)
				device["icon"] = "sha256:" + secret
			},
			want: "template 0 is not a valid template",
		},
		{
			name: "a template with a source",
			edit: func(library map[string]any) {
				item(library, "templates", 0)["source"] = map[string]any{"kind": "builtin", "id": "router", "version": 1}
			},
			want: "template 0 names a source",
		},
		{
			name: "a template whose updater has a control character",
			edit: func(library map[string]any) { item(library, "templates", 0)["updatedBy"] = "bad\nname" },
			want: "template 0 names an unusable user",
		},
		{
			name: "a collection of a template the library does not hold",
			edit: func(library map[string]any) { item(library, "collections", 0)["templateIds"] = []any{"server", secret} },
			want: "collection 0 names a template the library does not hold, or one twice",
		},
		{
			name: "a collection of a template twice",
			edit: func(library map[string]any) {
				id := item(library, "templates", 0)["id"]
				item(library, "collections", 0)["templateIds"] = []any{id, id}
			},
			want: "collection 0 names a template the library does not hold, or one twice",
		},
		{
			name: "a collection with no name",
			edit: func(library map[string]any) { item(library, "collections", 0)["name"] = " " },
			want: "collection 0 has no usable name",
		},
		{
			name: "a collection with a description that is none",
			edit: func(library map[string]any) { item(library, "collections", 0)["description"] = secret + "\x00" },
			want: "collection 0 has an unusable description",
		},
		{
			name: "a collection at version 0",
			edit: func(library map[string]any) { delete(item(library, "collections", 0), "version") },
			want: "collection 0 has version 0",
		},
		{
			name: "two collections with one id",
			edit: func(library map[string]any) {
				library["collections"] = append(
					library["collections"].([]any),
					item(library, "collections", 0),
				) //nolint:forcetypeassert // test data
			},
			want: "collection 1 repeats an identifier",
		},
		{
			name:  "lists that are null",
			edit:  func(library map[string]any) { library["collections"] = nil },
			valid: true,
		},
	}
}

// TestLibraryRefusesDamagedRecords asserts reading and changing a damaged library
// fail, and the error repeats nothing the record holds.
func TestLibraryRefusesDamagedRecords(t *testing.T) {
	for _, tt := range damagedLibraryCases(t) {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			ctx := context.Background()

			mustUpdate(t, h, func(library *TemplateLibrary) error {
				ids, err := library.AddTemplates(
					[]builder.Template{iconTemplate("Drawn", damagedLibraryIcon), testTemplate("Plain")}, h.service.NewID,
				)
				if err != nil {
					return err
				}

				_, err = library.AddCollection(
					CollectionContent{Name: "Floor", Description: "", TemplateIDs: ids[:1]}, h.service.NewID,
				)

				return err
			})

			value := []byte(tt.raw)
			if tt.edit != nil {
				value = libraryValue(t, h, tt.edit)
			}

			h.store.SetValue(NamespaceTemplates, LibraryKey, value)

			revision := libraryRecord(t, h).Revision
			library, err := h.service.GetLibrary(ctx)

			if tt.valid {
				if err != nil || library.Collections == nil {
					t.Fatalf("GetLibrary = %v, want the library", err)
				}

				return
			}

			if !errors.Is(err, ErrCorrupt) || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("GetLibrary = %v, want ErrCorrupt saying %q", err, tt.want)
			}

			if strings.Contains(err.Error(), damagedLibrarySecret) {
				t.Fatalf("the error repeats what the record holds: %v", err)
			}

			// A damaged library is not changed, and not replaced by an empty
			// one.
			runs := 0

			_, err = h.service.UpdateLibrary(ctx, testActor, func(library *TemplateLibrary) error {
				runs++

				library.Delete(templateIDs(library), nil)

				return nil
			})

			if !errors.Is(err, ErrCorrupt) || runs != 0 || libraryRecord(t, h).Revision != revision {
				t.Fatalf("UpdateLibrary = %v after %d runs, want ErrCorrupt and the record as it was", err, runs)
			}
		})
	}
}

// TestLibrarySurvivesCleanup asserts the cleanups a server start runs leave
// the template library alone.
func TestLibrarySurvivesCleanup(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	ids := mustAddTemplates(t, h, testTemplate("PLC"))
	revision := libraryRecord(t, h).Revision

	if _, err := h.service.CleanupOrphanedChunks(ctx); err != nil {
		t.Fatalf("CleanupOrphanedChunks returned error: %v", err)
	}

	if _, err := h.service.CleanupOrphanedDocuments(ctx, nil); err != nil {
		t.Fatalf("CleanupOrphanedDocuments returned error: %v", err)
	}

	if library := mustLibrary(t, h); library.Template(ids[0]) == nil || library.Revision != revision {
		t.Fatalf("after the cleanups the library holds %q at revision %d", templateIDs(library), library.Revision)
	}
}

func TestLibraryErrorWords(t *testing.T) {
	invalid := newLibraryErrorf("template %d is wrong", 3)
	limit := newLibraryLimitErrorf("a library holds at most %d templates", 200)

	if got, want := invalid.Error(), "builder: invalid request: template library: template 3 is wrong"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}

	if got, want := limit.Error(), "builder: payload too large: template library: a library holds at most 200 templates"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}

	if !errors.Is(invalid, ErrInvalid) || errors.Is(invalid, ErrTooLarge) || !errors.Is(limit, ErrTooLarge) ||
		errors.Is(limit, ErrInvalid) {
		t.Errorf("a refusal matches ErrInvalid, and a limit ErrTooLarge, and neither both")
	}
}
