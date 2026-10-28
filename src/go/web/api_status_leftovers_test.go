package web

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	"phenix/api/experiment"
	"phenix/store"
	"phenix/util/mm/mmtest"
	"phenix/web/weberror"
)

// leftoversCase is a request through the router, its status and the message of its JSON Error body.
type leftoversCase struct {
	contractErrorCase

	message string
}

// leftoversCheck sends tc as an admin to the contract fixture, and checks the status, that the body is the JSON Error
// with the code of the status and the message tc gives, and that openapi.yml documents the status.
func leftoversCheck(t *testing.T, spec openAPISpec, tc leftoversCase) {
	t.Helper()

	f := useContractFixture(t)
	server := serveContractAs(t, adminRole(), nil).URL

	path := tc.path
	if tc.prepare != nil {
		path = tc.prepare(t, f)
	}

	resp := do(t, tc.method, server+path, tc.body, tc.header...)
	if resp.StatusCode != tc.want {
		t.Fatalf("%s %s = %d, want %d: %s", tc.method, path, resp.StatusCode, tc.want, readBody(t, resp))
	}

	wantDocumentedBody(t, tc.method, path, resp)

	if ct := resp.Header.Get("Content-Type"); ct != mimeJSON {
		t.Errorf("%s %s Content-Type = %q, want %s", tc.method, path, ct, mimeJSON)
	}

	if got := errorMessage(t, tc.want, readBody(t, resp)); got != tc.message {
		t.Errorf("%s %s error message = %q, want %q", tc.method, path, got, tc.message)
	}

	if !spec.documents(tc.method, path, tc.want) {
		t.Errorf("openapi.yml documents %d for %s %s = false, want true", tc.want, tc.method, path)
	}
}

// leftoversStoreFails makes the store fail op on configs of kind with err, and returns path.
func leftoversStoreFails(path, op, kind string, err error) func(*testing.T, contractFixture) string {
	return func(t *testing.T, _ contractFixture) string {
		t.Helper()

		failTestStore(t, op, kind, err)

		return path
	}
}

// leftoversStoreFailsAfter makes the store fail op on configs of kind with err once it did op on kind n times,
// until the test ends.
func leftoversStoreFailsAfter(t *testing.T, op, kind string, n int, err error) {
	t.Helper()

	s, ok := store.DefaultStore.(*testStore)
	if !ok {
		t.Fatal("leftoversStoreFailsAfter: the store is not a testStore; call useTestStore first")
	}

	s.mu.Lock()
	// fail runs with s.mu held, which guards done
	done := 0
	s.fail = func(o, k string) error {
		if o != op || !strings.EqualFold(k, kind) {
			return nil
		}

		if done++; done > n {
			return err
		}

		return nil
	}
	s.mu.Unlock()

	t.Cleanup(func() {
		s.mu.Lock()
		s.fail = nil
		s.mu.Unlock()
	})
}

// leftoversTopology stores the topology main-topo, which upsertTestTopology updates on branch main.
func leftoversTopology(t *testing.T) {
	t.Helper()

	useTestStore(t, store.Config{
		Version: "phenix.sandia.gov/v1", Kind: "Topology", Metadata: store.ConfigMetadata{Name: "main-topo"},
		Spec: map[string]any{"nodes": []any{}},
	})
}

// leftoversNoCdromVM is a minimega that does not have test-vm: it answers each CD-ROM command so.
func leftoversNoCdromVM(cmd mmtest.Command) []*minicli.Response {
	if strings.HasPrefix(cmd.Base, "vm cdrom ") {
		return []*minicli.Response{mmtest.Error("head", "vm not found: "+contractVM)}
	}

	return nil
}

// The disk routes answer a request without a required query parameter with 400 and a message that names it, from
// the handler, rather than 404 or 405 from the router.
func TestLeftoversDiskRoutesNeedTheirQueryParameters(t *testing.T) {
	spec := loadOpenAPI(t)

	const (
		disks  = "/api/v1/disks"
		noDisk = "not a disk image phenix manages: no disk given"
		noNew  = "not a disk image phenix manages: no new image given"
		withD  = "?disk=" + contractDisk
	)

	cases := map[string]struct {
		method, path, message string
	}{
		"snapshot without disk":       {"POST", disks + "/snapshot?new=copy", noDisk},
		"snapshot without new":        {"POST", disks + "/snapshot" + withD, noNew},
		"snapshot without parameters": {"POST", disks + "/snapshot", noDisk},
		"rebase without disk":         {"POST", disks + "/rebase?backing=&unsafe=false", noDisk},
		"rebase without backing": {
			"POST", disks + "/rebase" + withD + "&unsafe=false",
			"no backing given: give an empty backing to make the image independent",
		},
		"rebase without unsafe":    {"POST", disks + "/rebase" + withD + "&backing=", `unsafe must be given: true or false`},
		"resize without disk":      {"POST", disks + "/resize?size=1G", noDisk},
		"resize without size":      {"POST", disks + "/resize" + withD, "no size given"},
		"commit without disk":      {"POST", disks + "/commit", noDisk},
		"clone without disk":       {"POST", disks + "/clone?new=copy", noDisk},
		"clone without new":        {"POST", disks + "/clone" + withD, noNew},
		"clone job without disk":   {"POST", disks + "/clone/jobs?new=copy", noDisk},
		"clone job without new":    {"POST", disks + "/clone/jobs" + withD, noNew},
		"delete without disk":      {"DELETE", disks, noDisk},
		"rename without disk":      {"POST", disks + "/rename?new=copy", noDisk},
		"rename without new":       {"POST", disks + "/rename" + withD, noNew},
		"download without disk":    {"GET", disks + "/download", noDisk},
		"checksum without disk":    {"GET", disks + "/checksum", noDisk},
		"details without disk":     {"GET", disks + "/details", noDisk},
		"delete with only a force": {"DELETE", disks + "?force=true", noDisk},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			leftoversCheck(t, spec, leftoversCase{
				contractErrorCase: contractErrorCase{method: tc.method, path: tc.path, want: http.StatusBadRequest},
				message:           tc.message,
			})
		})
	}
}

