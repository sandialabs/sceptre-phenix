package soh

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

// The hostsToUseUUIDForC2Active setting names one host, a list of hosts, or
// "all" for every host; any other value fails.
func TestSOHMetadataUUIDHosts(t *testing.T) {
	t.Parallel()

	hosts := []string{"web", "db", "win"}

	for name, tc := range map[string]struct {
		setting any
		want    []string // the hosts that use the UUID
		wantErr bool
	}{
		"not set":         {},
		"null":            {setting: nil},
		"one host":        {setting: "web", want: []string{"web"}},
		"list of hosts":   {setting: []any{"web", "win"}, want: []string{"web", "win"}},
		"empty list":      {setting: []any{}},
		"all":             {setting: "all", want: hosts},
		"all in a list":   {setting: []any{"db", "all"}, want: hosts},
		"list of numbers": {setting: []any{"web", 1}, wantErr: true},
		"number":          {setting: 1, wantErr: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			md := sohMetadata{Other: map[string]any{}}
			if name != "not set" {
				md.Other["hostsToUseUUIDForC2Active"] = tc.setting
			}

			if err := md.init(); (err != nil) != tc.wantErr {
				t.Fatalf("init(hostsToUseUUIDForC2Active %#v) error = %v, want error %t", tc.setting, err, tc.wantErr)
			}

			if tc.wantErr {
				return
			}

			var got []string

			for _, host := range hosts {
				if md.useUUIDForC2Active(host) {
					got = append(got, host)
				}
			}

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("hosts that use the UUID for hostsToUseUUIDForC2Active %#v mismatch (-want +got):\n%s", tc.setting, diff)
			}
		})
	}
}
