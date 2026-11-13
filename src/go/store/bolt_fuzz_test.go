package store

import (
	"errors"
	"fmt"
	"maps"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"go.etcd.io/bbolt"

	"phenix/types/version"
)

// boltRawPath reports whether p reads back unchanged from "bolt://" + p: an
// absolute path with no URL escape, query, fragment or control byte, or a plain relative one.
func boltRawPath(p string) bool {
	if strings.HasPrefix(p, "/") {
		return !strings.ContainsAny(p, "%?#") && !strings.ContainsFunc(p, func(r rune) bool { return r < 0x20 || r == 0x7f })
	}

	return !strings.ContainsFunc(p, func(r rune) bool {
		return !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._~-/", r)
	})
}

// BoltPath refuses every endpoint without the bolt scheme, and gives back the
// path of a bolt:// endpoint, written raw or escaped as a URL.
func FuzzBoltPath(f *testing.F) {
	for _, seed := range []string{
		"bolt:///etc/phenix/store.bdb", "bolt://store.bdb", "etcd://localhost:2379", "/etc/phenix/store.bdb",
		"foo://localhost", "/tmp/a b/50%.bdb", "/tmp/a#b?c", "//x", "dir/store.bdb", "../store.bdb", "BOLT://x", "",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, s string) {
		if _, err := BoltPath(s); err == nil && (len(s) < len("bolt:") || !strings.EqualFold(s[:len("bolt:")], "bolt:")) {
			t.Fatalf("BoltPath(%q) error = nil, want one for an endpoint without the bolt scheme", s)
		}

		if boltRawPath(s) {
			if got, err := BoltPath("bolt://" + s); err != nil || got != s {
				t.Fatalf("BoltPath(%q) = %q, %v; want %q, nil", "bolt://"+s, got, err, s)
			}
		}

		if strings.HasPrefix(s, "/") {
			escaped := (&url.URL{Scheme: "bolt", Path: s}).String()
			if got, err := BoltPath(escaped); err != nil || got != s {
				t.Fatalf("BoltPath(%q) = %q, %v; want %q, nil", escaped, got, err, s)
			}
		}
	})
}

const (
	boltFuzzCreate = iota
	boltFuzzUpdate
	boltFuzzDelete
	boltFuzzGet
	boltFuzzList
	boltFuzzOps
)

const (
	boltFuzzNames  = 3
	boltFuzzMaxOps = 16
)

// boltFuzzOp encodes one store operation as the two script bytes FuzzBoltStore reads.
func boltFuzzOp(op, name, kind int, ifRevision byte) []byte {
	return []byte{byte(name*boltFuzzOps + op), byte(kind<<4) | ifRevision&0x0f}
}

