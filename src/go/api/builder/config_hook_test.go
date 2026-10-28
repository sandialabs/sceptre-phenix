package builder

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"phenix/api/config"
	"phenix/store"
	"phenix/store/recordtest/memrecord"
	"phenix/store/storetest"
	"phenix/types"
)

// hookTestStore keeps configs in a BoltDB and records in memory. beforeList,
// when set, runs once before the next record listing.
type hookTestStore struct {
	*memrecord.Store
	hookTestConfigs

	beforeList func()
}

// hookTestConfigs gives hookTestStore its config methods. Embedded a level
// deeper than the record store, it never has its record methods promoted.
type hookTestConfigs struct{ store.Store }

func (s *hookTestStore) ListRecords(namespace, prefix string) (store.Records, error) {
	if run := s.beforeList; run != nil {
		s.beforeList = nil
		run()
	}

	return s.Store.ListRecords(namespace, prefix)
}

// hookTest stores documents and configs in a hookTestStore that is the phenix
// store for the test, which the Topology config hook reads.
type hookTest struct {
	t       *testing.T
	store   *hookTestStore
	service *Service
	// now is when documents are stored: the service's clock and the time
	// records are stamped with.
	now time.Time
}

func newHookTest(t *testing.T) *hookTest {
	t.Helper()

	h := &hookTest{t: t, now: time.Now()}
	h.store = &hookTestStore{Store: memrecord.New(), hookTestConfigs: hookTestConfigs{Store: storetest.NewBolt(t)}}
	h.store.Stamp = func() time.Time { return h.now }

	service, err := New(WithStore(h.store), WithClock(func() time.Time { return h.now }))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	h.service = service

	storetest.Use(t, h.store)

	return h
}

// publish stores a published document of target holding content, dated
// ago before now.
func (h *hookTest) publish(target, content string, ago time.Duration) *PublishedDocument {
	h.t.Helper()

	return h.publishAt(target, content, time.Now().Add(-ago))
}

// publishAt stores a published document of target holding content, dated at.
func (h *hookTest) publishAt(target, content string, at time.Time) *PublishedDocument {
	h.t.Helper()

	h.now = at

	doc, err := h.service.PutPublishedDocument(context.Background(), PutPublishedDocumentRequest{
		Target: target, Kind: configKindTopology, Actor: testActor,
		Document: testRandomDocument(h.t, content, 600),
	})
	if err != nil {
		h.t.Fatalf("PutPublishedDocument(%q) returned error: %s", target, fmtErr(err))
	}

	return doc
}

// writeTopology stores the topology name referencing doc, as a publication
// does after storing doc. The store dates it now.
func (h *hookTest) writeTopology(name string, doc *PublishedDocument) error {
	h.t.Helper()

	return h.writeReference(name, doc.Reference())
}

// writeReference stores the topology name with the document reference ref,
// past the config hook. The store dates it now.
func (h *hookTest) writeReference(name string, ref DocumentReference) error {
	h.t.Helper()

	return store.Create(h.topology(name, encodeTestReference(h.t, ref))) //nolint:wrapcheck // the store's own error
}

// topology returns a topology config name whose document annotation holds
// value.
func (h *hookTest) topology(name, value string) *store.Config {
	h.t.Helper()

	c, err := store.NewConfig("Topology/" + name)
	if err != nil {
		h.t.Fatalf("NewConfig returned error: %v", err)
	}

	c.Metadata.Annotations = store.Annotations{DocumentAnnotation: value, "kept": "yes"}
	c.Spec = map[string]any{"nodes": []any{}}

	return c
}

// encodeTestReference returns ref as a topology's annotation holds it.
func encodeTestReference(t *testing.T, ref DocumentReference) string {
	t.Helper()

	encoded, err := ref.EncodeReference()
	if err != nil {
		t.Fatalf("EncodeReference returned error: %v", err)
	}

	return encoded
}

// topologyUpdated returns the start of the second the topology name was last
// written in, as its metadata says.
func (h *hookTest) topologyUpdated(name string) time.Time {
	h.t.Helper()

	c, err := config.Get("topology/" + name)
	if err != nil {
		h.t.Fatalf("getting topology %s returned error: %v", name, err)
	}

	updated := configUpdated(c)
	if updated.IsZero() {
		h.t.Fatalf("topology %s has no updated time: %q", name, c.Metadata.Updated)
	}

	return updated
}

