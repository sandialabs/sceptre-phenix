package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	"phenix/util/mm/mmtest"
)

// Changing a VM's optical disc answers 400 for a missing image path or one
// minimega cannot take as one argument, without sending minimega anything, and
// 500 when minimega cannot change the disc.
//
//nolint:paralleltest // the fake minimega is shared by the process
func TestChangeOpticalDiscStatus(t *testing.T) {
	tests := []struct {
		name, isoPath string
		wantStatus    int
		wantSent      int // the minimega commands sent
	}{
		{name: "changes the disc", isoPath: "/phenix/images/tools.iso", wantStatus: http.StatusNoContent, wantSent: 1},
		{name: "rejects a path minimega would split", isoPath: "/phenix/images/my tools.iso", wantStatus: http.StatusBadRequest},
		{name: "rejects a path with a comma", isoPath: "/phenix/images/a,b.iso", wantStatus: http.StatusBadRequest},
		{name: "rejects a missing path", isoPath: "", wantStatus: http.StatusBadRequest},
		{
			name: "reports a disc minimega cannot change", isoPath: "/phenix/images/missing.iso",
			wantStatus: http.StatusInternalServerError, wantSent: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			useVMExperiment(t, true, "a")

			received := mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
				if strings.Contains(cmd.Base, "missing.iso") {
					return []*minicli.Response{{Host: "head", Error: "no such file"}}
				}

				return nil
			})

			server := serveAs(t, "alice", testRole("vms/cdrom", "update", "exp/a")).URL +
				"/api/v1/experiments/exp/vms/a/cdrom?isoPath=" + url.QueryEscape(test.isoPath)

			resp := do(t, http.MethodPost, server, "")
			if body := readBody(t, resp); resp.StatusCode != test.wantStatus {
				t.Errorf("POST cdrom?isoPath=%q = %d, want %d: %s", test.isoPath, resp.StatusCode, test.wantStatus, body)
			}

			if sent := mmtest.Bases(received()); len(sent) != test.wantSent {
				t.Errorf("POST cdrom?isoPath=%q sent minimega %q, want %d commands", test.isoPath, sent, test.wantSent)
			}
		})
	}
}
