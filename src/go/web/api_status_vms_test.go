package web

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	"phenix/util/mm"
	"phenix/util/mm/mmtest"
)

// vmsStoppedExp is an experiment that the VM status cases store stopped, with the VM a.
const vmsStoppedExp = "stopped-experiment"

// vmsAbsentMinimega is a minimega that has no VMs: `vm info` lists none, and the
// commands that name a VM answer that it is not found.
func vmsAbsentMinimega(cmd mmtest.Command) []*minicli.Response {
	for _, verb := range []string{"vm start ", "vm stop ", "vm kill "} {
		if name, ok := strings.CutPrefix(cmd.Base, verb); ok {
			return []*minicli.Response{mmtest.Error("head", "vm not found: "+name)}
		}
	}

	return nil
}

// vmsAt returns a prepare function that stores the stopped experiment, uses a minimega that has no VMs, and requests
// path.
func vmsAt(path string) func(t *testing.T, f contractFixture) string {
	return func(t *testing.T, _ contractFixture) string {
		t.Helper()

		useTestStore(t, namedExperiment(t, vmsStoppedExp, "", diskVM("a", "a.qc2")))
		useMinimega(t, vmsAbsentMinimega)

		return path
	}
}

// vmsCapturing returns a prepare function for path whose minimega has test-vm, running if running is set, with a
// connected interface 0, a disconnected interface 1, and a capture on interface 1.
func vmsCapturing(path string, running bool) func(t *testing.T, f contractFixture) string {
	return func(t *testing.T, _ contractFixture) string {
		t.Helper()

		useTestMM(t, &testMM{
			vms:      mm.VMs{{Name: contractVM, Running: running, Networks: []string{"EXP_1 (101)", "disconnected"}}},
			captures: []mm.Capture{{VM: contractVM, Interface: 1, Filepath: "/tmp/1.pcap"}},
		})

		return path
	}
}