// checkGone returns an error naming every doc that, or whose content, is
// not gone.
func (h *hookTest) checkGone(docs ...*PublishedDocument) error {
	var problems []error

	for _, doc := range docs {
		if _, err := h.service.GetPublishedDocument(context.Background(), doc.ID); !errors.Is(err, ErrNotFound) {
			problems = append(problems, fmt.Errorf("GetPublishedDocument(%s) error = %s, want ErrNotFound", doc.ID, fmtErr(err)))
		}

		if keys := contentKeysOf(&testHarness{store: h.store.Store}, publishedKey(doc.ID)); len(keys) != 0 {
			problems = append(problems, fmt.Errorf("content of document %s = %v, want none", doc.ID, keys))
		}
	}

	return errors.Join(problems...)
}

// checkReadable returns an error naming every doc that cannot be read back
// whole.
func (h *hookTest) checkReadable(docs ...*PublishedDocument) error {
	var problems []error

	for _, doc := range docs {
		if _, _, err := h.service.GetPublishedDocumentData(context.Background(), doc.ID); err != nil {
			problems = append(
				problems,
				fmt.Errorf("GetPublishedDocumentData(%s) returned error: %s, want it readable", doc.ID, fmtErr(err)),
			)
		}
	}

	return errors.Join(problems...)
}

// TestDeletingTopologyDeletesItsDocuments deletes a topology: its documents go,
// however recent, and another topology's stay.
func TestDeletingTopologyDeletesItsDocuments(t *testing.T) {
	h := newHookTest(t)

	old := h.publish("topo", "topo-v1", 2*OrphanGracePeriod)
	superseded := h.publish("topo", "topo-v2", time.Minute)
	current := h.publish("topo", "topo-v3", time.Minute)
	other := h.publish("other", "other-v1", time.Minute)

	for name, doc := range map[string]*PublishedDocument{"topo": current, "other": other} {
		if err := h.writeTopology(name, doc); err != nil {
			t.Fatalf("storing topology %s returned error: %v", name, err)
		}
	}

	if err := config.Delete("topology/topo"); err != nil {
		t.Fatalf("config.Delete(topology/topo) returned error: %v", err)
	}

	if err := h.checkGone(old, superseded, current); err != nil {
		t.Errorf("after config.Delete(topology/topo):\n%v", err)
	}

	if err := h.checkReadable(other); err != nil {
		t.Errorf("after config.Delete(topology/topo):\n%v", err)
	}

	if err := config.Delete("all"); err != nil {
		t.Fatalf("config.Delete(all) returned error: %v", err)
	}

	if err := h.checkGone(other); err != nil {
		t.Errorf("after config.Delete(all):\n%v", err)
	}
}

