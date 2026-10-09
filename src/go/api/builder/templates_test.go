package builder

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"phenix/store"
	"phenix/store/recordtest/memrecord"
	"phenix/types/builder"
)

// The ids of the built-in templates, in the order a library starts with.
var builtinIDs = []string{"server", "workstation", "router", "firewall", "external"} //nolint:gochecknoglobals // fixed list

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

// testIcon returns the id and the entry of a custom icon whose image
// depends on seed.
func testIcon(t *testing.T, seed int) (string, builder.Icon) {
	t.Helper()

	png := iconPixel(t, seed)

	return builder.IconID(png), builder.Icon{Name: fmt.Sprintf("icon %d", seed), Data: base64.StdEncoding.EncodeToString(png)}
}

// iconTemplate returns a valid template that names a custom icon.
func iconTemplate(name, icon string) builder.Template {
	template := testTemplate(name)
	template.Device.Icon = icon

	return template
}

// mustLibrary returns the library of a user.
func mustLibrary(t *testing.T, h *testHarness, owner string) *TemplateLibrary {
	t.Helper()

	library, err := h.service.GetLibrary(context.Background(), owner)
	if err != nil {
		t.Fatalf("GetLibrary(%s) returned error: %v", owner, err)
	}

	return library
}

// mustUpdate applies a change to the library of a user.
func mustUpdate(t *testing.T, h *testHarness, owner string, change func(*TemplateLibrary) error) *TemplateLibrary {
	t.Helper()

	library, err := h.service.UpdateLibrary(context.Background(), owner, owner, change)
	if err != nil {
		t.Fatalf("UpdateLibrary(%s) returned error: %v", owner, err)
	}

	return library
}

