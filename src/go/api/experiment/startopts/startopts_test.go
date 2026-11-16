package startopts_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

	"phenix/api/experiment/startopts"
	"phenix/api/vlan"
)

// ptr returns a pointer to v.
func ptr[T any](v T) *T { return &v }

func TestDecode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		body    string
		want    startopts.Options
		wantErr error
	}{
		{name: "no body", body: ""},
		{name: "white space", body: " \n"},
		{name: "null", body: "null"},
		{name: "no options", body: "{}"},
		{
			name: "every option",
			body: `{"dryRun": true, "treatMinimegaErrorsAsWarnings": true, "honorRunPeriodically": false,
				"vlanMin": 100, "vlanMax": 200}`,
			want: startopts.Options{
				DryRun: true, TreatMinimegaErrorsAsWarnings: true, HonorRunPeriodically: ptr(false), VLANMin: 100, VLANMax: 200,
			},
		},
		{name: "one end of the VLAN range", body: `{"vlanMax": 4094}`, want: startopts.Options{VLANMax: 4094}},
		{name: "periodic runs", body: `{"honorRunPeriodically": true}`, want: startopts.Options{HonorRunPeriodically: ptr(true)}},
		{name: "an unknown field", body: `{"dry_run": true}`, wantErr: startopts.ErrInvalid},
		{name: "a value of the wrong type", body: `{"dryRun": "yes"}`, wantErr: startopts.ErrInvalid},
		{name: "a VLAN ID that is not a whole number", body: `{"vlanMin": 1.5}`, wantErr: startopts.ErrInvalid},
		{name: "not an object", body: `[]`, wantErr: startopts.ErrInvalid},
		{name: "a second value", body: `{} {}`, wantErr: startopts.ErrInvalid},
		{name: "a broken object", body: `{"dryRun": true`, wantErr: startopts.ErrInvalid},
		{name: "a VLAN ID of 0 and below", body: `{"vlanMin": -1}`, wantErr: startopts.ErrInvalid},
		{name: "a VLAN ID above 4094", body: `{"vlanMax": 4095}`, wantErr: startopts.ErrInvalid},
		{name: "a range highest first", body: `{"vlanMin": 300, "vlanMax": 200}`, wantErr: startopts.ErrInvalid},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := startopts.Decode(strings.NewReader(tt.body))
			if !errors.Is(err, tt.wantErr) || (tt.wantErr == nil && err != nil) {
				t.Fatalf("Decode(%q) error = %v, want %v", tt.body, err, tt.wantErr)
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("Decode(%q) mismatch (-want +got):\n%s", tt.body, diff)
			}
		})
	}
}

func TestPeriodic(t *testing.T) {
	t.Parallel()

	tests := []struct {
		set               *bool
		byDefault, wanted bool
	}{
		{set: nil, byDefault: true, wanted: true},
		{set: nil, byDefault: false, wanted: false},
		{set: ptr(false), byDefault: true, wanted: false},
		{set: ptr(true), byDefault: false, wanted: true},
	}

	for _, tt := range tests {
		o := startopts.Options{HonorRunPeriodically: tt.set}
		if got := o.Periodic(tt.byDefault); got != tt.wanted {
			t.Errorf("Options{HonorRunPeriodically: %v}.Periodic(%t) = %t, want %t", tt.set, tt.byDefault, got, tt.wanted)
		}
	}
}

func TestFields(t *testing.T) {
	t.Parallel()

	got := startopts.Options{
		DryRun: true, TreatMinimegaErrorsAsWarnings: true, HonorRunPeriodically: ptr(false), VLANMin: 1, VLANMax: 2,
	}.Fields()
	want := []any{
		"phenix.start.dry_run", true, "phenix.start.mm_errors_as_warnings", true,
		"phenix.start.honor_run_periodically", false, "phenix.start.vlan_min", 1, "phenix.start.vlan_max", 2,
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Fields() mismatch (-want +got):\n%s", diff)
	}

	if got := (startopts.Options{}).Fields(); len(got) != 0 {
		t.Errorf("Options{}.Fields() = %v, want none", got)
	}
}

// validRange reports whether the CLI and the API take the VLAN range: each end 0 or a VLAN ID, lowest first.
func validRange(minID, maxID int) bool {
	inRange := func(id int) bool { return id == 0 || (id >= vlan.MinID && id <= vlan.MaxID) }

	return inRange(minID) && inRange(maxID) && (minID == 0 || maxID == 0 || minID <= maxID)
}

// Any options encode to a body that decodes to the same options when the VLAN range is valid, and to an error that
// wraps ErrInvalid otherwise; Check agrees with Decode.
func TestDecodeRoundTrip(t *testing.T) {
	t.Parallel()

	id := rapid.OneOf(rapid.IntRange(-10, 4105), rapid.SampledFrom([]int{0, 1, 4094, 4095}))

	rapid.Check(t, func(t *rapid.T) {
		want := startopts.Options{
			DryRun:                        rapid.Bool().Draw(t, "dryRun"),
			TreatMinimegaErrorsAsWarnings: rapid.Bool().Draw(t, "warnings"),
			HonorRunPeriodically:          rapid.Ptr(rapid.Bool(), true).Draw(t, "periodic"),
			VLANMin:                       id.Draw(t, "vlanMin"),
			VLANMax:                       id.Draw(t, "vlanMax"),
		}

		body, err := json.Marshal(want)
		if err != nil {
			t.Fatalf("json.Marshal(%+v) error = %v", want, err)
		}

		got, err := startopts.Decode(strings.NewReader(string(body)))

		valid := validRange(want.VLANMin, want.VLANMax)
		if checked := want.Check(); (checked == nil) != valid {
			t.Fatalf("Check(%+v) = %v, want valid %t", want, checked, valid)
		}

		if !valid {
			if !errors.Is(err, startopts.ErrInvalid) {
				t.Fatalf("Decode(%s) error = %v, want %v", body, err, startopts.ErrInvalid)
			}

			return
		}

		if err != nil {
			t.Fatalf("Decode(%s) error = %v", body, err)
		}

		if diff := cmp.Diff(want, got); diff != "" {
			t.Fatalf("Decode(%s) mismatch (-want +got):\n%s", body, diff)
		}
	})
}

// A body with a field that Options does not have, in any letter case, is refused.
func TestDecodeRefusesUnknownFields(t *testing.T) {
	t.Parallel()

	known := map[string]bool{
		"dryRun": true, "treatMinimegaErrorsAsWarnings": true, "honorRunPeriodically": true, "vlanMin": true, "vlanMax": true,
	}

	rapid.Check(t, func(t *rapid.T) {
		field := rapid.StringMatching(`[A-Za-z_]{1,24}`).Draw(t, "field")
		if isFoldOfKnown(field, known) {
			t.Skip("a known field")
		}

		body := fmt.Sprintf(`{%q: 1}`, field)

		if _, err := startopts.Decode(strings.NewReader(body)); !errors.Is(err, startopts.ErrInvalid) {
			t.Fatalf("Decode(%s) error = %v, want %v", body, err, startopts.ErrInvalid)
		}
	})
}

// isFoldOfKnown reports whether field matches a known field in another letter case, which encoding/json takes.
func isFoldOfKnown(field string, known map[string]bool) bool {
	for name := range known {
		if strings.EqualFold(field, name) {
			return true
		}
	}

	return false
}