// TestDeletingTopologyKeepsPublicationInFlight deletes a topology while a
// publication stores its document: that document stays until the grace period.
func TestDeletingTopologyKeepsPublicationInFlight(t *testing.T) {
	for _, test := range []struct {
		name    string
		content string
		// sameSecond stores the document at the end of the second the
		// topology was last written in, not a minute later.
		sameSecond bool
	}{
		{name: "new content", content: "topo-v2"},
		{name: "the content of the deleted topology", content: "topo-v1"},
		{name: "new content in the second of the last write", content: "topo-v2", sameSecond: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHookTest(t)

			deleted := h.publish("topo", "topo-v1", time.Minute)
			if err := h.writeTopology("topo", deleted); err != nil {
				t.Fatalf("storing the topology returned error: %v", err)
			}

			stored := time.Now().Add(time.Minute)
			if test.sameSecond {
				stored = h.topologyUpdated("topo").Add(time.Second - time.Nanosecond)
			}

			var inFlight *PublishedDocument

			h.store.beforeList = func() {
				inFlight = h.publishAt("topo", test.content, stored)
			}

			if err := config.Delete("topology/topo"); err != nil {
				t.Fatalf("config.Delete(topology/topo) returned error: %v", err)
			}

			if inFlight == nil {
				t.Fatal("config.Delete(topology/topo) did not list the documents")
			}

			if inFlight.ID != deleted.ID {
				if err := h.checkGone(deleted); err != nil {
					t.Errorf("after config.Delete(topology/topo):\n%v", err)
				}
			}

			if err := h.checkReadable(inFlight); err != nil {
				t.Errorf("after config.Delete(topology/topo):\n%v", err)
			}

			if err := h.writeTopology("topo", inFlight); err != nil {
				t.Fatalf("storing the topology again for the publication returned error: %v", err)
			}

			if err := h.checkReadable(inFlight); err != nil {
				t.Errorf("after storing the topology again:\n%v", err)
			}

			// Had the publication failed, nothing would reference the document.
			if err := config.Delete("topology/topo"); err != nil {
				t.Fatalf("second config.Delete(topology/topo) returned error: %v", err)
			}

			if test.sameSecond {
				// Stored no later than the second the topology that named it
				// was written in, it went with that topology.
				if err := h.checkGone(inFlight); err != nil {
					t.Errorf("after the second config.Delete(topology/topo):\n%v", err)
				}

				return
			}

			h.now = time.Now().Add(2 * OrphanGracePeriod)

			removed, err := h.service.CleanupOrphanedDocuments(t.Context(), nil, false)
			if err != nil {
				t.Fatalf("CleanupOrphanedDocuments returned error: %s", fmtErr(err))
			}

			if removed != 1 {
				t.Errorf("CleanupOrphanedDocuments = %d, want 1 for the in-flight document", removed)
			}

			if err := h.checkGone(inFlight); err != nil {
				t.Errorf("after CleanupOrphanedDocuments:\n%v", err)
			}
		})
	}
}

// TestRenamingTopologyDeletesItsDocuments renames a published topology: its
// documents go and its reference is dropped. Keeping the name keeps both.
func TestRenamingTopologyDeletesItsDocuments(t *testing.T) {
	h := newHookTest(t)

	// update stores the topology name as the topology as.
	update := func(name, as string) {
		t.Helper()

		c, err := config.Get("topology/" + name)
		if err != nil {
			t.Fatalf("getting topology %s returned error: %v", name, err)
		}

		c.Metadata.Name = as

		if err := config.Update("topology/"+name, c); err != nil {
			t.Fatalf("updating topology %s as %s returned error: %v", name, as, err)
		}
	}

	old := h.publish("topo", "topo-v1", 2*OrphanGracePeriod)
	superseded := h.publish("topo", "topo-v2", time.Minute)
	current := h.publish("topo", "topo-v3", time.Minute)
	other := h.publish("other", "other-v1", time.Minute)

	for name, doc := range map[string]*PublishedDocument{"topo": current, "other": other} {
		if err := h.writeTopology(name, doc); err != nil {
			t.Fatalf("storing topology %s returned error: %v", name, err)
		}
	}

	update("other", "other")

	if kept, err := config.Get("topology/other"); err != nil {
		t.Errorf("config.Get(topology/other) after an update that keeps the name returned error: %v", err)
	} else if !kept.HasAnnotation(DocumentAnnotation) {
		t.Errorf("config.Get(topology/other) after an update that keeps the name annotations = %v, want the document reference kept",
			kept.Metadata.Annotations)
	}

	update("topo", "renamed")

	if _, err := config.Get("topology/topo"); !errors.Is(err, store.ErrNotExist) {
		t.Errorf("config.Get(topology/topo) after the rename error = %v, want store.ErrNotExist", err)
	}

	renamed, err := config.Get("topology/renamed")
	if err != nil {
		t.Fatalf("getting the renamed topology returned error: %v", err)
	}

	if renamed.HasAnnotation(DocumentAnnotation) {
		t.Errorf("config.Get(topology/renamed) document annotation = %q, want none", renamed.Metadata.Annotations[DocumentAnnotation])
	}

	if err := h.checkGone(old, superseded, current); err != nil {
		t.Errorf("after renaming topology topo:\n%v", err)
	}

	if err := h.checkReadable(other); err != nil {
		t.Errorf("after renaming topology topo:\n%v", err)
	}
}