// mustAddTemplates adds templates to the library of a user and returns
// their ids.
func mustAddTemplates(t *testing.T, h *testHarness, owner string, templates ...builder.Template) []string {
	t.Helper()

	var ids []string

	mustUpdate(t, h, owner, func(library *TemplateLibrary) error {
		var err error

		ids, err = library.AddTemplates(templates, nil, h.service.NewID)

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

// libraryRecord returns the stored record of the library of testOwner.
func libraryRecord(t *testing.T, h *testHarness) store.Record {
	t.Helper()

	record, err := h.store.GetRecord(NamespaceTemplates, LibraryKey(testOwner))
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

func TestLibraryKey(t *testing.T) {
	key := regexp.MustCompile(`^lib/[0-9a-f]{64}$`)
	seen := map[string]string{}

	for _, user := range []string{"a", "a/b", "A", "..", "alice", "alice ", "ålice", "global-admin"} {
		got := LibraryKey(user)

		if !key.MatchString(got) || got != "lib/"+OwnerScope(user) {
			t.Errorf("LibraryKey(%q) = %q, want lib/ and the owner's scope", user, got)
		}

		if other, ok := seen[got]; ok {
			t.Errorf("LibraryKey(%q) and LibraryKey(%q) are both %q", user, other, got)
		}

		seen[got] = user

		if err := store.ValidateRecordKey(got); err != nil {
			t.Errorf("LibraryKey(%q) is not a record key: %v", user, err)
		}
	}
}

// TestLibraryStartsWithTheBuiltins asserts a user with no record is given
// the built-in templates, and that reading writes nothing.
func TestLibraryStartsWithTheBuiltins(t *testing.T) {
	h := newHarness(t)
	library := mustLibrary(t, h, testOwner)

	if library.Owner != testOwner || library.Revision != 0 || !library.Updated.IsZero() || library.UpdatedBy != "" {
		t.Fatalf("a new library = %+v, want the owner's, with no revision and no times", library)
	}

	if len(library.Collections) != 0 || library.Collections == nil || len(library.Icons) != 0 {
		t.Fatalf("a new library has collections %v and icons %v, want none", library.Collections, library.Icons)
	}

	builtins := builder.BuiltinTemplates()

	if got := templateIDs(library); !slices.Equal(got, builtinIDs) || len(builtins) != len(builtinIDs) {
		t.Fatalf("a new library holds %q, want %q", got, builtinIDs)
	}

	for i := range library.Templates {
		template := library.Templates[i]

		if !reflect.DeepEqual(template.Template, builtins[i]) {
			t.Errorf("template %d = %+v, want the built-in %+v", i, template.Template, builtins[i])
		}

		if template.Version != 1 || template.ETag() != `"1"` || !template.Created.IsZero() || !template.Updated.IsZero() ||
			template.Shares != nil || template.Public != nil {
			t.Errorf("template %q = %+v, want version 1, no times, not shared and not published", template.ID, template)
		}
	}

	// Each read is a library of its own.
	library.Templates[0].Name = "changed"
	library.Templates[0].Device.Spec["type"] = "changed"

	if again := mustLibrary(t, h, testOwner); again.Templates[0].Name != "Server" ||
		again.Templates[0].Device.Spec["type"] != "VirtualMachine" {
		t.Fatalf("a second read sees what was done to the first: %+v", again.Templates[0])
	}

	if got := h.store.Count(NamespaceTemplates); got != 0 {
		t.Fatalf("reading wrote %d library records, want 0", got)
	}

	// A change that changes nothing writes nothing either.
	unchanged := mustUpdate(t, h, testOwner, func(library *TemplateLibrary) error {
		library.Delete([]string{"no-such-template"}, []string{"no-such-collection"})

		return nil
	})

	if unchanged.Revision != 0 || h.store.Count(NamespaceTemplates) != 0 {
		t.Fatalf("a change of nothing wrote a record: revision %d", unchanged.Revision)
	}
}

// TestLibraryFirstChangeStoresTheBuiltins asserts the first change creates
// the one record of a library, from the built-in templates.
func TestLibraryFirstChangeStoresTheBuiltins(t *testing.T) {
	h := newHarness(t)

	ids := mustAddTemplates(t, h, testOwner, testTemplate("PLC"))

	if keys := h.store.Keys(NamespaceTemplates); len(keys) != 1 || keys[0] != LibraryKey(testOwner) {
		t.Fatalf("library records = %q, want only %q", keys, LibraryKey(testOwner))
	}

	for _, namespace := range []string{NamespaceDrafts, NamespaceChunks, NamespacePublished, NamespaceIcons} {
		if got := h.store.Count(namespace); got != 0 {
			t.Errorf("%s holds %d records, want 0", namespace, got)
		}
	}

	record := libraryRecord(t, h)
	library := mustLibrary(t, h, testOwner)

	if got, want := templateIDs(library), append(slices.Clone(builtinIDs), ids...); !slices.Equal(got, want) {
		t.Fatalf("the library holds %q, want %q", got, want)
	}

	if library.Revision != record.Revision || library.Updated.IsZero() || library.UpdatedBy != testOwner ||
		library.Updated.Location() != time.UTC {
		t.Fatalf("the stored library = revision %d, updated %v by %q", library.Revision, library.Updated, library.UpdatedBy)
	}

	added := library.Template(ids[0])

	if added.Name != "PLC" || added.Version != 1 || added.Created.IsZero() || !added.Created.Equal(added.Updated) {
		t.Fatalf("the new template = %+v, want version 1 and the time it was added", added)
	}

	// The built-in templates are stored as they were: no times.
	if server := library.Template("server"); server.Version != 1 || !server.Created.IsZero() || !server.Updated.IsZero() {
		t.Fatalf("the stored built-in = %+v, want version 1 and no times", server)
	}

	var stored map[string]json.RawMessage

	if err := json.Unmarshal(record.Value, &stored); err != nil {
		t.Fatalf("the library record is not JSON: %v", err)
	}

	for _, field := range []string{"owner", "templates", "collections", "updated", "updatedBy"} {
		if _, ok := stored[field]; !ok {
			t.Errorf("the library record has no %q", field)
		}
	}

	if len(stored) != 5 || string(stored["collections"]) != "[]" {
		t.Errorf("the library record = %s, want five fields and an empty list of collections", record.Value)
	}

	// Only the new template has times in the record.
	if got := strings.Count(string(record.Value), `"created"`); got != 1 {
		t.Errorf("the record holds %d creation times, want only the new template's: %s", got, record.Value)
	}
}

// TestLibraryDeletedBuiltinStaysDeleted asserts deleting a built-in is an
// ordinary delete: it is gone from the next read and after every later
// write, also when the delete was the library's first change.
func TestLibraryDeletedBuiltinStaysDeleted(t *testing.T) {
	h := newHarness(t)

	var templates, collections int

	mustUpdate(t, h, testOwner, func(library *TemplateLibrary) error {
		templates, collections = library.Delete([]string{"router", "external"}, nil)

		return nil
	})

	want := []string{"server", "workstation", "firewall"}

	if templates != 2 || collections != 0 {
		t.Fatalf("Delete removed %d templates and %d collections, want 2 and 0", templates, collections)
	}

	if got := templateIDs(mustLibrary(t, h, testOwner)); !slices.Equal(got, want) {
		t.Fatalf("after the delete the library holds %q, want %q", got, want)
	}

	ids := mustAddTemplates(t, h, testOwner, testTemplate("PLC"))

	mustUpdate(t, h, testOwner, func(library *TemplateLibrary) error {
		return library.ReplaceTemplate("server", testTemplate("Server two"), nil)
	})

	if got := templateIDs(mustLibrary(t, h, testOwner)); !slices.Equal(got, append(want, ids...)) {
		t.Fatalf("after later writes the library holds %q, want %q", got, append(want, ids...))
	}

	// Another user still starts with all five.
	if got := templateIDs(mustLibrary(t, h, testPeer)); !slices.Equal(got, builtinIDs) {
		t.Fatalf("another user's library holds %q, want %q", got, builtinIDs)
	}

	// Deleting every template leaves an empty library, not the built-ins.
	mustUpdate(t, h, testOwner, func(library *TemplateLibrary) error {
		library.Delete(templateIDs(library), nil)

		return nil
	})

	if library := mustLibrary(t, h, testOwner); len(library.Templates) != 0 || library.Templates == nil {
		t.Fatalf("an emptied library holds %q, want an empty list", templateIDs(library))
	}
}

func TestLibraryAddTemplates(t *testing.T) {
	h := newHarness(t)
	library := mustLibrary(t, h, testOwner)

	first, second := testTemplate("One"), testTemplate("Two")
	first.ID = "server"

	ids, err := library.AddTemplates([]builder.Template{first, second}, nil, h.service.NewID)
	if err != nil {
		t.Fatalf("AddTemplates returned error: %v", err)
	}

	// Each gets a new id, whatever it came with, and they go last, in order.
	if len(ids) != 2 || ids[0] == ids[1] || slices.Contains(builtinIDs, ids[0]) ||
		!slices.Equal(templateIDs(library), append(slices.Clone(builtinIDs), ids...)) {
		t.Fatalf("AddTemplates gave %q in %q", ids, templateIDs(library))
	}

	if library.Template(ids[0]).Name != "One" || library.Template(ids[1]).Name != "Two" || first.ID != "server" {
		t.Fatalf("the new templates = %+v", library.Templates[len(builtinIDs):])
	}

	if added, err := library.AddTemplates(nil, nil, h.service.NewID); err != nil || len(added) != 0 {
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
			name:      "a custom icon nothing carries",
			templates: []builder.Template{testTemplate("Fine"), iconTemplate("Drawn", "sha256:"+strings.Repeat("a", 64))},
			want: `template 1 names custom icon "sha256:` + strings.Repeat("a", 64) +
				`", which the request does not carry`,
		},
		{
			name:      "a custom icon with a long name",
			templates: []builder.Template{iconTemplate("Drawn", strings.Repeat("é", 100))},
			want:      `template 0 names custom icon "` + strings.Repeat("é", 40) + `...", which the request does not carry`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			library := mustLibrary(t, h, testOwner)

			_, err := library.AddTemplates(tt.templates, nil, h.service.NewID)

			if reason, limit := refusal(t, err); !strings.HasPrefix(reason, tt.want) || limit != tt.limit {
				t.Fatalf("refused with %q (limit %v), want %q", reason, limit, tt.want)
			}

			// A refusal leaves the library as it was.
			if got := templateIDs(library); !slices.Equal(got, builtinIDs) || len(library.Icons) != 0 {
				t.Fatalf("a refused change left %q and %d icons", got, len(library.Icons))
			}
		})
	}

	// An id source that fails, or gives an id the library has, fails the
	// change rather than replacing a template.
	broken := errors.New("no more ids")

	if _, err := library.AddTemplates(
		[]builder.Template{testTemplate("X")}, nil, func() (string, error) { return "", broken },
	); !errors.Is(err, broken) {
		t.Fatalf("a failing id source = %v, want its error", err)
	}

	if _, err := library.AddTemplates(
		[]builder.Template{testTemplate("X")}, nil, func() (string, error) { return "server", nil },
	); err == nil || errors.Is(err, ErrInvalid) {
		t.Fatalf("an id the library has = %v, want an error that is not the caller's", err)
	}
}

func TestLibraryReplaceTemplate(t *testing.T) {
	h := newHarness(t)
	ids := mustAddTemplates(t, h, testOwner, testTemplate("PLC"))
	before := mustLibrary(t, h, testOwner).Template(ids[0])

	h.now.Add(10)

	content := testTemplate("PLC two")
	content.ID = "ignored"
	content.Description = ""

	updated := mustUpdate(t, h, testOwner, func(library *TemplateLibrary) error {
		return library.ReplaceTemplate(ids[0], content, nil)
	})

	after := updated.Template(ids[0])

	// The content changed: one version more, and the time of the change.
	if after.ID != ids[0] || after.Name != "PLC two" || after.Description != "" || after.Version != before.Version+1 ||
		after.ETag() != `"2"` || !after.Created.Equal(before.Created) || !after.Updated.After(before.Updated) {
		t.Fatalf("the replaced template = %+v, was %+v", after, before)
	}

	if updated.Template("ignored") != nil || updated.Template("server").Version != 1 {
		t.Fatalf("replacing one template changed another: %q", templateIDs(updated))
	}

	// The same content again changes nothing, and writes nothing.
	revision := updated.Revision

	same := mustUpdate(t, h, testOwner, func(library *TemplateLibrary) error {
		return library.ReplaceTemplate(ids[0], content, nil)
	})

	if same.Revision != revision || same.Template(ids[0]).Version != after.Version ||
		libraryRecord(t, h).Revision != revision || !same.Updated.Equal(updated.Updated) {
		t.Fatalf("replacing with the same content wrote revision %d, version %d", same.Revision, same.Template(ids[0]).Version)
	}

	// A built-in is edited like any other.
	edited := mustUpdate(t, h, testOwner, func(library *TemplateLibrary) error {
		return library.ReplaceTemplate("router", testTemplate("Edge router"), nil)
	})

	if router := edited.Template("router"); router.Name != "Edge router" || router.Version != 2 ||
		!router.Created.IsZero() || router.Updated.IsZero() {
		t.Fatalf("the edited built-in = %+v, want version 2 and the time of the change", router)
	}

	library := mustLibrary(t, h, testOwner)

	if err := library.ReplaceTemplate("no-such", content, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("replacing a template the library does not hold = %v, want ErrNotFound", err)
	}

	err := library.ReplaceTemplate(ids[0], testTemplate(""), nil)

	if reason, limit := refusal(t, err); reason != "template.name: template name is required" || limit {
		t.Fatalf("an invalid replacement is refused with %q", reason)
	}

	err = library.ReplaceTemplate(ids[0], iconTemplate("Drawn", "sha256:"+strings.Repeat("b", 64)), nil)

	if reason, _ := refusal(t, err); !strings.HasPrefix(reason, `template 0 names custom icon "sha256:bbbb`) {
		t.Fatalf("a missing icon is refused with %q", reason)
	}

	if library.Template(ids[0]).Name != "PLC two" {
		t.Fatalf("a refused replacement changed the template: %+v", library.Template(ids[0]))
	}
}

func TestLibraryCollections(t *testing.T) {
	h := newHarness(t)
	ids := mustAddTemplates(t, h, testOwner, testTemplate("One"), testTemplate("Two"))

	var collection string

	created := mustUpdate(t, h, testOwner, func(library *TemplateLibrary) error {
		var err error

		collection, err = library.AddCollection(CollectionContent{
			Name: "Plant floor", Description: "PLCs", TemplateIDs: []string{ids[1], "server"},
		}, h.service.NewID)

		return err
	})

	first := created.Collection(collection)

	if first == nil || first.Name != "Plant floor" || first.Description != "PLCs" ||
		!slices.Equal(first.TemplateIDs, []string{ids[1], "server"}) || first.Version != 1 || first.ETag() != `"1"` ||
		first.Created.IsZero() || !first.Created.Equal(first.Updated) || first.Shares != nil || first.Public != nil {
		t.Fatalf("the new collection = %+v", first)
	}

	// A collection is not a change of its templates.
	if template := created.Template(ids[1]); template.Version != 1 {
		t.Fatalf("adding a collection changed its template: %+v", template)
	}

	h.now.Add(10)

	replaced := mustUpdate(t, h, testOwner, func(library *TemplateLibrary) error {
		return library.ReplaceCollection(collection, CollectionContent{Name: "Floor", Description: "", TemplateIDs: []string{ids[0]}})
	})

	second := replaced.Collection(collection)

	if second.Name != "Floor" || second.Description != "" || !slices.Equal(second.TemplateIDs, []string{ids[0]}) ||
		second.Version != 2 || !second.Created.Equal(first.Created) || !second.Updated.After(first.Updated) {
		t.Fatalf("the replaced collection = %+v, was %+v", second, first)
	}

	// An empty collection is one, and encodes its list as a list.
	emptied := mustUpdate(t, h, testOwner, func(library *TemplateLibrary) error {
		return library.ReplaceCollection(collection, CollectionContent{Name: "Floor", Description: "", TemplateIDs: nil})
	})

	if got := emptied.Collection(collection); got.Version != 3 || got.TemplateIDs == nil || len(got.TemplateIDs) != 0 ||
		!strings.Contains(string(libraryRecord(t, h).Value), `"templateIds":[]`) {
		t.Fatalf("the emptied collection = %+v", got)
	}

	same := mustUpdate(t, h, testOwner, func(library *TemplateLibrary) error {
		return library.ReplaceCollection(collection, CollectionContent{Name: "Floor", Description: "", TemplateIDs: []string{}})
	})

	if same.Revision != emptied.Revision || same.Collection(collection).Version != 3 {
		t.Fatalf("replacing a collection with the same content wrote revision %d", same.Revision)
	}

	library := mustLibrary(t, h, testOwner)

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
	ids := mustAddTemplates(t, h, testOwner, testTemplate("One"))

	var collection string

	mustUpdate(t, h, testOwner, func(library *TemplateLibrary) error {
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
			content: CollectionContent{Name: "X", Description: "", TemplateIDs: []string{"server", "nowhere"}},
			want:    `collection names template "nowhere", which this library does not hold`,
		},
		{
			name:    "a template twice",
			content: CollectionContent{Name: "X", Description: "", TemplateIDs: []string{"server", ids[0], "server"}},
			want:    `collection names template "server" more than once`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			library := mustLibrary(t, h, testOwner)

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
	ids := mustAddTemplates(t, h, testOwner, testTemplate("One"), testTemplate("Two"), testTemplate("Three"))

	var both, single string

	mustUpdate(t, h, testOwner, func(library *TemplateLibrary) error {
		var err error

		both, err = library.AddCollection(
			CollectionContent{Name: "Both", Description: "", TemplateIDs: []string{ids[0], ids[1], "server"}}, h.service.NewID,
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

	deleted := mustUpdate(t, h, testOwner, func(library *TemplateLibrary) error {
		templates, collections = library.Delete([]string{ids[1], "server", "no-such"}, []string{single, "no-such"})

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
	if deleted.Template(ids[2]) == nil || deleted.Template(ids[1]) != nil || deleted.Template("server") != nil {
		t.Fatalf("after the delete the library holds %q", templateIDs(deleted))
	}

	// The same delete again removes nothing and writes nothing.
	again := mustUpdate(t, h, testOwner, func(library *TemplateLibrary) error {
		templates, collections = library.Delete([]string{ids[1], "server"}, []string{single})

		return nil
	})

	if templates != 0 || collections != 0 || again.Revision != deleted.Revision {
		t.Fatalf("the same delete again removed %d and %d at revision %d", templates, collections, again.Revision)
	}
}

// TestLibraryIcons asserts the custom icons of a library's templates travel
// in its record, and leave it with the last template that names them.
func TestLibraryIcons(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	usedID, used := testIcon(t, 1)
	spareID, spare := testIcon(t, 2)
	icons := map[string]builder.Icon{usedID: used, spareID: spare}

	var ids []string

	added := mustUpdate(t, h, testOwner, func(library *TemplateLibrary) error {
		var err error

		ids, err = library.AddTemplates(
			[]builder.Template{iconTemplate("One", usedID), iconTemplate("Two", usedID), testTemplate("Plain")}, icons, h.service.NewID,
		)

		return err
	})

	// The icon the templates name is kept once; the one nothing names is not.
	if len(added.Icons) != 1 || added.Icons[usedID] != used {
		t.Fatalf("the library carries %v, want only %s", added.Icons, usedID)
	}

	if stored := mustLibrary(t, h, testOwner); len(stored.Icons) != 1 || stored.Icons[usedID] != used ||
		len(builder.ValidateIcons(stored.Icons, "icons")) != 0 {
		t.Fatalf("the stored library carries %v", stored.Icons)
	}

	// A later template may name an icon the library already holds.
	more := mustUpdate(t, h, testOwner, func(library *TemplateLibrary) error {
		return library.ReplaceTemplate(ids[2], iconTemplate("Plain", usedID), nil)
	})

	if len(more.Icons) != 1 {
		t.Fatalf("after naming a held icon the library carries %v", more.Icons)
	}

	// Replacing a template's icon brings the new one in.
	swapped := mustUpdate(t, h, testOwner, func(library *TemplateLibrary) error {
		return library.ReplaceTemplate(ids[2], iconTemplate("Plain", spareID), icons)
	})

	if len(swapped.Icons) != 2 || swapped.Icons[spareID] != spare {
		t.Fatalf("after a swap the library carries %v", swapped.Icons)
	}

	// The icon stays while one template names it, and goes with the last.
	one := mustUpdate(t, h, testOwner, func(library *TemplateLibrary) error {
		library.Delete([]string{ids[0], ids[2]}, nil)

		return nil
	})

	if len(one.Icons) != 1 || one.Icons[usedID] != used {
		t.Fatalf("after deleting two templates the library carries %v", one.Icons)
	}

	none := mustUpdate(t, h, testOwner, func(library *TemplateLibrary) error {
		library.Delete([]string{ids[1]}, nil)

		return nil
	})

	if none.Icons != nil || strings.Contains(string(libraryRecord(t, h).Value), `"icons"`) {
		t.Fatalf("after deleting the last template the library carries %v", none.Icons)
	}

	// An icon that is not one a document accepts is refused, with nothing
	// written.
	revision := none.Revision
	wrongID := "sha256:" + strings.Repeat("c", 64)

	for name, entry := range map[string]builder.Icon{
		"another image's id": used,
		"not base64":         {Name: "", Data: "not base64!"},
		"not a PNG":          {Name: "", Data: base64.StdEncoding.EncodeToString([]byte("<svg/>"))},
	} {
		_, err := h.service.UpdateLibrary(ctx, testOwner, testOwner, func(library *TemplateLibrary) error {
			_, err := library.AddTemplates(
				[]builder.Template{iconTemplate("Bad", wrongID)}, map[string]builder.Icon{wrongID: entry}, h.service.NewID,
			)

			return err
		})

		if reason, limit := refusal(t, err); !strings.HasPrefix(reason, `icons: icon "sha256:cccc`) || limit {
			t.Errorf("%s: refused with %q", name, reason)
		}
	}

	if libraryRecord(t, h).Revision != revision {
		t.Fatal("a refused icon wrote the library")
	}
}

// TestLibraryLimits asserts each bound of a library: its templates, its
// collections, the templates of one collection, its icons and its record.
func TestLibraryLimits(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	fill := make([]builder.Template, 0, MaxLibraryTemplates)
	for i := range MaxLibraryTemplates - len(builtinIDs) {
		fill = append(fill, testTemplate(fmt.Sprintf("T%d", i)))
	}

	ids := mustAddTemplates(t, h, testOwner, fill...)

	if library := mustLibrary(t, h, testOwner); len(library.Templates) != MaxLibraryTemplates {
		t.Fatalf("the library holds %d templates, want %d", len(library.Templates), MaxLibraryTemplates)
	}

	_, err := h.service.UpdateLibrary(ctx, testOwner, testOwner, func(library *TemplateLibrary) error {
		_, err := library.AddTemplates([]builder.Template{testTemplate("One more")}, nil, h.service.NewID)

		return err
	})

	if reason, limit := refusal(t, err); reason != "a library holds at most 200 templates" || !limit {
		t.Fatalf("the 201st template is refused with %q (limit %v)", reason, limit)
	}

	// A collection of every template is the largest one.
	all := append(slices.Clone(builtinIDs), ids...)

	for i := range MaxLibraryCollections {
		mustUpdate(t, h, testOwner, func(library *TemplateLibrary) error {
			_, err := library.AddCollection(
				CollectionContent{Name: fmt.Sprintf("C%d", i), Description: "", TemplateIDs: all[:1+i%3]}, h.service.NewID,
			)

			return err
		})
	}

	_, err = h.service.UpdateLibrary(ctx, testOwner, testOwner, func(library *TemplateLibrary) error {
		_, err := library.AddCollection(CollectionContent{Name: "One more", Description: "", TemplateIDs: nil}, h.service.NewID)

		return err
	})

	if reason, limit := refusal(t, err); reason != "a library holds at most 50 collections" || !limit {
		t.Fatalf("the 51st collection is refused with %q (limit %v)", reason, limit)
	}

	library := mustLibrary(t, h, testOwner)
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

func TestLibraryIconLimit(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	icons := map[string]builder.Icon{}
	templates := make([]builder.Template, 0, MaxLibraryTemplateIcons+1)

	for i := range MaxLibraryTemplateIcons + 1 {
		id, icon := testIcon(t, 100+i)
		icons[id] = icon
		templates = append(templates, iconTemplate(fmt.Sprintf("T%d", i), id))
	}

	add := func(templates []builder.Template) error {
		_, err := h.service.UpdateLibrary(ctx, testOwner, testOwner, func(library *TemplateLibrary) error {
			_, err := library.AddTemplates(templates, icons, h.service.NewID)

			return err
		})

		return err
	}

	if err := add(templates[:MaxLibraryTemplateIcons]); err != nil {
		t.Fatalf("a library with %d icons is refused: %v", MaxLibraryTemplateIcons, err)
	}

	if library := mustLibrary(t, h, testOwner); len(library.Icons) != MaxLibraryTemplateIcons {
		t.Fatalf("the library carries %d icons, want %d", len(library.Icons), MaxLibraryTemplateIcons)
	}

	err := add(templates[MaxLibraryTemplateIcons:])

	if reason, limit := refusal(t, err); reason != "a library holds at most 50 custom icons" || !limit {
		t.Fatalf("the 51st icon is refused with %q (limit %v)", reason, limit)
	}

	// One more template with an icon the library has is not one more icon.
	if err := add([]builder.Template{iconTemplate("Again", templates[0].Device.Icon)}); err != nil {
		t.Fatalf("a template with a held icon is refused: %v", err)
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

	mustAddTemplates(t, h, testOwner, fits...)

	record := libraryRecord(t, h)

	if len(record.Value) > MaxMetadataBytes || len(record.Value) < MaxMetadataBytes-each {
		t.Fatalf("the library record is %d bytes, want it just below %d", len(record.Value), MaxMetadataBytes)
	}

	_, err := h.service.UpdateLibrary(ctx, testOwner, testOwner, func(library *TemplateLibrary) error {
		_, err := library.AddTemplates([]builder.Template{paddedTemplate("One more", each)}, nil, h.service.NewID)

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
				mustAddTemplates(t, h, testOwner, testTemplate("Earlier"))
			}

			// Another request of the owner lands between this one's read and
			// its write.
			race := func(string, string) error {
				h.store.BeforeCreate, h.store.BeforeUpdate = nil, nil
				mustAddTemplates(t, h, testOwner, testTemplate("Racer"))

				return nil
			}

			h.store.BeforeCreate, h.store.BeforeUpdate = race, race

			runs := 0

			library := mustUpdate(t, h, testOwner, func(library *TemplateLibrary) error {
				runs++

				_, err := library.AddTemplates([]builder.Template{testTemplate("Mine")}, nil, h.service.NewID)

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

			if stored := mustLibrary(t, h, testOwner); stored.Revision != library.Revision ||
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
	mustAddTemplates(t, h, testOwner, testTemplate("Earlier"))

	racing := false

	h.store.BeforeUpdate = func(string, string) error {
		if racing {
			return nil
		}

		racing = true
		defer func() { racing = false }()

		mustUpdate(t, h, testOwner, func(library *TemplateLibrary) error {
			return library.ReplaceTemplate("server", testTemplate(fmt.Sprintf("Server %d", h.now.Load())), nil)
		})

		return nil
	}

	runs := 0

	_, err := h.service.UpdateLibrary(context.Background(), testOwner, testOwner, func(library *TemplateLibrary) error {
		runs++

		_, err := library.AddTemplates([]builder.Template{testTemplate("Mine")}, nil, h.service.NewID)

		return err
	})

	if !errors.Is(err, ErrBusy) || runs != maxLibraryAttempts || maxLibraryAttempts != 5 {
		t.Fatalf("after %d runs: %v, want ErrBusy after 5", runs, err)
	}

	h.store.BeforeUpdate = nil

	for _, template := range mustLibrary(t, h, testOwner).Templates {
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
				_, err := h.service.UpdateLibrary(context.Background(), testOwner, testOwner, func(library *TemplateLibrary) error {
					_, err := library.AddTemplates([]builder.Template{testTemplate(fmt.Sprintf("W%d", i))}, nil, h.service.NewID)

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

	library := mustLibrary(t, h, testOwner)

	if len(library.Templates) != len(builtinIDs)+writers {
		t.Fatalf("the library holds %q, want the built-ins and one template of each writer", templateIDs(library))
	}

	seen := map[string]bool{}
	for _, id := range templateIDs(library) {
		if seen[id] {
			t.Fatalf("template id %q is used twice", id)
		}

		seen[id] = true
	}
}

// TestUpdateLibraryStoreFailures asserts what a failed write returns: the
// store's error, or the library when the write was applied after all.
func TestUpdateLibraryStoreFailures(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	add := func(name string) (*TemplateLibrary, error) {
		return h.service.UpdateLibrary(ctx, testOwner, testOwner, func(library *TemplateLibrary) error {
			_, err := library.AddTemplates([]builder.Template{testTemplate(name)}, nil, h.service.NewID)

			return err
		})
	}

	// What the Etcd store returns once etcd is out of space.
	noSpace := func(namespace, key string) error {
		return fmt.Errorf("writing record %s/%s in Etcd: %w", namespace, key, store.ErrNoSpace)
	}

	h.store.BeforeCreate = noSpace

	if _, err := add("One"); !errors.Is(err, store.ErrNoSpace) || h.store.Count(NamespaceTemplates) != 0 {
		t.Fatalf("the first write out of space = %v, want the store's error and no record", err)
	}

	h.store.BeforeCreate = nil

	// A write that was applied before its error is the library's.
	timeout := errors.New("request timed out")
	h.store.AfterWrite = func(string, string) error { return timeout }

	created, err := add("One")
	if err != nil || created.Revision != libraryRecord(t, h).Revision {
		t.Fatalf("a create applied before its error = %v, want the stored library", err)
	}

	updated, err := add("Two")
	if err != nil || updated.Revision != libraryRecord(t, h).Revision || updated.Revision == created.Revision {
		t.Fatalf("an update applied before its error = %v, want the stored library", err)
	}

	h.store.AfterWrite = nil
	h.store.BeforeUpdate = noSpace

	if _, err := add("Three"); !errors.Is(err, store.ErrNoSpace) {
		t.Fatalf("an update out of space = %v, want the store's error", err)
	}

	h.store.BeforeUpdate = nil

	names := make([]string, 0, 2)
	for _, template := range mustLibrary(t, h, testOwner).Templates[len(builtinIDs):] {
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

	mustAddTemplates(t, h, testOwner, testTemplate("PLC"))

	revision := libraryRecord(t, h).Revision
	down := errors.New("the store is down")

	service, err := New(WithStore(&unreadableStore{Store: h.store, err: down}))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	if library, err := service.GetLibrary(ctx, testOwner); !errors.Is(err, down) || library != nil {
		t.Fatalf("GetLibrary = %v, %v, want the store's error", library, err)
	}

	if _, err := service.GetLibraryByKey(ctx, OwnerScope(testOwner)); !errors.Is(err, down) {
		t.Fatalf("GetLibraryByKey = %v, want the store's error", err)
	}

	runs := 0

	_, err = service.UpdateLibrary(ctx, testOwner, testOwner, func(library *TemplateLibrary) error {
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

	if _, err := h.service.UpdateLibrary(canceled, testOwner, testOwner, nothing); !errors.Is(err, context.Canceled) {
		t.Errorf("UpdateLibrary with a canceled context = %v", err)
	}

	if _, err := h.service.GetLibrary(canceled, testOwner); !errors.Is(err, context.Canceled) {
		t.Errorf("GetLibrary with a canceled context = %v", err)
	}

	if _, err := h.service.GetLibraryByKey(canceled, OwnerScope(testOwner)); !errors.Is(err, context.Canceled) {
		t.Errorf("GetLibraryByKey with a canceled context = %v", err)
	}

	for _, user := range []string{"", strings.Repeat("u", MaxOwnerLength+1), "a\nb"} {
		if _, err := h.service.GetLibrary(ctx, user); !errors.Is(err, ErrInvalid) {
			t.Errorf("GetLibrary(%.20q) = %v, want ErrInvalid", user, err)
		}

		if _, err := h.service.UpdateLibrary(ctx, user, testOwner, nothing); !errors.Is(err, ErrInvalid) {
			t.Errorf("UpdateLibrary of owner %.20q = %v, want ErrInvalid", user, err)
		}

		if _, err := h.service.UpdateLibrary(ctx, testOwner, user, nothing); !errors.Is(err, ErrInvalid) {
			t.Errorf("UpdateLibrary by actor %.20q = %v, want ErrInvalid", user, err)
		}
	}

	// What a change returns is returned as it is, and nothing is written.
	refused := errors.New("the caller's own refusal")

	_, err := h.service.UpdateLibrary(ctx, testOwner, testOwner, func(library *TemplateLibrary) error {
		library.Delete(templateIDs(library), nil)

		return refused
	})
	if err != refused || h.store.Count(NamespaceTemplates) != 0 { //nolint:errorlint // the very error, not one wrapping it
		t.Fatalf("a refused change = %v, want the change's own error and no record", err)
	}

	// A change that leaves the library invalid is refused, not stored.
	_, err = h.service.UpdateLibrary(ctx, testOwner, testOwner, func(library *TemplateLibrary) error {
		library.Templates[0].Name = ""

		return nil
	})

	if reason, limit := refusal(t, err); reason != "template 0 is not a valid template" || limit {
		t.Fatalf("an invalid library is refused with %q", reason)
	}

	// The actor of a change is recorded; it need not be the owner.
	library, err := h.service.UpdateLibrary(ctx, testOwner, testPeer, func(library *TemplateLibrary) error {
		library.Delete([]string{"server"}, nil)

		return nil
	})
	if err != nil || library.Owner != testOwner || library.UpdatedBy != testPeer {
		t.Fatalf("a change by another actor = %+v, %v", library, err)
	}

	if got := h.store.Keys(NamespaceTemplates); len(got) != 1 || got[0] != LibraryKey(testOwner) {
		t.Fatalf("library records = %q, want only the owner's", got)
	}
}

func TestGetLibraryByKey(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// A user with no record has no library to find by key.
	for _, scope := range []string{
		OwnerScope(testOwner), "", "lib", strings.ToUpper(OwnerScope(testOwner)), OwnerScope(testOwner)[:63], "../" + OwnerScope(testOwner),
	} {
		if _, err := h.service.GetLibraryByKey(ctx, scope); !errors.Is(err, ErrNotFound) {
			t.Errorf("GetLibraryByKey(%q) = %v, want ErrNotFound", scope, err)
		}
	}

	ids := mustAddTemplates(t, h, testOwner, testTemplate("PLC"))

	library, err := h.service.GetLibraryByKey(ctx, OwnerScope(testOwner))
	if err != nil || library.Owner != testOwner || library.Template(ids[0]) == nil {
		t.Fatalf("GetLibraryByKey of a stored library = %+v, %v", library, err)
	}

	if _, err := h.service.GetLibraryByKey(ctx, OwnerScope(testPeer)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetLibraryByKey of another user = %v, want ErrNotFound", err)
	}
}

// TestLibrariesAreSeparate asserts each user has a library of its own, also
// when one name starts with another.
func TestLibrariesAreSeparate(t *testing.T) {
	h := newHarness(t)

	mustAddTemplates(t, h, "a", testTemplate("Of a"))
	mustAddTemplates(t, h, "a/b", testTemplate("Of a/b"))

	for owner, want := range map[string]string{"a": "Of a", "a/b": "Of a/b"} {
		library := mustLibrary(t, h, owner)

		if library.Owner != owner || len(library.Templates) != len(builtinIDs)+1 ||
			library.Templates[len(builtinIDs)].Name != want {
			t.Errorf("the library of %q = %q", owner, templateIDs(library))
		}
	}

	if got := h.store.Count(NamespaceTemplates); got != 2 {
		t.Fatalf("%d library records, want 2", got)
	}
}

// libraryValue returns a library record value built from the stored one of
// testOwner with edit applied to its decoded form.
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

// damagedLibraryIconSeed is the seed of the icon the library of
// TestLibraryRefusesDamagedRecords carries.
const damagedLibraryIconSeed = 3

// damagedLibraryCase is one record that is, or is not, a library this
// package stores: raw, or the stored record with edit applied.
type damagedLibraryCase struct {
	name  string
	raw   string
	edit  func(library map[string]any)
	want  string
	valid bool
}

// damagedLibraryCases returns the records TestLibraryRefusesDamagedRecords
// puts in place of a library whose sixth template names a custom icon and
// whose one collection holds that template.
func damagedLibraryCases(t *testing.T) []damagedLibraryCase {
	t.Helper()

	const secret = damagedLibrarySecret

	iconID, _ := testIcon(t, damagedLibraryIconSeed)

	item := func(library map[string]any, list string, index int) map[string]any {
		items, _ := library[list].([]any)
		entry, _ := items[index].(map[string]any)

		return entry
	}

	share := func(user string) map[string]any {
		return map[string]any{"user": user, "userCreated": "2026-10-01T12:00:00Z", "grantedAt": "2026-10-01T12:00:00Z"}
	}

	shares := func(count int) []any {
		list := make([]any, 0, count)
		for i := range count {
			list = append(list, share(fmt.Sprintf("user-%02d", i)))
		}

		return list
	}

	return []damagedLibraryCase{
		{name: "not JSON", raw: `{"owner":`, want: "is not valid JSON"},
		{
			name: "trailing content",
			raw:  `{"owner":"alice","templates":[],"collections":[],"updated":"2026-10-01T12:00:00Z","updatedBy":"alice"} {}`,
			want: "trailing content",
		},
		{name: "an unknown field", edit: func(library map[string]any) { library["extra"] = secret }, want: "is not valid JSON"},
		{
			name: "another owner",
			edit: func(library map[string]any) { library["owner"] = testPeer },
			want: "the library names another owner",
		},
		{name: "no owner", edit: func(library map[string]any) { delete(library, "owner") }, want: "the library has no usable owner"},
		{
			name: "an actor with a control character",
			edit: func(library map[string]any) { library["updatedBy"] = "a\nb" },
			want: "the library has an unusable actor",
		},
		{
			name: "a template with no name",
			edit: func(library map[string]any) { item(library, "templates", 5)["name"] = "" },
			want: "template 5 is not a valid template",
		},
		{
			name: "a template with an invalid color",
			edit: func(library map[string]any) {
				device, _ := item(library, "templates", 5)["device"].(map[string]any)
				device["fillColor"] = secret
			},
			want: "template 5 is not a valid template",
		},
		{
			name: "a template with an id that is none",
			edit: func(library map[string]any) { item(library, "templates", 5)["id"] = "../" + secret },
			want: "template 5 has an invalid identifier",
		},
		{
			name: "two templates with one id",
			edit: func(library map[string]any) { item(library, "templates", 5)["id"] = "server" },
			want: "template 5 repeats an identifier",
		},
		{
			name: "a template at version 0",
			edit: func(library map[string]any) { item(library, "templates", 5)["version"] = 0 },
			want: "template 5 has version 0",
		},
		{
			name: "a template whose icon the library does not carry",
			edit: func(library map[string]any) { delete(library, "icons") },
			want: "template 5 names a custom icon the library does not carry",
		},
		{
			name: "an icon that does not match its id",
			edit: func(library map[string]any) {
				_, other := testIcon(t, 77)
				library["icons"] = map[string]any{iconID: map[string]any{"data": other.Data}}
			},
			want: "a custom icon is not one a document accepts",
		},
		{
			name: "a share with the owner",
			edit: func(library map[string]any) { item(library, "templates", 5)["shares"] = []any{share(testOwner)} },
			want: "template 5 has a share 0 that names the owner",
		},
		{
			name: "shares out of order",
			edit: func(library map[string]any) {
				item(library, "templates", 5)["shares"] = []any{share("carol"), share("bob")}
			},
			want: "template 5 has a share 1 that is out of order or repeats a user",
		},
		{
			name: "a share with a user that is none",
			edit: func(library map[string]any) { item(library, "templates", 5)["shares"] = []any{share("a/" + secret)} },
			want: "template 5 has a share 0 that names an invalid user",
		},
		{
			name: "a share bound to no account",
			edit: func(library map[string]any) {
				item(library, "templates", 5)["shares"] = []any{
					map[string]any{"user": "bob", "userCreated": "", "grantedAt": "2026-10-01T12:00:00Z"},
				}
			},
			want: "template 5 has a share 0 with no usable account binding",
		},
		{
			name: "26 shares",
			edit: func(library map[string]any) { item(library, "collections", 0)["shares"] = shares(MaxShares + 1) },
			want: "collection 0 is shared with 26 users, more than 25",
		},
		{
			name: "published by nobody",
			edit: func(library map[string]any) {
				item(library, "collections", 0)["public"] = map[string]any{"at": "2026-10-01T12:00:00Z", "by": ""}
			},
			want: "collection 0 is published by no usable user",
		},
		{
			name: "a collection of a template the library does not hold",
			edit: func(library map[string]any) { item(library, "collections", 0)["templateIds"] = []any{"server", secret} },
			want: "collection 0 names a template the library does not hold, or one twice",
		},
		{
			name: "a collection of a template twice",
			edit: func(library map[string]any) {
				item(library, "collections", 0)["templateIds"] = []any{"server", "server"}
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
		// What a later version may write, which this one reads.
		{
			name: "25 shares and a publication",
			edit: func(library map[string]any) {
				item(library, "templates", 5)["shares"] = shares(MaxShares)
				item(library, "templates", 5)["public"] = map[string]any{"at": "2026-10-01T12:00:00Z", "by": testPeer}
			},
			valid: true,
		},
		{
			name:  "lists that are null",
			edit:  func(library map[string]any) { library["collections"] = nil },
			valid: true,
		},
	}
}

// TestLibraryRefusesDamagedRecords asserts a record that is not a library
// this package stores is never read leniently: reading and changing it both
// fail, and the error repeats nothing the record holds.
func TestLibraryRefusesDamagedRecords(t *testing.T) {
	iconID, icon := testIcon(t, damagedLibraryIconSeed)

	for _, tt := range damagedLibraryCases(t) {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			ctx := context.Background()

			mustUpdate(t, h, testOwner, func(library *TemplateLibrary) error {
				ids, err := library.AddTemplates(
					[]builder.Template{iconTemplate("Drawn", iconID)}, map[string]builder.Icon{iconID: icon}, h.service.NewID,
				)
				if err != nil {
					return err
				}

				_, err = library.AddCollection(CollectionContent{Name: "Floor", Description: "", TemplateIDs: ids}, h.service.NewID)

				return err
			})

			value := []byte(tt.raw)
			if tt.edit != nil {
				value = libraryValue(t, h, tt.edit)
			}

			h.store.SetValue(NamespaceTemplates, LibraryKey(testOwner), value)

			revision := libraryRecord(t, h).Revision
			library, err := h.service.GetLibrary(ctx, testOwner)

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

			if _, err := h.service.GetLibraryByKey(ctx, OwnerScope(testOwner)); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("GetLibraryByKey = %v, want ErrCorrupt", err)
			}

			// A damaged library is not changed, and not replaced by the
			// built-in templates.
			runs := 0

			_, err = h.service.UpdateLibrary(ctx, testOwner, testOwner, func(library *TemplateLibrary) error {
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

// TestLibraryKeepsSharesAndPublication asserts the parts of the record that
// say who an item is shared with and published by are stored and read as
// they are, and are not part of an item's version.
func TestLibraryKeepsSharesAndPublication(t *testing.T) {
	h := newHarness(t)
	ids := mustAddTemplates(t, h, testOwner, testTemplate("PLC"))
	granted := time.Date(2026, time.October, 1, 12, 0, 0, 0, time.UTC)

	shares := []TemplateShare{
		{User: "bob", UserCreated: "2026-09-01T00:00:00Z", GrantedAt: granted},
		{User: "carol", UserCreated: "2026-09-02T00:00:00Z", GrantedAt: granted},
	}
	public := &TemplatePublished{At: granted, By: testOwner}

	var collection string

	shared := mustUpdate(t, h, testOwner, func(library *TemplateLibrary) error {
		template := library.Template(ids[0])
		template.Shares, template.Public = slices.Clone(shares), public

		var err error

		collection, err = library.AddCollection(CollectionContent{Name: "Floor", Description: "", TemplateIDs: ids}, h.service.NewID)
		if err != nil {
			return err
		}

		library.Collection(collection).Shares = slices.Clone(shares[:1])

		return nil
	})

	if shared.Template(ids[0]).Version != 1 {
		t.Fatalf("sharing a template changed its version: %+v", shared.Template(ids[0]))
	}

	stored := mustLibrary(t, h, testOwner)
	template := stored.Template(ids[0])

	if !slices.Equal(template.Shares, shares) || template.Public == nil || *template.Public != *public ||
		!slices.Equal(stored.Collection(collection).Shares, shares[:1]) || stored.Collection(collection).Public != nil {
		t.Fatalf("the stored library = %+v and %+v", template, stored.Collection(collection))
	}

	// Replacing the content keeps who the item is shared with.
	replaced := mustUpdate(t, h, testOwner, func(library *TemplateLibrary) error {
		if err := library.ReplaceTemplate(ids[0], testTemplate("PLC two"), nil); err != nil {
			return err
		}

		return library.ReplaceCollection(collection, CollectionContent{Name: "Floor two", Description: "", TemplateIDs: nil})
	})

	if template := replaced.Template(ids[0]); template.Version != 2 || !slices.Equal(template.Shares, shares) ||
		template.Public == nil || !slices.Equal(replaced.Collection(collection).Shares, shares[:1]) {
		t.Fatalf("after a replacement = %+v and %+v", template, replaced.Collection(collection))
	}

	// A change that would store a share list this package refuses to read
	// is refused.
	_, err := h.service.UpdateLibrary(context.Background(), testOwner, testOwner, func(library *TemplateLibrary) error {
		library.Template(ids[0]).Shares = []TemplateShare{shares[1], shares[0]}

		return nil
	})

	if reason, _ := refusal(t, err); !strings.Contains(reason, "out of order") {
		t.Fatalf("a share list out of order is refused with %q", reason)
	}
}

// TestLibrarySurvivesCleanup asserts the cleanups a server start runs leave
// the template libraries alone.
func TestLibrarySurvivesCleanup(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	ids := mustAddTemplates(t, h, testOwner, testTemplate("PLC"))
	revision := libraryRecord(t, h).Revision

	if _, err := h.service.CleanupOrphanedChunks(ctx); err != nil {
		t.Fatalf("CleanupOrphanedChunks returned error: %v", err)
	}

	if _, err := h.service.CleanupOrphanedDocuments(ctx, nil); err != nil {
		t.Fatalf("CleanupOrphanedDocuments returned error: %v", err)
	}

	if library := mustLibrary(t, h, testOwner); library.Template(ids[0]) == nil || library.Revision != revision {
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