// A HEAD of a disk download without the disk answers 400 from the handler, not 404 from the router.
func TestLeftoversDiskDownloadHeadNeedsTheDisk(t *testing.T) {
	useContractFixture(t)

	resp := do(t, http.MethodHead, serveContractAs(t, adminRole(), nil).URL+"/api/v1/disks/download", "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("HEAD /api/v1/disks/download = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}

	if !loadOpenAPI(t).documents(http.MethodHead, "/api/v1/disks/download", http.StatusBadRequest) {
		t.Error("openapi.yml documents 400 for HEAD /disks/download = false, want true")
	}
}

// The leftover status fixes: a workflow config upsert that races another request (409 or 404) or whose store fails
// (500), a netflow start and a config list whose store fails (500), an unknown config kind (400), an experiment create
// whose store fails (500), a CD-ROM change or eject of a VM that minimega does not have (404), and the snapshot list of
// a VM or experiment that does not exist (404).
func TestLeftoversAPIStatus(t *testing.T) {
	spec := loadOpenAPI(t)

	const (
		upsert = "/api/v1/workflow/configs/main"
		vm     = contractExpPath + "/vms/" + contractVM
	)

	yaml := []string{"Content-Type", mimeYAML}
	diskFailed := errors.New("disk failed")

	cases := map[string]leftoversCase{
		"workflow create of a config another request created": {
			contractErrorCase: contractErrorCase{
				method: "POST", body: upsertTestTopology, header: yaml, want: http.StatusConflict,
				prepare: leftoversStoreFails(upsert, "create", "Topology", fmt.Errorf("topology: %w", store.ErrExist)),
			},
			message: "config to create (Topology/main-topo) already exists",
		},
		"workflow create when the store fails": {
			contractErrorCase: contractErrorCase{
				method: "POST", body: upsertTestTopology, header: yaml, want: http.StatusInternalServerError,
				prepare: leftoversStoreFails(upsert, "create", "Topology", diskFailed),
			},
			message: "unable to create new config Topology/main-topo",
		},
		"workflow update of a config another request deleted": {
			contractErrorCase: contractErrorCase{
				method: "POST", body: upsertTestTopology, header: yaml, want: http.StatusNotFound,
				prepare: func(t *testing.T, _ contractFixture) string {
					t.Helper()

					leftoversTopology(t)
					// the first read finds the config, and the update's own read does not
					leftoversStoreFailsAfter(t, "get", "Topology", 1, fmt.Errorf("main-topo: %w", store.ErrNotExist))

					return upsert
				},
			},
			message: "config to update (Topology/main-topo) does not exist",
		},
		"workflow update when the store fails": {
			contractErrorCase: contractErrorCase{
				method: "POST", body: upsertTestTopology, header: yaml, want: http.StatusInternalServerError,
				prepare: func(t *testing.T, f contractFixture) string {
					t.Helper()

					leftoversTopology(t)

					return leftoversStoreFails(upsert, "update", "Topology", diskFailed)(t, f)
				},
			},
			message: "unable to update config Topology/main-topo",
		},
		"netflow when the store fails": {
			contractErrorCase: contractErrorCase{
				method: "POST", want: http.StatusInternalServerError,
				prepare: leftoversStoreFails(contractExpPath+"/netflow", "get", "Experiment", diskFailed),
			},
			message: "unable to start netflow capture",
		},
		"configs of an unknown kind": {
			contractErrorCase: contractErrorCase{method: "GET", path: "/api/v1/configs?kind=bogus", want: http.StatusBadRequest},
			message:           `kind must be one of all, topology, scenario, experiment, image, user, role, not "bogus"`,
		},
		"configs when the store fails": {
			contractErrorCase: contractErrorCase{
				method: "GET", want: http.StatusInternalServerError,
				prepare: leftoversStoreFails("/api/v1/configs?kind=topology", "list", "Topology", diskFailed),
			},
			message: "unable to get configs from store",
		},
		"experiment create when the store fails": {
			contractErrorCase: contractErrorCase{
				method: "POST", body: `{"name":"fresh","topology":"` + contractTopo + `"}`, want: http.StatusInternalServerError,
				prepare: leftoversStoreFails("/api/v1/experiments", "create", "Experiment", diskFailed),
			},
			message: "unable to store experiment fresh",
		},
		"experiment create when the topology cannot be read": {
			contractErrorCase: contractErrorCase{
				method: "POST", body: `{"name":"fresh","topology":"` + contractTopo + `"}`, want: http.StatusInternalServerError,
				prepare: leftoversStoreFails("/api/v1/experiments", "get", "Topology", diskFailed),
			},
			message: "unable to store experiment fresh",
		},
		"experiment create of a missing topology": {
			contractErrorCase: contractErrorCase{
				method: "POST", path: "/api/v1/experiments", body: `{"name":"fresh","topology":"none"}`,
				want: http.StatusBadRequest,
			},
			message: "topology doesn't exist",
		},
		"disc change of a VM minimega does not have": {
			contractErrorCase: contractErrorCase{
				method: "POST", want: http.StatusNotFound,
				prepare: func(t *testing.T, _ contractFixture) string {
					t.Helper()

					useMinimega(t, leftoversNoCdromVM)

					return vm + "/cdrom?isoPath=a.iso"
				},
			},
			message: "VM " + contractVM + " not found in experiment " + contractExp,
		},
		"disc eject of a VM minimega does not have": {
			contractErrorCase: contractErrorCase{
				method: "DELETE", want: http.StatusNotFound,
				prepare: func(t *testing.T, _ contractFixture) string {
					t.Helper()

					useMinimega(t, leftoversNoCdromVM)

					return vm + "/cdrom"
				},
			},
			message: "VM " + contractVM + " not found in experiment " + contractExp,
		},
		"snapshots of a VM not in the experiment": {
			contractErrorCase: contractErrorCase{method: "GET", path: contractExpPath + "/vms/none/snapshots", want: http.StatusNotFound},
			message:           "VM none not found in experiment " + contractExp,
		},
		"snapshots of a missing experiment": {
			contractErrorCase: contractErrorCase{
				method: "GET", path: "/api/v1/experiments/" + contractOther + "/vms/" + contractVM + "/snapshots",
				want: http.StatusNotFound,
			},
			message: "experiment " + contractOther + " not found",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) { leftoversCheck(t, spec, tc) })
	}
}