// TestCreatingTopologyDropsAnotherTopologysDocument stores a copy of a published
// topology under a new name: the copy names no document.
func TestCreatingTopologyDropsAnotherTopologysDocument(t *testing.T) {
	h := newHookTest(t)

	doc := h.publish("topo", "topo-v1", time.Minute)
	reference := encodeTestReference(t, doc.Reference())

	for name, named := range map[string]bool{"topo": true, "copy": false} {
		t.Run(name, func(t *testing.T) {
			stored, err := config.Create(config.CreateFromConfig(h.topology(name, reference)), config.CreateWithValidation())
			if err != nil {
				t.Fatalf("config.Create(topology %s) returned error: %v", name, err)
			}

			want := store.Annotations{"kept": "yes"}
			if named {
				want[DocumentAnnotation] = reference
			}

			if diff := cmp.Diff(want, stored.Metadata.Annotations); diff != "" {
				t.Errorf("config.Create(topology %s) annotations mismatch (-want +got):\n%s", name, diff)
			}
		})
	}

	if err := h.checkReadable(doc); err != nil {
		t.Error(err)
	}
}

// TestStoringTopologyChecksItsReference stores each kind of document reference:
// another topology's ID is dropped, and a reference that does not decode is refused
// (TestDecodeReferenceIsStrict has every kind of refusal).
func TestStoringTopologyChecksItsReference(t *testing.T) {
	h := newHookTest(t)

	const file = "/phenix/topologies/site/builder.yaml"

	own := h.publish("topo", "topo-v1", time.Minute)
	other := h.publish("other", "other-v1", time.Minute)
	encode := func(ref DocumentReference) string { return encodeTestReference(t, ref) }

	for i, test := range []struct {
		name  string
		value string
		// want is the annotation stored, "" when it is removed.
		want string
		// refused is whether the write is refused.
		refused bool
	}{
		{
			name:  "digest only",
			value: encode(DocumentReference{Digest: own.Digest}),
			want:  encode(DocumentReference{Digest: own.Digest}),
		},
		{
			name:  "its own digest and id",
			value: encode(own.Reference()),
			want:  encode(own.Reference()),
		},
		{
			name:  "an id alone, which only the store can check",
			value: encode(DocumentReference{ID: other.ID}),
			want:  encode(DocumentReference{ID: other.ID}),
		},
		{
			name:  "path only",
			value: encode(DocumentReference{Path: file}),
			want:  encode(DocumentReference{Path: file}),
		},
		{
			name:  "its own digest and id with a path",
			value: encode(DocumentReference{Digest: own.Digest, ID: own.ID, Path: file}),
			want:  encode(DocumentReference{Digest: own.Digest, ID: own.ID, Path: file}),
		},
		{
			name:  "the digest and id of another topology",
			value: encode(other.Reference()),
			want:  "",
		},
		{
			name:  "the digest and id of another topology with a path",
			value: encode(DocumentReference{Digest: other.Digest, ID: other.ID, Path: file}),
			want:  encode(DocumentReference{Digest: other.Digest, Path: file}),
		},
		{
			name:  "its own digest with another id",
			value: encode(DocumentReference{Digest: own.Digest, ID: other.ID}),
			want:  "",
		},
		{
			name:  "written by hand",
			value: ` { "path" : "` + file + `", "id": "` + own.ID + `" ,"digest":"` + own.Digest + `" } `,
			want:  encode(DocumentReference{Digest: own.Digest, ID: own.ID, Path: file}),
		},
		{name: "not json", value: "{", refused: true},
		{name: "an unknown sub-key", value: `{"id":"` + own.ID + `","draftId":"draft-1"}`, refused: true},
	} {
		// check returns how the topology a write stored, or the write's error,
		// differs from what the test wants.
		check := func(stage string, stored *store.Config, err error) error {
			if test.refused {
				var problems []error

				if !errors.Is(err, types.ErrValidationFailed) {
					problems = append(problems, fmt.Errorf("%s error = %w, want types.ErrValidationFailed", stage, err))
				}

				if _, getErr := config.Get("topology/topo"); stage == "create" && !errors.Is(getErr, store.ErrNotExist) {
					problems = append(
						problems,
						fmt.Errorf("config.Get(topology/topo) after a refused %s error = %w, want store.ErrNotExist", stage, getErr),
					)
				}

				return errors.Join(problems...)
			}

			if err != nil {
				return fmt.Errorf("%s returned error: %w", stage, err)
			}

			want := store.Annotations{"kept": "yes"}
			if test.want != "" {
				want[DocumentAnnotation] = test.want
			}

			if diff := cmp.Diff(want, stored.Metadata.Annotations); diff != "" {
				return fmt.Errorf("%s stored annotations mismatch (-want +got):\n%s", stage, diff)
			}

			return nil
		}

		for _, validate := range []bool{true, false} {
			// Without validation only a refusal differs, so one accepted case is enough.
			if !validate && !test.refused && i > 0 {
				continue
			}

			t.Run(fmt.Sprintf("%s/create validated %t", test.name, validate), func(t *testing.T) {
				options := []config.CreateOption{config.CreateFromConfig(h.topology("topo", test.value))}
				if validate {
					options = append(options, config.CreateWithValidation())
				}

				_, err := config.Create(options...)

				stored, getErr := config.Get("topology/topo")
				if err == nil && getErr != nil {
					t.Fatalf("config.Get(topology/topo) after create returned error: %v", getErr)
				}

				if problem := check("create", stored, err); problem != nil {
					t.Errorf("config.Create(topology/topo with %s):\n%v", test.name, problem)
				}

				if !validate && test.refused && !errors.Is(err, ErrInvalid) {
					t.Errorf("config.Create(topology/topo with %s) error = %v, want the hook's refusal ErrInvalid", test.name, err)
				}

				_ = store.Delete(h.topology("topo", ""))
			})
		}

		t.Run(test.name+"/update", func(t *testing.T) {
			if err := h.writeReference("topo", own.Reference()); err != nil {
				t.Fatalf("storing the topology returned error: %v", err)
			}

			defer func() { _ = store.Delete(h.topology("topo", "")) }()

			err := config.Update("topology/topo", h.topology("topo", test.value))

			stored, getErr := config.Get("topology/topo")
			if getErr != nil {
				t.Fatalf("config.Get(topology/topo) after update returned error: %v", getErr)
			}

			if problem := check("update", stored, err); problem != nil {
				t.Errorf("config.Update(topology/topo with %s):\n%v", test.name, problem)
			}

			if want := encode(own.Reference()); test.refused && stored.Metadata.Annotations[DocumentAnnotation] != want {
				t.Errorf("refused config.Update(topology/topo with %s) stored the reference %q, want %q kept",
					test.name, stored.Metadata.Annotations[DocumentAnnotation], want)
			}
		})
	}

	if err := h.checkReadable(own, other); err != nil {
		t.Error(err)
	}
}

