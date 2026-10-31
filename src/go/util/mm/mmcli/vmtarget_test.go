package mmcli

import (
	"slices"
	"strings"
	"testing"

	"github.com/activeshadow/libminimega/minicli"
	"github.com/activeshadow/libminimega/miniclient"
	"pgregory.net/rapid"
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

// vmtargetBatches returns a drained-once channel carrying one response per batch, with one
// node's answer per error ("" for success).
func vmtargetBatches(batches [][]string) chan *miniclient.Response {
	out := make(chan *miniclient.Response, len(batches))

	for _, errs := range batches {
		resps := make(minicli.Responses, 0, len(errs))
		for _, err := range errs {
			resps = append(resps, &minicli.Response{Host: "node", Error: err})
		}

		out <- &miniclient.Response{Resp: resps}
	}

	close(out)

	return out
}

// However the nodes' answers are ordered and batched: any other error is reported, else a
// success hides "vm not found", which is reported only when no node succeeded.
func TestVMTargetErrorResponseProperties(t *testing.T) {
	t.Parallel()

	const notFound = vmNotFoundPrefix + "vm1"

	others := []string{"VM state error: vm1", "bind: address already in use"}
	answer := rapid.SampledFrom(append([]string{"", notFound}, others...))

	rapid.Check(t, func(t *rapid.T) {
		batches := rapid.SliceOfN(rapid.SliceOfN(answer, 0, 3), 0, 3).Draw(t, "batches")

		answered := slices.Concat(batches...)
		err := VMTargetErrorResponse(vmtargetBatches(batches))

		var wantOthers []string

		for _, other := range others {
			if slices.Contains(answered, other) {
				wantOthers = append(wantOthers, other)
			}
		}

		switch {
		case len(wantOthers) > 0:
			for _, other := range wantOthers {
				if err == nil || !strings.Contains(err.Error(), other) {
					t.Fatalf("VMTargetErrorResponse(%q) = %v, want one containing %q", batches, err, other)
				}
			}

			if strings.Contains(err.Error(), notFound) {
				t.Fatalf("VMTargetErrorResponse(%q) = %v, want one without %q", batches, err, notFound)
			}
		case slices.Contains(answered, "") || !slices.Contains(answered, notFound):
			if err != nil {
				t.Fatalf("VMTargetErrorResponse(%q) = %v, want nil", batches, err)
			}
		case err == nil || !strings.Contains(err.Error(), notFound):
			t.Fatalf("VMTargetErrorResponse(%q) = %v, want one containing %q", batches, err, notFound)
		}
	})
}