// vmsStatusCases are the VM routes' answers for a VM, experiment, snapshot or capture that does not exist (404), an
// experiment or VM that does not run (409), and a file name or interface that cannot be used (400).
func vmsStatusCases() map[string]contractErrorCase {
	const (
		exp     = contractExpPath
		vm      = exp + "/vms/" + contractVM
		none    = exp + "/vms/none"
		other   = "/api/v1/experiments/" + contractOther
		stopped = "/api/v1/experiments/" + vmsStoppedExp + "/vms/a"
	)

	cases := map[string]contractErrorCase{
		"get VMs of unknown experiment":   {method: "GET", prepare: vmsAt(other + "/vms"), want: http.StatusNotFound},
		"get VM of unknown experiment":    {method: "GET", prepare: vmsAt(other + "/vms/" + contractVM), want: http.StatusNotFound},
		"get unknown VM":                  {method: "GET", prepare: vmsAt(none), want: http.StatusNotFound},
		"delete unknown VM":               {method: "DELETE", prepare: vmsAt(none), want: http.StatusNotFound},
		"delete VM already deleted":       {method: "DELETE", prepare: vmsAt(vm), want: http.StatusNotFound},
		"delete VM of stopped experiment": {method: "DELETE", prepare: vmsAt(stopped), want: http.StatusConflict},
		"delete VM of unknown experiment": {
			method: "DELETE", prepare: vmsAt(other + "/vms/" + contractVM), want: http.StatusNotFound,
		},
		"stop VM minimega lacks":    {method: "POST", prepare: vmsAt(vm + "/stop"), want: http.StatusNotFound},
		"start VM minimega lacks":   {method: "POST", prepare: vmsAt(vm + "/start"), want: http.StatusNotFound},
		"restart VM minimega lacks": {method: "POST", prepare: vmsAt(vm + "/restart"), want: http.StatusNotFound},
		"start VM of stopped experiment": {
			method: "POST", prepare: vmsAt(stopped + "/start"), want: http.StatusConflict,
		},
		"update VM while running": {
			method: "PATCH", prepare: vmsAt(vm), body: `{"cpus":4}`, want: http.StatusConflict,
		},
		"update interface of VM of stopped experiment": {
			method: "PATCH", prepare: vmsAt(stopped), body: `{"interface":{"index":0,"vlan":"EXP_1"}}`,
			want: http.StatusConflict,
		},
		"update unknown VM": {method: "PATCH", prepare: vmsAt(none), body: `{"cpus":4}`, want: http.StatusNotFound},
		"update VM of unknown experiment": {
			method: "PATCH", prepare: vmsAt(other + "/vms/" + contractVM), body: `{"cpus":4}`, want: http.StatusNotFound,
		},
		"redeploy unknown VM": {method: "POST", prepare: vmsAt(none + "/redeploy"), body: `{}`, want: http.StatusNotFound},
		"redeploy VM of stopped experiment": {
			method: "POST", prepare: vmsAt(stopped + "/redeploy"), body: `{}`, want: http.StatusConflict,
		},
		"change disc of unknown VM": {
			method: "POST", prepare: vmsAt(none + "/cdrom?isoPath=a.iso"), want: http.StatusNotFound,
		},
		"eject disc of unknown VM": {method: "DELETE", prepare: vmsAt(none + "/cdrom"), want: http.StatusNotFound},
		"change disc of VM of stopped experiment": {
			method: "POST", prepare: vmsAt(stopped + "/cdrom?isoPath=a.iso"), want: http.StatusConflict,
		},
		"snapshot of unknown VM": {
			method: "POST", prepare: vmsAt(none + "/snapshots"), body: `{"filename":"s1"}`, want: http.StatusNotFound,
		},
		"snapshot of VM that is not running": {
			method: "POST", prepare: vmsAt(vm + "/snapshots"), body: `{"filename":"s1"}`, want: http.StatusConflict,
		},
		"snapshot of VM of stopped experiment": {
			method: "POST", prepare: vmsAt(stopped + "/snapshots"), body: `{"filename":"s1"}`, want: http.StatusConflict,
		},
		"restore unknown VM": {
			method: "POST", prepare: vmsAt(none + "/snapshots/s1"), want: http.StatusNotFound,
		},
		"restore missing snapshot": {
			method: "POST", want: http.StatusNotFound,
			prepare: func(t *testing.T, f contractFixture) string {
				t.Helper()

				useVMSnapshotFiles(t, nil, contractVM+"__s1")

				return vmsAt(vm+"/snapshots/"+contractVM+"__s9")(t, f)
			},
		},
		"commit of unknown VM": {
			method: "POST", prepare: vmsAt(none + "/commit"), body: `{"filename":"x.qc2"}`, want: http.StatusNotFound,
		},
		"memory snapshot of unknown VM": {
			method: "POST", prepare: vmsAt(none + "/memorySnapshot"), body: `{"filename":"m.elf"}`, want: http.StatusNotFound,
		},
		"capture without file name": {
			method: "POST", prepare: vmsCapturing(vm+"/captures", true), body: `{"interface":0}`, want: http.StatusBadRequest,
		},
		"capture of interface VM lacks": {
			method: "POST", prepare: vmsCapturing(vm+"/captures", true), body: `{"interface":99,"filename":"c.pcap"}`,
			want: http.StatusBadRequest,
		},
		"capture of disconnected interface": {
			method: "POST", prepare: vmsCapturing(vm+"/captures", true), body: `{"interface":1,"filename":"c.pcap"}`,
			want: http.StatusBadRequest,
		},
		"capture of VM that is not running": {
			method: "POST", prepare: vmsCapturing(vm+"/captures", false), body: `{"interface":0,"filename":"c.pcap"}`,
			want: http.StatusConflict,
		},
		"capture of unknown VM": {
			method: "POST", prepare: vmsCapturing(none+"/captures", true), body: `{"interface":0,"filename":"c.pcap"}`,
			want: http.StatusNotFound,
		},
		"stop captures of VM with none": {
			method: "DELETE", prepare: vmsAt(vm + "/captures"), want: http.StatusNotFound,
		},
		"stop capture of interface not captured": {
			method: "DELETE", prepare: vmsCapturing(vm+"/captures?iface=0", true), want: http.StatusNotFound,
		},
		"stop capture of negative interface": {
			method: "DELETE", prepare: vmsCapturing(vm+"/captures?iface=-1", true), want: http.StatusBadRequest,
		},
		"tunneler download past its end": {
			method: "GET", header: []string{"Range", "bytes=999999-"}, want: http.StatusRequestedRangeNotSatisfiable,
			prepare: func(t *testing.T, _ contractFixture) string {
				t.Helper()

				dir := t.TempDir()
				t.Chdir(dir)

				if err := os.MkdirAll(filepath.Join(dir, "downloads", "tunneler"), 0o700); err != nil {
					t.Fatal(err)
				}

				build := filepath.Join(dir, "downloads", "tunneler", "phenix-tunneler-linux-amd64")
				if err := os.WriteFile(build, []byte("linux"), 0o600); err != nil {
					t.Fatal(err)
				}

				return "/downloads/tunneler/phenix-tunneler-linux-amd64"
			},
		},
	}

	// a snapshot or commit file name must be a bare name that minimega takes as one argument
	for name, body := range map[string]string{
		"no file name":              `{}`,
		"file name of space":        `{"filename":"a b"}`,
		"file name in other folder": `{"filename":"../x"}`,
	} {
		cases["snapshot with "+name] = contractErrorCase{
			method: "POST", prepare: vmsAt(vm + "/snapshots"), body: body, want: http.StatusBadRequest,
		}
	}

	for name, filename := range map[string]string{"relative path": "../../tmp/x.qc2", "absolute path": "/tmp/x.qc2"} {
		cases["commit to "+name] = contractErrorCase{
			method: "POST", prepare: vmsAt(vm + "/commit"), body: `{"filename":"` + filename + `"}`,
			want: http.StatusBadRequest,
		}
	}

	return cases
}