// TestRenamingTopologyKeepsItsFile renames topologies that name a Builder file:
// the path and digest stay, the ID goes.
func TestRenamingTopologyKeepsItsFile(t *testing.T) {
	h := newHookTest(t)

	const file = "/phenix/topologies/site/builder.yaml"

	doc := h.publish("pinned", "pinned-v1", time.Minute)

	for name, test := range map[string]struct{ ref, want DocumentReference }{
		"floating": {
			ref:  DocumentReference{Path: file},
			want: DocumentReference{Path: file},
		},
		"pinned": {
			ref:  DocumentReference{Digest: doc.Digest, ID: doc.ID, Path: file},
			want: DocumentReference{Digest: doc.Digest, Path: file},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := h.writeReference(name, test.ref); err != nil {
				t.Fatalf("storing topology %s returned error: %v", name, err)
			}

			c, err := config.Get("topology/" + name)
			if err != nil {
				t.Fatalf("config.Get(topology/%s) returned error: %v", name, err)
			}

			c.Metadata.Name = name + "-renamed"

			if err := config.Update("topology/"+name, c); err != nil {
				t.Fatalf("config.Update(topology/%s) renaming it returned error: %v", name, err)
			}

			renamed, err := config.Get("topology/" + name + "-renamed")
			if err != nil {
				t.Fatalf("config.Get(topology/%s-renamed) returned error: %v", name, err)
			}

			if got, want := renamed.Metadata.Annotations[DocumentAnnotation], encodeTestReference(t, test.want); got != want {
				t.Errorf("renamed topology %s document annotation = %q, want %q", name, got, want)
			}
		})
	}

	// The document was published to the old name, which no topology has now.
	if err := h.checkGone(doc); err != nil {
		t.Errorf("after the renames:\n%v", err)
	}
}