// A start or a stop that fails for a missing experiment answers 404, and one for an experiment that already runs or
// does not run answers 409, for every caller of startWebError and stopExperiment, such as POST /workflow/apply.
func TestLeftoversLifecycleStatus(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		err  error
		want int
	}{
		"missing experiment": {fmt.Errorf("getting: %w", experiment.ErrExperimentNotFound), http.StatusNotFound},
		"running experiment": {fmt.Errorf("starting: %w", experiment.ErrExperimentRunning), http.StatusConflict},
		"stopped experiment": {fmt.Errorf("stopping: %w", experiment.ErrStopNotRunning), http.StatusConflict},
		"other failure":      {errors.New("minimega is down"), http.StatusBadRequest},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := startWebError(tc.err, "unable to start experiment %s", "exp").Status; got != tc.want {
				t.Errorf("startWebError(%v) status = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}

// stopExperiment, which POST /workflow/apply calls for a restart, answers 404 for a missing experiment and 409 for
// one that does not run.
//
//nolint:paralleltest // replaces the store
func TestLeftoversStopExperimentStatus(t *testing.T) {
	useTestStore(t, namedExperiment(t, "stopped", ""))

	for name, want := range map[string]int{"none": http.StatusNotFound, "stopped": http.StatusConflict} {
		t.Run(name, func(t *testing.T) {
			_, err := stopExperiment(name, false, false)

			var werr *weberror.WebError
			if !errors.As(err, &werr) {
				t.Fatalf("stopExperiment(%s) error = %v, want a *weberror.WebError", name, err)
			}

			if diff := cmp.Diff(want, werr.Status); diff != "" {
				t.Errorf("stopExperiment(%s) status mismatch (-want +got):\n%s", name, diff)
			}
		})
	}
}

// A token lifetime of more days than a [time.Duration] holds answers 400 and stores no token, rather than a token of a
// lifetime that the multiplication wrapped to.
func TestLeftoversTokenLifetimeTooLong(t *testing.T) {
	own := testRole(resourceUsers, verbPatch, "test-user")

	for _, lifetime := range []string{"106752", "213600", "106751992"} {
		t.Run(lifetime, func(t *testing.T) {
			useUsersTestStore(t)
			createUserWithToken(t, "test-user")

			body := `{"lifetime":"` + lifetime + `"}`
			req := roleRequest(http.MethodPost, "/api/v1/users/test-user/tokens", body, map[string]string{"username": "test-user"}, own)

			_, rec := serveHandler(CreateUserToken, req)

			want := authAnswer{http.StatusBadRequest, weberror.CodeInvalidRequest, "invalid token lifetime provided"}
			if diff := cmp.Diff(want, authAnswerOf(t, rec)); diff != "" {
				t.Errorf("POST /users/test-user/tokens with lifetime %s mismatch (-want +got):\n%s", lifetime, diff)
			}
		})
	}
}