// A BoltDB agrees with boltFuzzModel over any script of creates, updates,
// deletes, gets and lists: each write takes the next revision, IfRevision
// guards updates and deletes, and a refused write changes nothing.
func FuzzBoltStore(f *testing.F) {
	const a, b = 0, 1

	f.Add(slices.Concat( // TestBoltReadsAndWrites
		boltFuzzOp(boltFuzzGet, a, 0, 0), boltFuzzOp(boltFuzzUpdate, a, 0, 0), boltFuzzOp(boltFuzzDelete, a, 0, 0),
		boltFuzzOp(boltFuzzCreate, a, 0, 0), boltFuzzOp(boltFuzzCreate, b, 0, 0), boltFuzzOp(boltFuzzCreate, a, 0, 0),
		boltFuzzOp(boltFuzzUpdate, a, 0, 0), boltFuzzOp(boltFuzzList, a, 0, 0), boltFuzzOp(boltFuzzDelete, b, 0, 0),
		boltFuzzOp(boltFuzzGet, b, 0, 0),
	), "a", "b")
	f.Add(slices.Concat( // TestBoltRevisionsIncreaseAcrossConfigs
		boltFuzzOp(boltFuzzCreate, a, 0, 0), boltFuzzOp(boltFuzzUpdate, a, 0, 0), boltFuzzOp(boltFuzzCreate, b, 1, 0),
		boltFuzzOp(boltFuzzGet, a, 0, 0), boltFuzzOp(boltFuzzList, a, 1, 0),
	), "a", "b")
	f.Add(slices.Concat( // TestBoltIfRevision
		boltFuzzOp(boltFuzzCreate, a, 0, 0), boltFuzzOp(boltFuzzUpdate, a, 0, 0), boltFuzzOp(boltFuzzUpdate, a, 0, 1),
		boltFuzzOp(boltFuzzDelete, a, 0, 1), boltFuzzOp(boltFuzzUpdate, a, 0, 2), boltFuzzOp(boltFuzzDelete, a, 0, 3),
		boltFuzzOp(boltFuzzGet, a, 0, 0), boltFuzzOp(boltFuzzCreate, a, 0, 9),
	), "a", "b")
	f.Add(slices.Concat(
		boltFuzzOp(boltFuzzCreate, a, 2, 0), boltFuzzOp(boltFuzzGet, a, 2, 0), boltFuzzOp(boltFuzzCreate, b, 2, 0),
	), "", "x/y")

	kinds := slices.Sorted(maps.Keys(version.StoredVersion))

	f.Fuzz(func(t *testing.T, script []byte, nameA, nameB string) {
		// a stored name that is not UTF-8 reads back with U+FFFD in its place
		if !utf8.ValidString(nameA) || !utf8.ValidString(nameB) {
			t.Skip("names must be UTF-8")
		}

		// a fuzz input's temp dir holds "#", so the endpoint escapes it
		path := filepath.Join(t.TempDir(), "store.bdb")
		db := new(BoltDB)

		if err := db.Init(Endpoint((&url.URL{Scheme: "bolt", Path: path}).String())); err != nil {
			t.Fatalf("BoltDB.Init(%s) error = %v", path, err)
		}

		names := [boltFuzzNames]string{nameA, nameB, "a"}
		model := &boltFuzzModel{revisions: map[string]map[string]uint64{}}

		for i := 0; i+1 < len(script) && i < 2*boltFuzzMaxOps; i += 2 {
			op, name := int(script[i])%boltFuzzOps, names[int(script[i])/boltFuzzOps%boltFuzzNames]
			kind, ifRevision := kinds[int(script[i+1]>>4)%len(kinds)], uint64(script[i+1]&0x0f)
			model.step(t, db, op, kind, name, ifRevision)
		}

		configs, err := db.List(kinds...)
		if err != nil {
			t.Fatalf("List(every kind) error = %v", err)
		}

		want := make([]string, 0, len(configs))
		for _, kind := range kinds {
			want = append(want, model.listed(kind)...)
		}

		if diff := cmp.Diff(want, listedRevisions(configs), cmpopts.EquateEmpty()); diff != "" {
			t.Fatalf("List(every kind) at the end mismatch (-model +store):\n%s", diff)
		}
	})
}

// boltFuzzModel is the store FuzzBoltStore expects: the revision of each
// config by kind and name, and the last revision given.
type boltFuzzModel struct {
	revisions map[string]map[string]uint64
	last      uint64
}

func (m *boltFuzzModel) listed(kind string) []string {
	names := slices.Sorted(maps.Keys(m.revisions[kind]))
	out := make([]string, 0, len(names))

	for _, name := range names {
		out = append(out, fmt.Sprintf("%s/%s@%d", kind, name, m.revisions[kind][name]))
	}

	return out
}

// write gives kind/name the next revision and returns it.
func (m *boltFuzzModel) write(kind, name string) uint64 {
	m.last++

	if m.revisions[kind] == nil {
		m.revisions[kind] = map[string]uint64{}
	}

	m.revisions[kind][name] = m.last

	return m.last
}

// guard returns the error an update or delete of kind/name at ifRevision must return, or nil.
func (m *boltFuzzModel) guard(kind, name string, ifRevision uint64) error {
	stored, ok := m.revisions[kind][name]

	switch {
	case !ok:
		return ErrNotExist
	case ifRevision != 0 && ifRevision != stored:
		return ErrConflict
	default:
		return nil
	}
}

