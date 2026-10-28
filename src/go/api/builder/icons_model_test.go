package builder

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

	"phenix/types/builder"
)

// iconView is what the state machine compares of an icon.
type iconView struct {
	Name      string
	Owner     string
	UpdatedBy string
	ID        string
	Aliases   []string
	ETag      string
}

func viewOf(icon *LibraryIcon) iconView {
	return iconView{
		Name: icon.Name, Owner: icon.Owner, UpdatedBy: icon.UpdatedBy,
		ID: icon.ID, Aliases: icon.Aliases, ETag: icon.ETag(),
	}
}

// storedName is what a record of the icon library holds: an icon of a name,
// or an alias of a name with the key it points at.
type storedName struct {
	Kind, Name, Target string
}

// iconMachine runs steps against a Service and an in-test model of the icon library.
// The model keeps icons by serial and maps each lower-case name or alias to one icon.
type iconMachine struct {
	h      *testHarness
	images [][]byte
	// bases are the names the steps use, in lower case. names are the bases
	// in three cases each, so steps often meet a name taken ignoring case.
	bases []string
	names *rapid.Generator[string]
	users *rapid.Generator[string]

	icons map[int]*iconView
	// byName maps each name and alias, in lower case, to its icon.
	byName map[string]int
	// tags are the entity tags the service gave, in order.
	tags []string
	next int
}

func newIconMachine(h *testHarness, images [][]byte) *iconMachine {
	bases := []string{"plc", "hmi", "rtu", "relay"}
	names := make([]string, 0, 3*len(bases))

	for _, base := range bases {
		names = append(names, base, strings.ToUpper(base), strings.ToUpper(base[:1])+base[1:])
	}

	return &iconMachine{
		h: h, images: images, bases: bases,
		names: rapid.SampledFrom(names), users: rapid.SampledFrom([]string{testActor, testPeer}),
		icons: map[int]*iconView{}, byName: map[string]int{}, tags: nil, next: 0,
	}
}

// lookup returns the icon a name names, ignoring case.
func (m *iconMachine) lookup(name string) (int, *iconView) {
	serial, ok := m.byName[strings.ToLower(name)]
	if !ok {
		return 0, nil
	}

	return serial, m.icons[serial]
}

// take records a tag the service gave to a change. A revision is never given
// twice, so the tag must be new.
func (m *iconMachine) take(t *rapid.T, call, tag string) {
	t.Helper()

	if slices.Contains(m.tags, tag) {
		t.Fatalf("%s gave the tag %s, which an earlier change had, want a new one", call, tag)
	}

	m.tags = append(m.tags, tag)
}

// add adds an icon under a name, which may be taken.
func (m *iconMachine) add(t *rapid.T) {
	name, owner := m.names.Draw(t, "name"), m.users.Draw(t, "owner")
	image := m.images[rapid.IntRange(0, len(m.images)-1).Draw(t, "image")]
	id := builder.IconID(image)

	icon, created, err := m.h.service.AddIcon(context.Background(), owner, name, image)

	_, existing := m.lookup(name)

	switch {
	case existing != nil && existing.ID != id:
		want := IconNameTakenError{Name: name, Icon: existing.Name, Owner: existing.Owner}

		var taken *IconNameTakenError
		if !errors.As(err, &taken) {
			t.Fatalf("AddIcon(%q) of another image error = %v, want an IconNameTakenError", name, err)
		}

		if diff := cmp.Diff(want, *taken); diff != "" {
			t.Fatalf("AddIcon(%q) of another image refusal mismatch (-want +got):\n%s", name, diff)
		}
	case existing != nil:
		if err != nil || created {
			t.Fatalf("AddIcon(%q) of the same image = created %t, error %v, want the icon as it is", name, created, err)
		}

		if diff := iconDiff(*existing, viewOf(icon)); diff != "" {
			t.Fatalf("AddIcon(%q) of the same image mismatch (-want +got):\n%s", name, diff)
		}
	default:
		if err != nil || !created {
			t.Fatalf("AddIcon(%q) = created %t, error %v, want a new icon", name, created, err)
		}

		m.take(t, "AddIcon("+name+")", icon.ETag())

		want := iconView{Name: name, Owner: owner, UpdatedBy: owner, ID: id, Aliases: nil, ETag: icon.ETag()}
		if diff := iconDiff(want, viewOf(icon)); diff != "" {
			t.Fatalf("AddIcon(%q) mismatch (-want +got):\n%s", name, diff)
		}

		m.next++
		m.icons[m.next] = &want
		m.byName[strings.ToLower(name)] = m.next
	}
}

