package mmcli

import (
	"strings"
	"testing"

	"github.com/activeshadow/libminimega/minicli"
	"github.com/activeshadow/libminimega/miniclient"
)

// responsesOf returns a drained-once channel carrying one response per host,
// each with the given error ("" for success).
func responsesOf(errs ...string) chan *miniclient.Response {
	resps := make(minicli.Responses, 0, len(errs))

	for i, err := range errs {
		resps = append(resps, &minicli.Response{Host: "node" + string(rune('0'+i)), Error: err})
	}

	out := make(chan *miniclient.Response, 1)
	out <- &miniclient.Response{Resp: resps}

	close(out)

	return out
}

func TestVMTargetErrorResponse(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		errs    []string
		want    string // substring of the error, "" for none
		notWant string // substring the error must not have, "" for any
	}{
		"owner succeeds, others not found": {
			errs: []string{"", "vm not found: vm1", "vm not found: vm1"},
		},
		"single node succeeds": {errs: []string{""}},
		"not found anywhere": {
			errs: []string{"vm not found: vm1", "vm not found: vm1"},
			want: "vm not found: vm1",
		},
		"owner fails": {
			errs:    []string{"VM state error: vm1", "vm not found: vm1"},
			want:    "VM state error: vm1",
			notWant: "not found",
		},
		"other error beside a success": {
			errs: []string{"", "bind: address already in use"},
			want: "bind: address already in use",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := VMTargetErrorResponse(responsesOf(tc.errs...))

			switch {
			case tc.want == "" && err != nil:
				t.Errorf("VMTargetErrorResponse(%q) = %v, want nil", tc.errs, err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Errorf("VMTargetErrorResponse(%q) = %v, want one containing %q", tc.errs, err, tc.want)
			case tc.notWant != "" && strings.Contains(err.Error(), tc.notWant):
				t.Errorf("VMTargetErrorResponse(%q) = %v, want one without %q", tc.errs, err, tc.notWant)
			}
		})
	}
}
