package builder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

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

	if len(ids) != len(templates) {
		t.Fatalf("AddTemplates of %d templates returned the ids %q", len(templates), ids)
	}

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

// libraryNames returns the names of the templates of a library, in order.
func libraryNames(library *TemplateLibrary) []string {
	names := make([]string, 0, len(library.Templates))

	for i := range library.Templates {
		names = append(names, library.Templates[i].Name)
	}

	return names
}

// libraryIcons returns the custom icon names of the templates of a library,
// in order.
func libraryIcons(library *TemplateLibrary) []string {
	icons := make([]string, 0, len(library.Templates))

	for i := range library.Templates {
		icons = append(icons, library.Templates[i].Device.Icon)
	}

	return icons
}

// collectionNames returns the names of the collections of a library, in order.
func collectionNames(library *TemplateLibrary) []string {
	names := make([]string, 0, len(library.Collections))

	for i := range library.Collections {
		names = append(names, library.Collections[i].Name)
	}

	return names
}

// authors is who added an item of the library and who last changed it.
type authors struct {
	CreatedBy string
	UpdatedBy string
}

// templateAuthors returns the authors of a template, or none for a template
// the library does not hold.
func templateAuthors(library *TemplateLibrary, id string) authors {
	if template := library.Template(id); template != nil {
		return authors{CreatedBy: template.CreatedBy, UpdatedBy: template.UpdatedBy}
	}

	return authors{}
}

// collectionAuthors returns the authors of a collection, or none for a
// collection the library does not hold.
func collectionAuthors(library *TemplateLibrary, id string) authors {
	if collection := library.Collection(id); collection != nil {
		return authors{CreatedBy: collection.CreatedBy, UpdatedBy: collection.UpdatedBy}
	}

	return authors{}
}