// rename renames the icon a name names: in place, to a new name, back to an
// old name, or onto a name that another icon holds.
func (m *iconMachine) rename(t *rapid.T) {
	from, to, caller := m.names.Draw(t, "from"), m.names.Draw(t, "to"), m.users.Draw(t, "caller")

	icon, err := m.h.service.RenameIcon(context.Background(), caller, from, to)

	serial, current := m.lookup(from)
	if current == nil {
		if !errors.Is(err, ErrNotFound) || icon != nil {
			t.Fatalf("RenameIcon(%q, %q) of no icon = %v, %v, want ErrNotFound", from, to, icon, err)
		}

		return
	}

	other, taken := m.lookup(to)

	switch {
	case to == current.Name:
		if err != nil {
			t.Fatalf("RenameIcon(%q, %q) to its own name returned error: %v", from, to, err)
		}
	case taken != nil && other != serial:
		want := IconNameTakenError{Name: to, Icon: taken.Name, Owner: taken.Owner}

		var refusal *IconNameTakenError
		if !errors.As(err, &refusal) {
			t.Fatalf("RenameIcon(%q, %q) onto another icon error = %v, want an IconNameTakenError", from, to, err)
		}

		if diff := cmp.Diff(want, *refusal); diff != "" {
			t.Fatalf("RenameIcon(%q, %q) onto another icon refusal mismatch (-want +got):\n%s", from, to, diff)
		}

		return
	default:
		if err != nil {
			t.Fatalf("RenameIcon(%q, %q) returned error: %v", from, to, err)
		}

		// A rename to another name keeps the old one as an alias, and takes
		// back the new one when it is an alias of this icon.
		if !strings.EqualFold(to, current.Name) {
			current.Aliases = append(slices.DeleteFunc(slices.Clone(current.Aliases), func(alias string) bool {
				return strings.EqualFold(alias, to)
			}), current.Name)
			m.byName[strings.ToLower(to)] = serial
		}

		current.Name, current.UpdatedBy, current.ETag = to, caller, icon.ETag()
		m.take(t, "RenameIcon("+from+", "+to+")", icon.ETag())
	}

	if diff := iconDiff(*current, viewOf(icon)); diff != "" {
		t.Fatalf("RenameIcon(%q, %q) mismatch (-want +got):\n%s", from, to, diff)
	}
}

// remove deletes the icon a name names, with its current tag or with any tag
// the service gave, which is mostly a stale one.
func (m *iconMachine) remove(t *rapid.T) {
	name, caller := m.names.Draw(t, "name"), m.users.Draw(t, "caller")
	serial, current := m.lookup(name)

	tag := RevisionETag(0)
	if current != nil && rapid.Bool().Draw(t, "current tag") {
		tag = current.ETag
	} else if len(m.tags) > 0 {
		tag = rapid.SampledFrom(m.tags).Draw(t, "tag")
	}

	err := m.h.service.DeleteIcon(context.Background(), caller, name, tag)

	switch {
	case current == nil:
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("DeleteIcon(%q, %s) of no icon = %v, want ErrNotFound", name, tag, err)
		}
	case tag != current.ETag:
		want := IconStaleError{Name: current.Name, ETag: current.ETag}

		var stale *IconStaleError
		if !errors.As(err, &stale) || !errors.Is(err, ErrConflict) {
			t.Fatalf("DeleteIcon(%q, %s) = %v, want an IconStaleError that matches ErrConflict", name, tag, err)
		}

		if diff := cmp.Diff(want, *stale); diff != "" {
			t.Fatalf("DeleteIcon(%q, %s) refusal mismatch (-want +got):\n%s", name, tag, diff)
		}
	default:
		if err != nil {
			t.Fatalf("DeleteIcon(%q, %s) returned error: %v", name, tag, err)
		}

		delete(m.icons, serial)
		maps.DeleteFunc(m.byName, func(_ string, holder int) bool { return holder == serial })
	}
}