// The VM routes answer a VM, experiment, snapshot or capture that does not exist with 404, an experiment or VM that
// does not run with 409, and a file name or interface they cannot use with 400, each as openapi.yml documents it,
// with the JSON Error body. Neither minimega nor the store changes.
//
//nolint:paralleltest // replaces the store, minimega and the working directory
func TestVMRoutesAnswerTheirStatus(t *testing.T) {
	spec := loadOpenAPI(t)

	for name, tc := range vmsStatusCases() {
		t.Run(name, func(t *testing.T) { checkContractError(t, spec, name, tc) })
	}
}

// A second DELETE of a VM finds that minimega no longer has it and answers 404 with the VM named, after a first that
// answered 204.
//
//nolint:paralleltest // replaces the store and minimega
func TestDeleteVMTwice(t *testing.T) {
	useTestStore(t, namedExperiment(t, "exp", "2024-01-01T00:00:00Z", diskVM("a", "a.qc2")))

	var killed bool

	useMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
		if cmd.Base != "vm kill a" {
			return nil
		}

		if killed {
			return []*minicli.Response{mmtest.Error("head", "vm not found: a")}
		}

		killed = true

		return nil
	})

	server := serveAs(t, "alice", testRole("vms", "delete", "exp/*")).URL

	for _, want := range []int{http.StatusNoContent, http.StatusNotFound} {
		resp := do(t, http.MethodDelete, server+"/api/v1/experiments/exp/vms/a", "")
		body := readBody(t, resp)

		if resp.StatusCode != want {
			t.Fatalf("DELETE vms/a = %d, want %d: %s", resp.StatusCode, want, body)
		}

		if want == http.StatusNotFound {
			if got, wantMsg := errorMessage(t, resp.StatusCode, body), "VM a not found in experiment exp"; got != wantMsg {
				t.Errorf("DELETE vms/a again = %q, want %q", got, wantMsg)
			}
		}
	}
}

// openapi.yml documents the data URL that GET screenshot.png answers with base64 set as text/plain, next to the PNG.
func TestScreenshotDocumentsBase64(t *testing.T) {
	t.Parallel()

	doc := loadOpenAPIDocument(t)

	item := doc.Paths.Find("/experiments/{exp}/vms/{name}/screenshot.png")
	if item == nil || item.Get == nil {
		t.Fatal("openapi.yml has no GET /experiments/{exp}/vms/{name}/screenshot.png")
	}

	ok := item.Get.Responses.Get(http.StatusOK)
	if ok == nil || ok.Value == nil {
		t.Fatal("GET screenshot.png documents no 200")
	}

	for _, contentType := range []string{"image/png", "text/plain"} {
		if ok.Value.Content.Get(contentType) == nil {
			t.Errorf("GET screenshot.png 200 documents %s = false, want true", contentType)
		}
	}
}