// step runs one operation on db and on m, and fails t when they disagree.
func (m *boltFuzzModel) step(t *testing.T, db *BoltDB, op int, kind, name string, ifRevision uint64) {
	t.Helper()

	c := &Config{Kind: kind, Metadata: ConfigMetadata{Name: name}, IfRevision: ifRevision}
	before := c.Metadata
	call := fmt.Sprintf("%s/%q (IfRevision %d)", kind, name, ifRevision)

	var wrote bool

	switch op {
	case boltFuzzCreate:
		wrote = m.create(t, db, c, call)
	case boltFuzzUpdate:
		wrote = m.update(t, db, c, call)
	case boltFuzzDelete:
		m.delete(t, db, c, call)
	case boltFuzzGet:
		m.get(t, db, c, call)

		return
	default:
		configs, err := db.List(kind)
		if err != nil {
			t.Fatalf("List(%s) error = %v", kind, err)
		}

		if diff := cmp.Diff(m.listed(kind), listedRevisions(configs), cmpopts.EquateEmpty()); diff != "" {
			t.Fatalf("List(%s) mismatch (-model +store):\n%s", kind, diff)
		}

		return
	}

	// every refused write and every delete leaves the caller's metadata as it was
	if diff := cmp.Diff(before, c.Metadata); !wrote && diff != "" {
		t.Fatalf("operation %d on %s changed the metadata (-before +after):\n%s", op, call, diff)
	}
}

func (m *boltFuzzModel) create(t *testing.T, db *BoltDB, c *Config, call string) bool {
	t.Helper()

	kind, name := c.Kind, c.Metadata.Name
	_, exists := m.revisions[kind][name]
	err := db.Create(c)

	switch {
	case exists:
		if !errors.Is(err, ErrExist) {
			t.Fatalf("Create(%s) of an existing name error = %v, want %v", call, err, ErrExist)
		}

		return false
	case name == "" || len(name) > bbolt.MaxKeySize:
		if err == nil || errors.Is(err, ErrExist) {
			t.Fatalf("Create(%s) of a name Bolt cannot store error = %v, want a write error", call, err)
		}

		return false
	case err != nil:
		t.Fatalf("Create(%s) error = %v", call, err)
	}

	if rev := m.write(kind, name); c.Metadata.Revision != rev || c.Metadata.Created == "" || c.Metadata.Updated == "" {
		t.Fatalf("Create(%s) metadata = %+v, want revision %d and times set", call, c.Metadata, rev)
	}

	return true
}

func (m *boltFuzzModel) update(t *testing.T, db *BoltDB, c *Config, call string) bool {
	t.Helper()

	kind, name := c.Kind, c.Metadata.Name
	want := m.guard(kind, name, c.IfRevision)
	err := db.Update(c)

	if want != nil {
		if !errors.Is(err, want) {
			t.Fatalf("Update(%s) error = %v, want %v", call, err, want)
		}

		return false
	}

	if err != nil {
		t.Fatalf("Update(%s) error = %v", call, err)
	}

	if rev := m.write(kind, name); c.Metadata.Revision != rev || c.Metadata.Updated == "" {
		t.Fatalf("Update(%s) metadata = %+v, want revision %d and updated set", call, c.Metadata, rev)
	}

	return true
}

func (m *boltFuzzModel) delete(t *testing.T, db *BoltDB, c *Config, call string) {
	t.Helper()

	want := m.guard(c.Kind, c.Metadata.Name, c.IfRevision)
	if err := db.Delete(c); !errors.Is(err, want) {
		t.Fatalf("Delete(%s) error = %v, want %v", call, err, want)
	}

	if want == nil {
		delete(m.revisions[c.Kind], c.Metadata.Name)
	}
}

func (m *boltFuzzModel) get(t *testing.T, db *BoltDB, c *Config, call string) {
	t.Helper()

	kind, name := c.Kind, c.Metadata.Name
	stored, exists := m.revisions[kind][name]
	err := db.Get(c)

	switch {
	case !exists:
		if !errors.Is(err, ErrNotExist) {
			t.Fatalf("Get(%s) of a missing name error = %v, want %v", call, err, ErrNotExist)
		}
	case err != nil:
		t.Fatalf("Get(%s) error = %v", call, err)
	case c.Kind != kind || c.Metadata.Name != name || c.Metadata.Revision != stored:
		t.Fatalf("Get(%s) = %s/%q@%d, want revision %d", call, c.Kind, c.Metadata.Name, c.Metadata.Revision, stored)
	}
}
