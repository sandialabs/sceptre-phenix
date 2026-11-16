package startopts_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"phenix/api/experiment/startopts"
)

// FuzzDecode checks that Decode does not panic, that it either returns
// options with a valid VLAN range or an error that wraps ErrInvalid, and that
// the options it returns encode to a body that decodes to them again.
func FuzzDecode(f *testing.F) {
	for _, seed := range []string{
		"", "{}", "null", `{"dryRun": true}`, `{"vlanMin": 100, "vlanMax": 200}`, `{"vlanMin": 300, "vlanMax": 200}`,
		`{"honorRunPeriodically": false}`, `{"x": 1}`, `{} {}`, `[`, `{"vlanMax": 4095}`,
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, body string) {
		got, err := startopts.Decode(strings.NewReader(body))
		if err != nil {
			if !errors.Is(err, startopts.ErrInvalid) {
				t.Fatalf("Decode(%q) error = %v, want one that wraps %v", body, err, startopts.ErrInvalid)
			}

			return
		}

		if !validRange(got.VLANMin, got.VLANMax) {
			t.Fatalf("Decode(%q) = %+v, which has a VLAN range that Check refuses", body, got)
		}

		again, err := json.Marshal(got)
		if err != nil {
			t.Fatalf("json.Marshal(%+v) error = %v", got, err)
		}

		if back, err := startopts.Decode(strings.NewReader(string(again))); err != nil || !equal(got, back) {
			t.Fatalf("Decode(%s) = %+v, %v; want %+v, nil", again, back, err, got)
		}
	})
}

// equal reports whether a and b are the same options.
func equal(a, b startopts.Options) bool {
	same := (a.HonorRunPeriodically == nil) == (b.HonorRunPeriodically == nil)
	if same && a.HonorRunPeriodically != nil {
		same = *a.HonorRunPeriodically == *b.HonorRunPeriodically
	}

	return same && a.DryRun == b.DryRun && a.TreatMinimegaErrorsAsWarnings == b.TreatMinimegaErrorsAsWarnings &&
		a.VLANMin == b.VLANMin && a.VLANMax == b.VLANMax
}