// TestDeletingTopologyNeverTouchesItsFile deletes topologies that name a digest,
// with or without a file: the documents go, and the file is not read or removed.
func TestDeletingTopologyNeverTouchesItsFile(t *testing.T) {
	h := newHookTest(t)

	file := filepath.Join(t.TempDir(), "builder.yaml")
	content := []byte("not a builder document\n")

	//nolint:gosec // a file the test reads back
	if err := os.WriteFile(file, content, 0o600); err != nil {
		t.Fatalf("writing the builder file: %v", err)
	}

	byDigest := h.publishAt("by-digest", "by-digest-v1", time.Now())
	if err := h.writeReference("by-digest", DocumentReference{Digest: byDigest.Digest}); err != nil {
		t.Fatalf("storing the topology returned error: %v", err)
	}

	withFile := h.publishAt("with-file", "with-file-v1", time.Now())
	if err := h.writeReference("with-file", DocumentReference{Digest: withFile.Digest, Path: file}); err != nil {
		t.Fatalf("storing the topology returned error: %v", err)
	}

	if err := h.writeReference("file-only", DocumentReference{Path: file}); err != nil {
		t.Fatalf("storing the topology returned error: %v", err)
	}

	for _, name := range []string{"by-digest", "with-file", "file-only"} {
		if err := config.Delete("topology/" + name); err != nil {
			t.Fatalf("config.Delete(topology/%s) returned error: %v", name, err)
		}
	}

	if err := h.checkGone(byDigest, withFile); err != nil {
		t.Errorf("after the deletes:\n%v", err)
	}

	got, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("os.ReadFile(builder file) after the deletes returned error: %v", err)
	}

	if !bytes.Equal(got, content) {
		t.Errorf("os.ReadFile(builder file) after the deletes = %q, want %q untouched", got, content)
	}
}

// TestLeaveTopologyDocuments asserts the hook leaves the documents of a
// topology to a caller that asked for them, for as long as it asked.
func TestLeaveTopologyDocuments(t *testing.T) {
	h := newHookTest(t)

	docs := map[string]*PublishedDocument{}

	for _, name := range []string{"left", "other", "later"} {
		docs[name] = h.publish(name, name+"-v1", time.Minute)
		if err := h.writeTopology(name, docs[name]); err != nil {
			t.Fatalf("storing topology %s returned error: %v", name, err)
		}
	}

	restore := LeaveTopologyDocuments("left")
	again := LeaveTopologyDocuments("left")

	again()
	again()

	for _, name := range []string{"left", "other"} {
		if err := config.Delete("topology/" + name); err != nil {
			t.Fatalf("config.Delete(topology/%s) returned error: %v", name, err)
		}
	}

	if err := h.checkReadable(docs["left"]); err != nil {
		t.Errorf("after deleting a left topology:\n%v", err)
	}

	if err := h.checkGone(docs["other"]); err != nil {
		t.Errorf("after deleting a topology that is not left:\n%v", err)
	}

	restore()

	if err := h.writeTopology("left", docs["left"]); err != nil {
		t.Fatalf("storing the topology again returned error: %v", err)
	}

	for _, name := range []string{"left", "later"} {
		if err := config.Delete("topology/" + name); err != nil {
			t.Fatalf("config.Delete(topology/%s) returned error: %v", name, err)
		}
	}

	if err := h.checkGone(docs["left"], docs["later"]); err != nil {
		t.Errorf("after restore:\n%v", err)
	}
}

// TestDeletingTopologyWhenDocumentsCannotBeDeleted asserts a failure to
// remove the documents does not fail deleting the topology.
func TestDeletingTopologyWhenDocumentsCannotBeDeleted(t *testing.T) {
	h := newHookTest(t)

	doc := h.publish("topo", "topo-v1", time.Minute)
	if err := h.writeTopology("topo", doc); err != nil {
		t.Fatalf("storing the topology returned error: %v", err)
	}

	h.store.FailDelete = func(string, string) error { return errors.New("injected delete failure") }

	if err := config.Delete("topology/topo"); err != nil {
		t.Errorf("config.Delete(topology/topo) returned error: %v", err)
	}

	if _, err := config.Get("topology/topo"); !errors.Is(err, store.ErrNotExist) {
		t.Errorf("config.Get(topology/topo) after the delete error = %v, want store.ErrNotExist", err)
	}

	if err := h.checkReadable(doc); err != nil {
		t.Error(err)
	}
}