// ignoreTimes ignores the times of the templates and collections of a library,
// which a test checks on their own.
func ignoreTimes() cmp.Options {
	return cmp.Options{
		cmpopts.IgnoreFields(LibraryTemplate{}, "Created", "Updated"),
		cmpopts.IgnoreFields(TemplateCollection{}, "Created", "Updated"),
	}
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

// decodedLibraryRecord returns the stored record of the library, decoded.
func decodedLibraryRecord(t *testing.T, h *testHarness) map[string]any {
	t.Helper()

	var stored map[string]any

	if err := json.Unmarshal(libraryRecord(t, h).Value, &stored); err != nil {
		t.Fatalf("the library record is not JSON: %v", err)
	}

	return stored
}

// refusalOf returns the LibraryError err is, or an error that says why err is
// not a refusal that matches ErrInvalid, or ErrTooLarge for a limit.
func refusalOf(err error) (LibraryError, error) {
	var refused *LibraryError

	if !errors.As(err, &refused) {
		return LibraryError{}, fmt.Errorf("error = %w, want a LibraryError", err)
	}

	want := ErrInvalid
	if refused.Limit {
		want = ErrTooLarge
	}

	if !errors.Is(err, want) {
		return *refused, fmt.Errorf("error = %w, want it to match %w", err, want)
	}

	return *refused, nil
}

// refusalDiff returns how err differs from the refusal want, or "" when it is
// that refusal.
func refusalDiff(want LibraryError, err error) string {
	got, problem := refusalOf(err)
	if problem != nil {
		return problem.Error()
	}

	return cmp.Diff(want, got)
}

// TestLibraryStartsEmpty asserts a server with no record has an empty
// library, without the built-in templates, and that reading writes nothing.
func TestLibraryStartsEmpty(t *testing.T) {
	h := newHarness(t)
	library := mustLibrary(t, h)

	want := &TemplateLibrary{Templates: []LibraryTemplate{}, Collections: []TemplateCollection{}}
	if diff := cmp.Diff(want, library); diff != "" {
		t.Errorf("GetLibrary() of a new server mismatch (-want +got):\n%s", diff)
	}

	if got := h.store.Count(NamespaceTemplates); got != 0 {
		t.Errorf("after GetLibrary() the store holds %d library records, want 0", got)
	}

	// A change that changes nothing writes nothing either.
	unchanged := mustUpdate(t, h, func(library *TemplateLibrary) error {
		library.Delete([]string{"server", "no-such-template"}, []string{"no-such-collection"})

		return nil
	})

	if unchanged.Revision != 0 {
		t.Errorf("UpdateLibrary(delete nothing) revision = %d, want 0", unchanged.Revision)
	}

	if got := h.store.Count(NamespaceTemplates); got != 0 {
		t.Errorf("after UpdateLibrary(delete nothing) the store holds %d library records, want 0", got)
	}
}

// TestLibraryFirstChangeCreatesTheRecord asserts the first change creates the
// one record of the library, which holds only what users added.
func TestLibraryFirstChangeCreatesTheRecord(t *testing.T) {
	h := newHarness(t)

	ids := mustAddTemplates(t, h, testTemplate("PLC"))

	if diff := cmp.Diff([]string{LibraryKey}, h.store.Keys(NamespaceTemplates)); diff != "" {
		t.Errorf("library record keys mismatch (-want +got):\n%s", diff)
	}

	for _, namespace := range []string{NamespaceDrafts, NamespaceContent, NamespacePublished, NamespaceIcons} {
		if got := h.store.Count(namespace); got != 0 {
			t.Errorf("Count(%s) = %d, want 0", namespace, got)
		}
	}

	record := libraryRecord(t, h)
	library := mustLibrary(t, h)

	if diff := cmp.Diff(ids, templateIDs(library)); diff != "" {
		t.Errorf("GetLibrary() template ids mismatch (-want +got):\n%s", diff)
	}

	wantLibrary := &TemplateLibrary{Revision: record.Revision, UpdatedBy: testActor}
	ignore := cmpopts.IgnoreFields(TemplateLibrary{}, "Templates", "Collections", "Updated")

	if diff := cmp.Diff(wantLibrary, library, ignore); diff != "" {
		t.Errorf("GetLibrary() mismatch (-want +got):\n%s", diff)
	}

	if library.Updated.IsZero() || library.Updated.Location() != time.UTC {
		t.Errorf("GetLibrary() updated = %v, want the time of the change in UTC", library.Updated)
	}

	added := library.Template(ids[0])
	if added == nil {
		t.Fatalf("GetLibrary() holds no template %q", ids[0])
	}

	want := LibraryTemplate{Template: testTemplate("PLC"), Version: 1, CreatedBy: testActor, UpdatedBy: testActor}
	want.ID = ids[0]

	if diff := cmp.Diff(want, *added, ignoreTimes()); diff != "" {
		t.Errorf("GetLibrary() new template mismatch (-want +got):\n%s", diff)
	}

	if added.Created.IsZero() || !added.Created.Equal(added.Updated) {
		t.Errorf("GetLibrary() new template created %v, updated %v, want both the time it was added",
			added.Created, added.Updated)
	}

	stored := decodedLibraryRecord(t, h)

	wantFields := []string{"collections", "templates", "updated", "updatedBy"}
	if diff := cmp.Diff(wantFields, slices.Sorted(maps.Keys(stored))); diff != "" {
		t.Errorf("library record fields mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff([]any{}, stored["collections"]); diff != "" {
		t.Errorf("library record collections mismatch (-want +got):\n%s", diff)
	}
}

// TestLibraryRefusesSource asserts a library template never names a source:
// adding one drops it, and a change that sets one is refused.
func TestLibraryRefusesSource(t *testing.T) {
	h := newHarness(t)

	copied := testTemplate("Copy")
	copied.Source = &builder.TemplateSource{Kind: builder.TemplateSourceBuiltin, ID: "router", Version: 1}

	ids := mustAddTemplates(t, h, copied)

	added := mustLibrary(t, h).Template(ids[0])
	if added == nil {
		t.Fatalf("GetLibrary() holds no template %q", ids[0])
	}

	if added.Source != nil {
		t.Errorf("AddTemplates(template with a source) kept the source %+v, want none", added.Source)
	}

	if value := libraryRecord(t, h).Value; bytes.Contains(value, []byte(`"source"`)) {
		t.Errorf("library record = %s, want no source", value)
	}

	_, err := h.service.UpdateLibrary(context.Background(), testActor, func(library *TemplateLibrary) error {
		library.Template(ids[0]).Source = copied.Source

		return nil
	})

	if diff := refusalDiff(LibraryError{Reason: "template 0 names a source"}, err); diff != "" {
		t.Errorf("UpdateLibrary(set a source) refusal mismatch (-want +got):\n%s", diff)
	}
}

// TestBuiltinIDsAreNotServerIDs asserts no built-in ID can be the ID of an
// item read from a template file, so a source names one of them only.
func TestBuiltinIDsAreNotServerIDs(t *testing.T) {
	for _, template := range builder.BuiltinTemplates() {
		if strings.HasPrefix(template.ID, serverIDPrefix) {
			t.Errorf("BuiltinTemplates() ID = %q, want no prefix %q", template.ID, serverIDPrefix)
		}
	}

	if id := serverID("template", "server.yaml", "server"); !strings.HasPrefix(id, serverIDPrefix) {
		t.Errorf("serverID(template, server.yaml, server) = %q, want the prefix %q", id, serverIDPrefix)
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

	// The ids are indexed below.
	if len(ids) != 2 {
		t.Fatalf("AddTemplates(One, Two) = %q, want two ids", ids)
	}

	// Each gets a new id, whatever it came with, and they go last, in order.
	if ids[0] == ids[1] || ids[0] == "server" {
		t.Errorf("AddTemplates(One with id server, Two) = %q, want two new ids", ids)
	}

	if diff := cmp.Diff(append(slices.Clone(earlier), ids...), templateIDs(library)); diff != "" {
		t.Errorf("template ids after AddTemplates mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff([]string{"Earlier", "One", "Two"}, libraryNames(library)); diff != "" {
		t.Errorf("template names after AddTemplates mismatch (-want +got):\n%s", diff)
	}

	if first.ID != "server" {
		t.Errorf("after AddTemplates its argument has the id %q, want server", first.ID)
	}

	if added, err := library.AddTemplates(nil, h.service.NewID); err != nil || len(added) != 0 {
		t.Errorf("AddTemplates(nil) = %q, %v, want no ids and no error", added, err)
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
			want:      "templates[1].name: a template needs a name",
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
			want: "templates[0].device.spec: a template device needs a spec",
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

			if got, problem := refusalOf(err); problem != nil {
				t.Errorf("AddTemplates(%s) %v", tt.name, problem)
			} else if !strings.HasPrefix(got.Reason, tt.want) || got.Limit != tt.limit {
				t.Errorf("AddTemplates(%s) refusal = %q (limit %v), want %q (limit %v)",
					tt.name, got.Reason, got.Limit, tt.want, tt.limit)
			}

			// A refusal leaves the library as it was.
			if got := templateIDs(library); len(got) != 0 {
				t.Errorf("after a refused AddTemplates(%s) the library holds %q, want nothing", tt.name, got)
			}
		})
	}

	// An id source that fails, or gives an id the library has, fails the
	// change rather than replacing a template.
	broken := errors.New("no more ids")

	if _, err := library.AddTemplates(
		[]builder.Template{testTemplate("X")}, func() (string, error) { return "", broken },
	); !errors.Is(err, broken) {
		t.Errorf("AddTemplates(failing id source) = %v, want %v", err, broken)
	}

	if _, err := library.AddTemplates(
		[]builder.Template{testTemplate("X")}, func() (string, error) { return earlier[0], nil },
	); err == nil || errors.Is(err, ErrInvalid) {
		t.Errorf("AddTemplates(id source giving %q) = %v, want an error that is not ErrInvalid", earlier[0], err)
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
	if after == nil {
		t.Fatalf("after ReplaceTemplate the library holds no template %q", ids[0])
	}

	// The content changed: one version more, and the time of the change.
	want := LibraryTemplate{
		Template: content, Version: before.Version + 1, Created: before.Created,
		CreatedBy: testActor, UpdatedBy: testActor,
	}
	want.ID = ids[0]

	if diff := cmp.Diff(want, *after, cmpopts.IgnoreFields(LibraryTemplate{}, "Updated")); diff != "" {
		t.Errorf("ReplaceTemplate(%q) mismatch (-want +got):\n%s", ids[0], diff)
	}

	if !after.Updated.After(before.Updated) {
		t.Errorf("ReplaceTemplate(%q) updated = %v, want after %v", ids[0], after.Updated, before.Updated)
	}

	if got := after.ETag(); got != `"2"` {
		t.Errorf("ETag() after ReplaceTemplate = %s, want \"2\"", got)
	}

	if updated.Template("ignored") != nil {
		t.Errorf("ReplaceTemplate(%q, content with id ignored) added a template ignored", ids[0])
	}

	if other := updated.Template(ids[1]); other == nil || other.Version != 1 {
		t.Errorf("after ReplaceTemplate(%q) the other template = %+v, want it at version 1", ids[0], other)
	}

	// The same content again changes nothing, and writes nothing.
	revision := updated.Revision

	same := mustUpdate(t, h, func(library *TemplateLibrary) error {
		return library.ReplaceTemplate(ids[0], content)
	})

	if diff := cmp.Diff(updated, same); diff != "" {
		t.Errorf("ReplaceTemplate(%q, same content) mismatch (-want +got):\n%s", ids[0], diff)
	}

	if got := libraryRecord(t, h).Revision; got != revision {
		t.Errorf("after ReplaceTemplate(%q, same content) the record revision = %d, want %d", ids[0], got, revision)
	}

	library := mustLibrary(t, h)

	// A built-in template is not in the library.
	for _, id := range []string{"no-such", "router"} {
		if err := library.ReplaceTemplate(id, content); !errors.Is(err, ErrNotFound) {
			t.Errorf("ReplaceTemplate(%q) = %v, want ErrNotFound", id, err)
		}
	}

	if err := library.ReplaceTemplate("no-such", content); !errors.Is(err, ErrNotFound) {
		t.Errorf("ReplaceTemplate(no-such) = %v, want ErrNotFound", err)
	}

	err := library.ReplaceTemplate(ids[0], testTemplate(""))

	if diff := refusalDiff(LibraryError{Reason: "template.name: a template needs a name"}, err); diff != "" {
		t.Errorf("ReplaceTemplate(%q, no name) refusal mismatch (-want +got):\n%s", ids[0], diff)
	}

	err = library.ReplaceTemplate(ids[0], iconTemplate("Drawn", "two words"))

	const wantIcon = `template.device.icon: icon name "two words" must be`
	if got, problem := refusalOf(err); problem != nil {
		t.Errorf("ReplaceTemplate(%q, icon two words) %v", ids[0], problem)
	} else if !strings.HasPrefix(got.Reason, wantIcon) {
		t.Errorf("ReplaceTemplate(%q, icon two words) refusal = %q, want it to start %q", ids[0], got.Reason, wantIcon)
	}

	if got := library.Template(ids[0]); got == nil || got.Name != "PLC two" {
		t.Errorf("after refused ReplaceTemplate(%q) the template = %+v, want it named PLC two", ids[0], got)
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

	want := map[string]authors{
		"template":   {CreatedBy: testActor, UpdatedBy: testActor},
		"collection": {CreatedBy: testActor, UpdatedBy: testActor},
	}
	got := map[string]authors{
		"template":   templateAuthors(added, ids[0]),
		"collection": collectionAuthors(added, collection),
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("authors of a new template and collection mismatch (-want +got):\n%s", diff)
	}

	// Another user changes the content: the updater is that user.
	changed, err := h.service.UpdateLibrary(ctx, testPeer, func(library *TemplateLibrary) error {
		if err := library.ReplaceTemplate(ids[0], testTemplate("PLC two")); err != nil {
			return err
		}

		return library.ReplaceCollection(collection, CollectionContent{Name: "Floor two", Description: "", TemplateIDs: ids[:1]})
	})
	if err != nil {
		t.Fatalf("UpdateLibrary as %s returned error: %v", testPeer, err)
	}

	if changed.UpdatedBy != testPeer {
		t.Errorf("UpdateLibrary as %s updated by = %q, want %q", testPeer, changed.UpdatedBy, testPeer)
	}

	want = map[string]authors{
		"the template":     {CreatedBy: testActor, UpdatedBy: testPeer},
		"the collection":   {CreatedBy: testActor, UpdatedBy: testPeer},
		"an unchanged one": {CreatedBy: testActor, UpdatedBy: testActor},
	}
	got = map[string]authors{
		"the template":     templateAuthors(changed, ids[0]),
		"the collection":   collectionAuthors(changed, collection),
		"an unchanged one": templateAuthors(changed, ids[1]),
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("authors after UpdateLibrary as %s mismatch (-want +got):\n%s", testPeer, diff)
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
	if first == nil {
		t.Fatalf("after AddCollection the library holds no collection %q", collection)
	}

	want := TemplateCollection{
		ID: collection, Name: "Plant floor", Description: "PLCs", TemplateIDs: []string{ids[1], ids[0]},
		Version: 1, CreatedBy: testActor, UpdatedBy: testActor,
	}

	if diff := cmp.Diff(want, *first, ignoreTimes()); diff != "" {
		t.Errorf("AddCollection(Plant floor) mismatch (-want +got):\n%s", diff)
	}

	if first.Created.IsZero() || !first.Created.Equal(first.Updated) {
		t.Errorf("AddCollection(Plant floor) created %v, updated %v, want both the time it was added",
			first.Created, first.Updated)
	}

	if got := first.ETag(); got != `"1"` {
		t.Errorf("ETag() of a new collection = %s, want \"1\"", got)
	}

	// A collection is not a change of its templates.
	if template := created.Template(ids[1]); template == nil || template.Version != 1 {
		t.Errorf("after AddCollection its template = %+v, want it at version 1", template)
	}

	h.now.Add(10)

	replaced := mustUpdate(t, h, func(library *TemplateLibrary) error {
		return library.ReplaceCollection(collection, CollectionContent{Name: "Floor", Description: "", TemplateIDs: []string{ids[0]}})
	})

	second := replaced.Collection(collection)
	if second == nil {
		t.Fatalf("after ReplaceCollection the library holds no collection %q", collection)
	}

	want = TemplateCollection{
		ID: collection, Name: "Floor", TemplateIDs: []string{ids[0]},
		Version: 2, Created: first.Created, CreatedBy: testActor, UpdatedBy: testActor,
	}

	if diff := cmp.Diff(want, *second, cmpopts.IgnoreFields(TemplateCollection{}, "Updated")); diff != "" {
		t.Errorf("ReplaceCollection(Floor) mismatch (-want +got):\n%s", diff)
	}

	if !second.Updated.After(first.Updated) {
		t.Errorf("ReplaceCollection(Floor) updated = %v, want after %v", second.Updated, first.Updated)
	}

	// An empty collection is one, and encodes its list as a list.
	emptied := mustUpdate(t, h, func(library *TemplateLibrary) error {
		return library.ReplaceCollection(collection, CollectionContent{Name: "Floor", Description: "", TemplateIDs: nil})
	})

	want = TemplateCollection{
		ID: collection, Name: "Floor", TemplateIDs: []string{}, Version: 3, CreatedBy: testActor, UpdatedBy: testActor,
	}

	if got := emptied.Collection(collection); got == nil {
		t.Errorf("after ReplaceCollection(no templates) the library holds no collection %q", collection)
	} else if diff := cmp.Diff(want, *got, ignoreTimes()); diff != "" {
		t.Errorf("ReplaceCollection(no templates) mismatch (-want +got):\n%s", diff)
	}

	stored, _ := decodedLibraryRecord(t, h)["collections"].([]any)
	if len(stored) != 1 {
		t.Fatalf("library record collections = %v, want one", stored)
	}

	storedCollection, _ := stored[0].(map[string]any)
	if diff := cmp.Diff([]any{}, storedCollection["templateIds"]); diff != "" {
		t.Errorf("library record templateIds of an empty collection mismatch (-want +got):\n%s", diff)
	}

	same := mustUpdate(t, h, func(library *TemplateLibrary) error {
		return library.ReplaceCollection(collection, CollectionContent{Name: "Floor", Description: "", TemplateIDs: []string{}})
	})

	if diff := cmp.Diff(emptied, same); diff != "" {
		t.Errorf("ReplaceCollection(same content) mismatch (-want +got):\n%s", diff)
	}

	library := mustLibrary(t, h)

	err := library.ReplaceCollection("no-such", CollectionContent{Name: "X", Description: "", TemplateIDs: nil})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("ReplaceCollection(no-such) = %v, want ErrNotFound", err)
	}

	if _, err := library.AddCollection(
		CollectionContent{Name: "X", Description: "", TemplateIDs: nil}, func() (string, error) { return collection, nil },
	); err == nil || errors.Is(err, ErrInvalid) {
		t.Errorf("AddCollection(id source giving %q) = %v, want an error that is not ErrInvalid", collection, err)
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
		{name: "no name", content: CollectionContent{Name: " \t", Description: "", TemplateIDs: nil}, want: "a collection needs a name"},
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

			if diff := refusalDiff(LibraryError{Reason: tt.want}, err); diff != "" {
				t.Errorf("AddCollection(%s) refusal mismatch (-want +got):\n%s", tt.name, diff)
			}

			err = library.ReplaceCollection(collection, tt.content)

			if diff := refusalDiff(LibraryError{Reason: tt.want}, err); diff != "" {
				t.Errorf("ReplaceCollection(%s) refusal mismatch (-want +got):\n%s", tt.name, diff)
			}

			if diff := cmp.Diff([]string{"Floor"}, collectionNames(library)); diff != "" {
				t.Errorf("collection names after refused changes mismatch (-want +got):\n%s", diff)
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
		t.Errorf("Delete(two templates and one collection) = %d, %d, want 2, 1", templates, collections)
	}

	if deleted.Collection(single) != nil {
		t.Errorf("after Delete the library still holds the collection %q", single)
	}

	kept := deleted.Collection(both)
	if kept == nil {
		t.Fatalf("after Delete the library holds no collection %q", both)
	}

	// Losing a template is a change of the collection's content.
	want := TemplateCollection{
		ID: both, Name: "Both", TemplateIDs: []string{ids[0]}, Version: 2, CreatedBy: testActor, UpdatedBy: testActor,
	}

	if diff := cmp.Diff(want, *kept, ignoreTimes()); diff != "" {
		t.Errorf("pruned collection after Delete mismatch (-want +got):\n%s", diff)
	}

	if !kept.Updated.After(kept.Created) {
		t.Errorf("pruned collection updated = %v, want after its creation %v", kept.Updated, kept.Created)
	}

	// The templates of the deleted collection stay in the library.
	if diff := cmp.Diff([]string{ids[0], ids[2]}, templateIDs(deleted)); diff != "" {
		t.Errorf("template ids after Delete mismatch (-want +got):\n%s", diff)
	}

	// The same delete again removes nothing and writes nothing.
	again := mustUpdate(t, h, func(library *TemplateLibrary) error {
		templates, collections = library.Delete([]string{ids[1], ids[3]}, []string{single})

		return nil
	})

	if templates != 0 || collections != 0 {
		t.Errorf("Delete(the same items again) = %d, %d, want 0, 0", templates, collections)
	}

	if again.Revision != deleted.Revision {
		t.Errorf("Delete(the same items again) revision = %d, want %d", again.Revision, deleted.Revision)
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

	if diff := cmp.Diff([]string{"plc", "Not-uploaded", ""}, libraryIcons(library)); diff != "" {
		t.Errorf("template icons mismatch (-want +got):\n%s", diff)
	}

	if value := libraryRecord(t, h).Value; bytes.Contains(value, []byte(`"icons"`)) || bytes.Contains(value, []byte("iVBOR")) {
		t.Errorf("library record = %s, want no image", value)
	}

	// Renaming or deleting the icon leaves the templates as they are: the
	// old name keeps resolving, and then nothing does.
	if _, err := h.service.RenameIcon(ctx, testPeer, "plc", "plc-v2"); err != nil {
		t.Fatalf("RenameIcon returned error: %v", err)
	}

	if err := h.service.DeleteIcon(ctx, testPeer, "plc", iconTag(t, h, "plc")); err != nil {
		t.Fatalf("DeleteIcon returned error: %v", err)
	}

	after := mustLibrary(t, h)

	if diff := cmp.Diff([]string{"plc", "Not-uploaded", ""}, libraryIcons(after)); diff != "" {
		t.Errorf("template icons after RenameIcon and DeleteIcon mismatch (-want +got):\n%s", diff)
	}

	if after.Revision != library.Revision {
		t.Errorf("library revision after RenameIcon and DeleteIcon = %d, want %d", after.Revision, library.Revision)
	}

	if after.Template(ids[0]) == nil {
		t.Errorf("after RenameIcon and DeleteIcon the library holds no template %q", ids[0])
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

	// The next template is refused only in a full library.
	if library := mustLibrary(t, h); len(library.Templates) != MaxLibraryTemplates {
		t.Fatalf("the library holds %d templates, want %d", len(library.Templates), MaxLibraryTemplates)
	}

	_, err := h.service.UpdateLibrary(ctx, testActor, func(library *TemplateLibrary) error {
		_, err := library.AddTemplates([]builder.Template{testTemplate("One more")}, h.service.NewID)

		return err
	})

	if diff := refusalDiff(LibraryError{Reason: "a library holds at most 200 templates", Limit: true}, err); diff != "" {
		t.Errorf("UpdateLibrary(add the 201st template) refusal mismatch (-want +got):\n%s", diff)
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

	if diff := refusalDiff(LibraryError{Reason: "a library holds at most 50 collections", Limit: true}, err); diff != "" {
		t.Errorf("UpdateLibrary(add the 51st collection) refusal mismatch (-want +got):\n%s", diff)
	}

	library := mustLibrary(t, h)
	first := library.Collections[0].ID

	if err := library.ReplaceCollection(first, CollectionContent{Name: "All", Description: "", TemplateIDs: all}); err != nil {
		t.Errorf("ReplaceCollection(%d templates) = %v, want no error", len(all), err)
	}

	err = library.ReplaceCollection(first, CollectionContent{Name: "All", Description: "", TemplateIDs: append(slices.Clone(all), "x")})

	if diff := refusalDiff(LibraryError{Reason: "a collection holds at most 200 templates", Limit: true}, err); diff != "" {
		t.Errorf("ReplaceCollection(201 templates) refusal mismatch (-want +got):\n%s", diff)
	}

	if len(library.Templates) != MaxLibraryTemplates || len(library.Collections) != MaxLibraryCollections {
		t.Errorf("the library holds %d templates and %d collections, want %d and %d",
			len(library.Templates), len(library.Collections), MaxLibraryTemplates, MaxLibraryCollections)
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

	// The next template is refused only in a library just below the limit.
	if len(record.Value) > MaxMetadataBytes || len(record.Value) < MaxMetadataBytes-each {
		t.Fatalf("the library record is %d bytes, want it just below %d", len(record.Value), MaxMetadataBytes)
	}

	_, err := h.service.UpdateLibrary(ctx, testActor, func(library *TemplateLibrary) error {
		_, err := library.AddTemplates([]builder.Template{paddedTemplate("One more", each)}, h.service.NewID)

		return err
	})

	// The words of the error are checked below.
	got, problem := refusalOf(err)
	if problem != nil {
		t.Fatalf("UpdateLibrary(a library over 512 KiB) %v", problem)
	}

	const wantReason = "a library takes at most 524288 bytes, and this one would take "
	if !strings.HasPrefix(got.Reason, wantReason) || !got.Limit {
		t.Errorf("UpdateLibrary(a library over 512 KiB) refusal = %q (limit %v), want %q (limit true)",
			got.Reason, got.Limit, wantReason)
	}

	if strings.Contains(err.Error(), "pppp") {
		t.Errorf("UpdateLibrary(a library over 512 KiB) = %.200s, want an error that does not repeat the library", err)
	}

	if got := libraryRecord(t, h).Revision; got != record.Revision {
		t.Errorf("after a refused UpdateLibrary the record revision = %d, want %d", got, record.Revision)
	}
}

// TestUpdateLibraryRetriesOnConflict asserts a change that lost to another
// write is applied again to what that write stored.
func TestUpdateLibraryRetriesOnConflict(t *testing.T) {
	for _, first := range []bool{true, false} {
		t.Run(fmt.Sprintf("first write %v", first), func(t *testing.T) {
			h := newHarness(t)

			want := []string{"Racer", "Mine"}
			if !first {
				mustAddTemplates(t, h, testTemplate("Earlier"))

				want = slices.Insert(want, 0, "Earlier")
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

			if runs != 2 {
				t.Errorf("UpdateLibrary ran its change %d times, want 2", runs)
			}

			// The racer's template, and then mine, once.
			if diff := cmp.Diff(want, libraryNames(library)); diff != "" {
				t.Errorf("UpdateLibrary(add Mine) template names mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(library, mustLibrary(t, h)); diff != "" {
				t.Errorf("GetLibrary() after UpdateLibrary mismatch (-want +got):\n%s", diff)
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

	if !errors.Is(err, ErrBusy) {
		t.Errorf("UpdateLibrary(always raced) = %v, want ErrBusy", err)
	}

	if maxLibraryAttempts != 5 {
		t.Errorf("maxLibraryAttempts = %d, want 5", maxLibraryAttempts)
	}

	if runs != maxLibraryAttempts {
		t.Errorf("UpdateLibrary(always raced) ran its change %d times, want %d", runs, maxLibraryAttempts)
	}

	h.store.BeforeUpdate = nil

	if names := libraryNames(mustLibrary(t, h)); slices.Contains(names, "Mine") {
		t.Errorf("after UpdateLibrary gave up the library holds %q, want no Mine", names)
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
					t.Errorf("UpdateLibrary(writer %d) = %v, want no error or ErrBusy", i, err)

					return
				}
			}
		}()
	}

	group.Wait()

	library := mustLibrary(t, h)

	if len(library.Templates) != writers {
		t.Errorf("the library holds %q, want one template of each of %d writers", templateIDs(library), writers)
	}

	seen := map[string]bool{}
	for _, id := range templateIDs(library) {
		if seen[id] {
			t.Errorf("the library holds the template id %q twice, want each once", id)
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

	if _, err := add("One"); !errors.Is(err, full) {
		t.Errorf("UpdateLibrary(refused first write) = %v, want %v", err, full)
	}

	if got := h.store.Count(NamespaceTemplates); got != 0 {
		t.Errorf("after a refused first write the store holds %d library records, want 0", got)
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
		t.Errorf("UpdateLibrary(refused update) = %v, want %v", err, full)
	}

	h.store.BeforeUpdate = nil

	if diff := cmp.Diff([]string{"One", "Two"}, libraryNames(mustLibrary(t, h))); diff != "" {
		t.Errorf("template names after a refused update mismatch (-want +got):\n%s", diff)
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
		t.Errorf("GetLibrary() = %v, %v, want nil and %v", library, err, down)
	}

	runs := 0

	_, err = service.UpdateLibrary(ctx, testActor, func(library *TemplateLibrary) error {
		runs++

		library.Delete(templateIDs(library), nil)

		return nil
	})

	if !errors.Is(err, down) || errors.Is(err, ErrBusy) {
		t.Errorf("UpdateLibrary(unreadable library) = %v, want %v and not ErrBusy", err, down)
	}

	if runs != 0 {
		t.Errorf("UpdateLibrary(unreadable library) ran its change %d times, want 0", runs)
	}

	if got := libraryRecord(t, h).Revision; got != revision {
		t.Errorf("after UpdateLibrary(unreadable library) the record revision = %d, want %d", got, revision)
	}
}

func TestUpdateLibraryRequests(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	nothing := func(*TemplateLibrary) error { return nil }

	canceled, cancel := context.WithCancel(ctx)
	cancel()

	if _, err := h.service.UpdateLibrary(canceled, testActor, nothing); !errors.Is(err, context.Canceled) {
		t.Errorf("UpdateLibrary(canceled context) = %v, want context.Canceled", err)
	}

	if _, err := h.service.GetLibrary(canceled); !errors.Is(err, context.Canceled) {
		t.Errorf("GetLibrary(canceled context) = %v, want context.Canceled", err)
	}

	for _, user := range []string{"", strings.Repeat("u", MaxOwnerLength+1), "a\nb"} {
		if _, err := h.service.UpdateLibrary(ctx, user, nothing); !errors.Is(err, ErrInvalid) {
			t.Errorf("UpdateLibrary(actor %.20q) = %v, want ErrInvalid", user, err)
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
	if err != refused { //nolint:errorlint // the very error, not one wrapping it
		t.Errorf("UpdateLibrary(change that fails) = %v, want the change's own error", err)
	}

	if got := h.store.Count(NamespaceTemplates); got != 0 {
		t.Errorf("after UpdateLibrary(change that fails) the store holds %d library records, want 0", got)
	}

	mustAddTemplates(t, h, testTemplate("PLC"))

	// A change that leaves the library invalid is refused, not stored.
	_, err = h.service.UpdateLibrary(ctx, testActor, func(library *TemplateLibrary) error {
		library.Templates[0].Name = ""

		return nil
	})

	if diff := refusalDiff(LibraryError{Reason: "template 0 is not a valid template"}, err); diff != "" {
		t.Errorf("UpdateLibrary(clear a name) refusal mismatch (-want +got):\n%s", diff)
	}

	// The actor of a change is recorded.
	library, err := h.service.UpdateLibrary(ctx, testPeer, func(library *TemplateLibrary) error {
		library.Delete(templateIDs(library), nil)

		return nil
	})
	if err != nil {
		t.Fatalf("UpdateLibrary as %s returned error: %v", testPeer, err)
	}

	if library.UpdatedBy != testPeer {
		t.Errorf("UpdateLibrary as %s updated by = %q, want %q", testPeer, library.UpdatedBy, testPeer)
	}

	if got := templateIDs(library); len(got) != 0 {
		t.Errorf("UpdateLibrary(delete all) left %q, want nothing", got)
	}
}

// libraryValue returns a library record value built from the stored one with
// edit applied to its decoded form.
func libraryValue(t *testing.T, h *testHarness, edit func(library map[string]any)) []byte {
	t.Helper()

	library := decodedLibraryRecord(t, h)

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
		{name: "not JSON", raw: `{"templates":`},
		{
			name: "trailing content",
			raw:  `{"templates":[],"collections":[],"updated":"2026-10-01T12:00:00Z","updatedBy":"alice"} {}`,
		},
		{name: "an unknown field", edit: func(library map[string]any) { library["extra"] = secret }},
		{
			name: "an actor with a control character",
			edit: func(library map[string]any) { library["updatedBy"] = "a\nb" },
		},
		{
			name: "a template with no name",
			edit: func(library map[string]any) { item(library, "templates", 0)["name"] = "" },
		},
		{
			name: "a template with an invalid color",
			edit: func(library map[string]any) {
				device, _ := item(library, "templates", 0)["device"].(map[string]any)
				device["fillColor"] = secret
			},
		},
		{
			name: "a template with an id that is none",
			edit: func(library map[string]any) { item(library, "templates", 0)["id"] = "../" + secret },
		},
		{
			name: "two templates with one id",
			edit: func(library map[string]any) {
				item(library, "templates", 1)["id"] = item(library, "templates", 0)["id"]
			},
		},
		{
			name: "a template at version 0",
			edit: func(library map[string]any) { item(library, "templates", 0)["version"] = 0 },
		},
		{
			name: "a template whose icon is not an icon name",
			edit: func(library map[string]any) {
				device, _ := item(library, "templates", 0)["device"].(map[string]any)
				device["icon"] = "sha256:" + secret
			},
		},
		{
			name: "a template with a source",
			edit: func(library map[string]any) {
				item(library, "templates", 0)["source"] = map[string]any{"kind": "builtin", "id": "router", "version": 1}
			},
		},
		{
			name: "a template whose updater has a control character",
			edit: func(library map[string]any) { item(library, "templates", 0)["updatedBy"] = "bad\nname" },
		},
		{
			name: "a collection of a template the library does not hold",
			edit: func(library map[string]any) { item(library, "collections", 0)["templateIds"] = []any{"server", secret} },
		},
		{
			name: "a collection of a template twice",
			edit: func(library map[string]any) {
				id := item(library, "templates", 0)["id"]
				item(library, "collections", 0)["templateIds"] = []any{id, id}
			},
		},
		{
			name: "a collection with no name",
			edit: func(library map[string]any) { item(library, "collections", 0)["name"] = " " },
		},
		{
			name: "a collection with a description that is none",
			edit: func(library map[string]any) { item(library, "collections", 0)["description"] = secret + "\x00" },
		},
		{
			name: "a collection at version 0",
			edit: func(library map[string]any) { delete(item(library, "collections", 0), "version") },
		},
		{
			name: "two collections with one id",
			edit: func(library map[string]any) {
				library["collections"] = append(
					library["collections"].([]any),
					item(library, "collections", 0),
				) //nolint:forcetypeassert // test data
			},
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
				if err != nil {
					t.Fatalf("GetLibrary(%s) returned error: %v", tt.name, err)
				}

				if library.Collections == nil {
					t.Errorf("GetLibrary(%s) collections = nil, want an empty list", tt.name)
				}

				return
			}

			if err == nil {
				t.Fatalf("GetLibrary(%s) = %+v, want ErrCorrupt", tt.name, library)
			}

			var corrupt *CorruptError
			if !errors.As(err, &corrupt) || corrupt.Kind != kindLibrary || corrupt.ID != LibraryKey {
				t.Errorf("GetLibrary(%s) = %v, want a CorruptError of the template library", tt.name, err)
			}

			// The error must not leak what the record holds.
			if strings.Contains(err.Error(), damagedLibrarySecret) {
				t.Errorf("GetLibrary(%s) = %v, want an error that does not repeat the record", tt.name, err)
			}

			// A damaged library is not changed, and not replaced by an empty
			// one.
			runs := 0

			_, err = h.service.UpdateLibrary(ctx, testActor, func(library *TemplateLibrary) error {
				runs++

				library.Delete(templateIDs(library), nil)

				return nil
			})

			if !errors.Is(err, ErrCorrupt) {
				t.Errorf("UpdateLibrary(%s) = %v, want ErrCorrupt", tt.name, err)
			}

			if runs != 0 {
				t.Errorf("UpdateLibrary(%s) ran its change %d times, want 0", tt.name, runs)
			}

			if got := libraryRecord(t, h).Revision; got != revision {
				t.Errorf("after UpdateLibrary(%s) the record revision = %d, want %d", tt.name, got, revision)
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

	if _, err := h.service.CleanupOrphanedContent(ctx, false); err != nil {
		t.Errorf("CleanupOrphanedContent() returned error: %v", err)
	}

	if _, err := h.service.CleanupOrphanedDocuments(ctx, nil, false); err != nil {
		t.Errorf("CleanupOrphanedDocuments() returned error: %v", err)
	}

	library := mustLibrary(t, h)

	if diff := cmp.Diff(ids, templateIDs(library)); diff != "" {
		t.Errorf("template ids after the cleanups mismatch (-want +got):\n%s", diff)
	}

	if library.Revision != revision {
		t.Errorf("library revision after the cleanups = %d, want %d", library.Revision, revision)
	}
}

func TestLibraryErrorKinds(t *testing.T) {
	invalid := newLibraryErrorf("template %d is wrong", 3)
	limit := newLibraryLimitErrorf("a library holds at most %d templates", 200)

	if !errors.Is(invalid, ErrInvalid) {
		t.Errorf("errors.Is(%v, ErrInvalid) = false, want true", invalid)
	}

	if errors.Is(invalid, ErrTooLarge) {
		t.Errorf("errors.Is(%v, ErrTooLarge) = true, want false", invalid)
	}

	if !errors.Is(limit, ErrTooLarge) {
		t.Errorf("errors.Is(%v, ErrTooLarge) = false, want true", limit)
	}

	if errors.Is(limit, ErrInvalid) {
		t.Errorf("errors.Is(%v, ErrInvalid) = true, want false", limit)
	}
}