// get compares what GetIcon returns for a name with the model.
func (m *iconMachine) get(t *rapid.T, name string) {
	t.Helper()

	icon, err := m.h.service.GetIcon(context.Background(), name)

	_, want := m.lookup(name)
	if want == nil {
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("GetIcon(%q) = %v, %v, want ErrNotFound", name, icon, err)
		}

		return
	}

	if err != nil {
		t.Fatalf("GetIcon(%q) returned error: %v", name, err)
	}

	if diff := iconDiff(*want, viewOf(icon)); diff != "" {
		t.Fatalf("GetIcon(%q) mismatch (-want +got):\n%s", name, diff)
	}
}

// check compares the listed icons, the names, and the stored records with
// the model.
func (m *iconMachine) check(t *rapid.T) {
	icons, err := m.h.service.ListIcons(context.Background())
	if err != nil {
		t.Fatalf("ListIcons returned error: %v", err)
	}

	got := make([]iconView, 0, len(icons))
	for i := range icons {
		got = append(got, viewOf(&icons[i]))
	}

	want := make([]iconView, 0, len(m.icons))
	for _, icon := range m.icons {
		want = append(want, *icon)
	}

	slices.SortFunc(want, func(a, b iconView) int {
		if order := strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); order != 0 {
			return order
		}

		return strings.Compare(a.Name, b.Name)
	})

	if diff := iconDiff(want, got); diff != "" {
		t.Fatalf("ListIcons mismatch (-want +got):\n%s", diff)
	}

	// Each name in lower case names its icon or nothing. Each current and
	// old name in upper case names its icon.
	for _, name := range m.bases {
		m.get(t, name)
	}

	for _, icon := range m.icons {
		for _, name := range append([]string{icon.Name}, icon.Aliases...) {
			m.get(t, strings.ToUpper(name))
		}
	}

	if diff := cmp.Diff(m.records(), storedNames(t, m.h)); diff != "" {
		t.Fatalf("icon records mismatch (-want +got):\n%s", diff)
	}
}

// records returns the records the model holds, by key.
func (m *iconMachine) records() map[string]storedName {
	records := make(map[string]storedName, len(m.byName))

	for _, icon := range m.icons {
		target := strings.ToLower(icon.Name)
		records[iconKey(icon.Name)] = storedName{Kind: iconRecordKind, Name: icon.Name, Target: ""}

		for _, alias := range icon.Aliases {
			records[iconKey(alias)] = storedName{Kind: aliasRecordKind, Name: alias, Target: target}
		}
	}

	return records
}

// storedNames returns what the records of the icon library hold, by key.
func storedNames(t *rapid.T, h *testHarness) map[string]storedName {
	t.Helper()

	records, err := h.store.ListRecords(NamespaceIcons, iconKeyPrefix)
	if err != nil {
		t.Fatalf("ListRecords(%s) returned error: %v", NamespaceIcons, err)
	}

	stored := make(map[string]storedName, len(records))

	for _, record := range records {
		icon, alias, err := readIconRecord(record)

		switch {
		case err != nil:
			t.Fatalf("readIconRecord(%s) returned error: %v", record.Key, err)
		case icon != nil:
			stored[record.Key] = storedName{Kind: icon.Kind, Name: icon.Name, Target: ""}
		default:
			stored[record.Key] = storedName{Kind: alias.Kind, Name: alias.Name, Target: alias.Target}
		}
	}

	return stored
}

// TestIconLibraryStateMachine runs random adds, renames, deletes and lookups
// against the Service. After each step it compares the icons, each current
// and old name in two cases, and the stored records with iconMachine.
func TestIconLibraryStateMachine(t *testing.T) {
	images := [][]byte{iconPixel(t, 1), iconPixel(t, 2), iconPixel(t, 3)}

	rapid.Check(t, func(rt *rapid.T) {
		m := newIconMachine(newHarness(t), images)

		rt.Repeat(map[string]func(*rapid.T){
			"add":    m.add,
			"rename": m.rename,
			"delete": m.remove,
			"get":    func(rt *rapid.T) { m.get(rt, m.names.Draw(rt, "name")) },
			"":       m.check,
		})
	})
}