// TestDeleteConfigDocuments removes the documents of one config stored before
// it was last written or past the grace period, and nothing else.
func TestDeleteConfigDocuments(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	old := publishTestDocument(t, h, "topo", testRandomDocument(t, "topo-v1", 1200))

	h.passOrphanGracePeriod()

	superseded := publishTestDocument(t, h, "topo", testRandomDocument(t, "topo-v2", 1200))
	current := publishTestDocument(t, h, "topo", testRandomDocument(t, "topo-v3", 1200))
	written := memrecord.Time(h.now.Add(1))
	inFlight := publishTestDocument(t, h, "topo", testRandomDocument(t, "topo-v4", 1200))
	other := publishTestDocument(t, h, "other", testRandomDocument(t, "other-v1", 1200))

	experiment, err := h.service.PutPublishedDocument(ctx, PutPublishedDocumentRequest{
		Target: "topo", Kind: "Experiment", Actor: testActor,
		Document: testRandomDocument(t, "experiment", 1200),
	})
	if err != nil {
		t.Fatalf("PutPublishedDocument returned error: %s", fmtErr(err))
	}

	deleted := DeletedConfig{Kind: configKindTopology, Name: "topo", DocumentID: current.ID, Updated: written}

	removed, err := h.service.DeleteConfigDocuments(ctx, DeletedConfig{
		Kind: "topology", Name: "topo", DocumentID: current.ID, Updated: time.Time{},
	})
	if err != nil {
		t.Errorf("DeleteConfigDocuments(no written time) returned error: %s", fmtErr(err))
	} else if removed != 1 {
		t.Errorf("DeleteConfigDocuments(no written time) = %d, want 1 for the old document", removed)
	}

	removed, err = h.service.DeleteConfigDocuments(ctx, deleted)
	if err != nil {
		t.Errorf("DeleteConfigDocuments(%+v) returned error: %s", deleted, fmtErr(err))
	} else if removed != 2 {
		t.Errorf("DeleteConfigDocuments(%+v) = %d, want 2", deleted, removed)
	}

	for _, doc := range []*PublishedDocument{old, superseded, current} {
		if _, err := h.service.GetPublishedDocument(ctx, doc.ID); !errors.Is(err, ErrNotFound) {
			t.Errorf("GetPublishedDocument(%s) error = %s, want ErrNotFound", doc.ID, fmtErr(err))
		}
	}

	for _, doc := range []*PublishedDocument{inFlight, other, experiment} {
		if _, _, err := h.service.GetPublishedDocumentData(ctx, doc.ID); err != nil {
			t.Errorf("GetPublishedDocumentData(%s) returned error: %s, want the document kept", doc.ID, fmtErr(err))
		}
	}

	h.passOrphanGracePeriod()

	if removed, err := h.service.DeleteConfigDocuments(ctx, deleted); err != nil {
		t.Errorf("DeleteConfigDocuments(%+v) past the grace period returned error: %s", deleted, fmtErr(err))
	} else if removed != 1 {
		t.Errorf("DeleteConfigDocuments(%+v) past the grace period = %d, want 1", deleted, removed)
	}

	for _, test := range []struct {
		name    string
		invalid DeletedConfig
	}{
		{name: "no name", invalid: DeletedConfig{Kind: "Topology", Name: "", DocumentID: "", Updated: written}},
		{name: "unknown kind", invalid: DeletedConfig{Kind: "Unknown", Name: "topo", DocumentID: "", Updated: written}},
		{name: "malformed document id", invalid: DeletedConfig{Kind: "Topology", Name: "topo", DocumentID: "not an id", Updated: written}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := h.service.DeleteConfigDocuments(ctx, test.invalid); !errors.Is(err, ErrInvalid) {
				t.Errorf("DeleteConfigDocuments(%+v) error = %s, want ErrInvalid", test.invalid, fmtErr(err))
			}
		})
	}
}

