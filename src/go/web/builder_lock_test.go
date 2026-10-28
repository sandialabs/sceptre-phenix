package web

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	bapi "phenix/api/builder"
	"phenix/store"
	bdoc "phenix/types/builder"
)

// builderLockPatience is how long a request that must wait for the publish
// lock is given to answer all the same.
const builderLockPatience = 75 * time.Millisecond

// builderHeldPublication is a publication whose first config write waits
// until release is called. The publication holds the publish lock then.
type builderHeldPublication struct {
	release  func()
	answered <-chan *httptest.ResponseRecorder
}

// holdBuilderPublication reroutes the harness so a publication waits at its
// first config write, starts the publication of draft with body as alice,
// and returns once the publication waits there.
func holdBuilderPublication(
	t *testing.T,
	harness *builderHarness,
	draft builderDraftResponse,
	body string,
) builderHeldPublication {
	t.Helper()

	var (
		entered = make(chan struct{})
		release = make(chan struct{})
		once    sync.Once
		wait    = func() {
			once.Do(func() { close(entered) })
			<-release
		}
	)

	harness.reroute(func(api *builderAPI) {
		create, update := api.publish.createConfig, api.publish.updateConfig

		api.publish.createConfig = func(config *store.Config) (*store.Config, error) {
			wait()

			return create(config)
		}
		api.publish.updateConfig = func(name string, config *store.Config) error {
			wait()

			return update(name, config)
		}
	})

	answered := make(chan *httptest.ResponseRecorder, 1)

	go func() {
		answered <- harness.do(builderRequest{
			method: http.MethodPost, path: "/builder/drafts/" + draft.ID + "/publish",
			body: body, user: builderAlice, ifMatch: draft.ETag,
		})
	}()

	select {
	case <-entered:
	case recorder := <-answered:
		t.Fatalf("publish %s answered %d before its config write: %s", body, recorder.Code, recorder.Body)
	case <-time.After(10 * time.Second):
		t.Fatalf("publish %s did not reach its config write", body)
	}

	var released sync.Once

	return builderHeldPublication{
		release:  func() { released.Do(func() { close(release) }) },
		answered: answered,
	}
}

// startBuilderRequest sends request in a new goroutine and returns the
// channel its answer arrives on.
func startBuilderRequest(harness *builderHarness, request builderRequest) <-chan *httptest.ResponseRecorder {
	answered := make(chan *httptest.ResponseRecorder, 1)

	go func() { answered <- harness.do(request) }()

	return answered
}

// waitedForPublication reports an error unless the request whose answer
// arrives on answered is still waiting after [builderLockPatience]. Then it
// releases the held publication and returns the publication's answer and
// the request's answer.
func waitedForPublication(
	t *testing.T,
	held builderHeldPublication,
	answered <-chan *httptest.ResponseRecorder,
	what string,
) (*httptest.ResponseRecorder, *httptest.ResponseRecorder) {
	t.Helper()

	select {
	case recorder := <-answered:
		held.release()
		t.Errorf("%s answered %d during a publication, want it to wait for the publication", what, recorder.Code)

		return <-held.answered, recorder
	case <-time.After(builderLockPatience):
	}

	held.release()

	publication := <-held.answered

	return publication, <-answered
}

// TestBuilderDeleteDraftWaitsForItsPublication asserts a delete of a draft
// that is being published waits for the publication, then fails with 412
// draft.stale, and keeps the published draft.
func TestBuilderDeleteDraftWaitsForItsPublication(t *testing.T) {
	harness := newBuilderHarness(t)
	draft := createBuilderPublishDraft(t, harness, bdoc.NewDocument("racing"))

	held := holdBuilderPublication(t, harness, draft,
		`{"mode":"topology","topology":{"name":"racing","action":"create"}}`)

	deleted := startBuilderRequest(harness, builderRequest{
		method: http.MethodDelete, path: "/builder/drafts/" + draft.ID, user: builderBob, ifMatch: draft.ETag,
	})

	publication, refused := waitedForPublication(t, held, deleted, "DELETE /builder/drafts/"+draft.ID)

	if err := builderStatusError(publication, http.StatusOK); err != nil {
		t.Fatalf("publish of draft %s during a delete: %v", draft.ID, err)
	}

	var published builderPublishResponse

	decodeBuilderAnswer(t, publication, &published)

	if published.Status != bapi.PublishSucceeded {
		t.Errorf("publish of draft %s during a delete: status = %q, want %q", draft.ID, published.Status, bapi.PublishSucceeded)
	}

	if err := builderStatusError(refused, http.StatusPreconditionFailed); err != nil {
		t.Fatalf("DELETE /builder/drafts/%s after its publication: %v", draft.ID, err)
	}

	if code := builderRefusal(t, refused).Code; code != string(bdoc.CodeDraftStale) {
		t.Errorf("DELETE /builder/drafts/%s after its publication: code = %q, want %q", draft.ID, code, bdoc.CodeDraftStale)
	}

	meta := mustDraftMeta(t, harness, draft.ID)

	if got := refused.Header().Get("ETag"); got != meta.ETag() {
		t.Errorf("DELETE /builder/drafts/%s after its publication: ETag = %s, want the published draft's %s",
			draft.ID, got, meta.ETag())
	}

	if meta.Publication == nil || meta.Publication.TopologyTarget != "racing" {
		t.Errorf("draft %s publication = %+v, want the publication of topology racing", draft.ID, meta.Publication)
	}

	// The ETag the delete got deletes the draft.
	harness.doDecode(builderRequest{
		method: http.MethodDelete, path: "/builder/drafts/" + draft.ID, user: builderBob, ifMatch: meta.ETag(),
	}, http.StatusNoContent, nil)
}

// TestBuilderCleanupWaitsForAPublication asserts a cleanup waits for a new
// publication of a published, unchanged draft, so the publication completes.
// The cleanup then deletes the draft at its new publication.
func TestBuilderCleanupWaitsForAPublication(t *testing.T) {
	harness := newBuilderHarness(t)
	draft, _ := builderPublishedDraft(t, harness, "first")

	held := holdBuilderPublication(t, harness, draft,
		`{"mode":"topology","topology":{"name":"second","action":"create"}}`)

	cleaned := startBuilderRequest(harness, builderRequest{
		method: http.MethodPost, path: builderCleanupPath, user: builderBob,
	})

	publication, cleanup := waitedForPublication(t, held, cleaned, "POST "+builderCleanupPath)

	if err := builderStatusError(publication, http.StatusOK); err != nil {
		t.Fatalf("publish of draft %s during a cleanup: %v", draft.ID, err)
	}

	var published builderPublishResponse

	decodeBuilderAnswer(t, publication, &published)

	if published.Status != bapi.PublishSucceeded {
		t.Errorf("publish of draft %s during a cleanup: status = %q, want %q", draft.ID, published.Status, bapi.PublishSucceeded)
	}

	if err := builderStatusError(cleanup, http.StatusOK); err != nil {
		t.Fatalf("POST %s after a publication: %v", builderCleanupPath, err)
	}

	var response builderCleanupResponse

	decodeBuilderAnswer(t, cleanup, &response)

	want := builderCleanupResponse{DryRun: false, Drafts: []string{draft.ID}, Documents: 0, Content: 0}
	if diff := cmp.Diff(want, response); diff != "" {
		t.Errorf("POST %s after a publication mismatch (-want +got):\n%s", builderCleanupPath, diff)
	}

	if slices.Contains(harness.store.Keys(bapi.NamespaceDrafts), draft.ID) {
		t.Errorf("draft %s is stored after the cleanup, want it deleted", draft.ID)
	}
}