// TestDeleteConfigDocumentsInTheSecondOfTheLastWrite sorts the documents stored
// in the second of a config's last write into older ones and ones in flight.
func TestDeleteConfigDocumentsInTheSecondOfTheLastWrite(t *testing.T) {
	const topo = "topo"

	type stored struct {
		content string
		// at is when the document is stored, from the start of the second
		// the config was last written in.
		at time.Duration
		// again, when not zero, is when the same content is published again.
		again time.Duration
		// named is the document the config named.
		named bool
		kept  bool
	}

	for _, test := range []struct {
		name string
		docs []stored
	}{
		{
			name: "the document of the config and those before it go",
			docs: []stored{
				{content: "earlier", at: -time.Second},
				{content: "superseded", at: 100 * time.Millisecond},
				{content: "current", at: 200 * time.Millisecond, named: true},
				{content: "in flight", at: 300 * time.Millisecond, kept: true},
				{content: "in flight later", at: time.Minute, kept: true},
			},
		},
		{
			name: "the document of the config stored the second before",
			docs: []stored{
				{content: "superseded", at: -300 * time.Millisecond},
				{content: "current", at: -200 * time.Millisecond, named: true},
				{content: "in flight", at: 0, kept: true},
			},
		},
		{
			name: "the document of the config published again in a later second",
			docs: []stored{
				{content: "earlier", at: -time.Second},
				{content: "superseded", at: 100 * time.Millisecond, kept: true},
				{content: "current", at: 200 * time.Millisecond, again: time.Second, named: true, kept: true},
			},
		},
		{
			name: "a config that named no document",
			docs: []stored{
				{content: "earlier", at: -time.Nanosecond},
				{content: "in the second", at: 0, kept: true},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			ctx := context.Background()
			// Later than the service's clock gets, which dates a document too.
			updated := memrecord.Time(h.now.Load()).Add(OrphanGracePeriod / 2)
			deleted := DeletedConfig{Kind: configKindTopology, Name: topo, DocumentID: "", Updated: updated}
			docs := make([]*PublishedDocument, 0, len(test.docs))
			contents := make([][]byte, 0, len(test.docs))

			publish := func(content []byte, at time.Duration) *PublishedDocument {
				h.store.Stamp = func() time.Time { return updated.Add(at) }

				return publishTestDocument(t, h, topo, content)
			}

			for _, doc := range test.docs {
				contents = append(contents, testRandomDocument(t, doc.content, 600))
				docs = append(docs, publish(contents[len(contents)-1], doc.at))

				if doc.named {
					deleted.DocumentID = docs[len(docs)-1].ID
				}
			}

			for i, doc := range test.docs {
				if doc.again != 0 {
					docs[i] = publish(contents[i], doc.again)
				}
			}

			want := 0

			for _, doc := range test.docs {
				if !doc.kept {
					want++
				}
			}

			removed, err := h.service.DeleteConfigDocuments(ctx, deleted)
			if err != nil {
				t.Fatalf("DeleteConfigDocuments(%+v) returned error: %s", deleted, fmtErr(err))
			}

			if removed != want {
				t.Errorf("DeleteConfigDocuments(%+v) = %d, want %d", deleted, removed, want)
			}

			for i, doc := range test.docs {
				_, _, err := h.service.GetPublishedDocumentData(ctx, docs[i].ID)
				if doc.kept && err != nil {
					t.Errorf("GetPublishedDocumentData(document %q) returned error: %s, want it kept", doc.content, fmtErr(err))
				}

				if !doc.kept && !errors.Is(err, ErrNotFound) {
					t.Errorf("GetPublishedDocumentData(document %q) error = %s, want ErrNotFound", doc.content, fmtErr(err))
				}
			}
		})
	}
}

func TestConfigUpdated(t *testing.T) {
	t.Parallel()

	second := time.Date(2026, time.September, 30, 12, 0, 5, 0, time.UTC)

	for _, test := range []struct {
		name    string
		updated string
		want    time.Time
	}{
		{name: "UTC", updated: "2026-09-30T12:00:05Z", want: second},
		{name: "offset", updated: "2026-09-30T14:00:05+02:00", want: second},
		{name: "fraction of a second", updated: "2026-09-30T12:00:05.25Z", want: second},
		{name: "no updated time", updated: "", want: time.Time{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := configUpdated(&store.Config{Metadata: store.ConfigMetadata{Updated: test.updated}})
			if !got.Equal(test.want) {
				t.Errorf("configUpdated(%q) = %s, want %s", test.updated, got, test.want)
			}
		})
	}
}
