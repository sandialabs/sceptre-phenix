package main

import (
	"encoding/json"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

// remoteNamesModel gives the non-empty names of the objects in the list at key
// of body, keeping only the objects whose running is true when running is set.
func remoteNamesModel(body []byte, key string, running bool) []string {
	var payload map[string]json.RawMessage

	_ = json.Unmarshal(body, &payload)

	var entries []json.RawMessage

	_ = json.Unmarshal(payload[key], &entries)

	var names []string

	for _, raw := range entries {
		var entry map[string]json.RawMessage
		if json.Unmarshal(raw, &entry) != nil {
			continue
		}

		var (
			isRunning bool
			name      string
		)

		if running && (json.Unmarshal(entry["running"], &isRunning) != nil || !isRunning) {
			continue
		}

		if json.Unmarshal(entry["name"], &name) == nil && name != "" {
			names = append(names, name)
		}
	}

	return names
}

// getRemoteExperiments and getRemoteVMs never panic, fail only for a body that
// is not a JSON object, and agree with remoteNamesModel.
func FuzzRemoteNames(f *testing.F) {
	for _, seed := range []string{
		`{"experiments":[{"name":"exp1","running":true,"topology":"topo","scenario":"","vlan_count":2},` +
			`{"name":"exp2","running":false}]}`,
		`{"vms":[{"name":"vm1","host":"compute1","running":true,"networks":["EXP_1"]},{"name":"vm2"}]}`,
		`{"experiments":[{"name":"a"},{"name":"b","running":"true"},"c",1,null,[],{"running":true}]}`,
		`{"vms":["a",{"name":7},{"name":""},null]}`,
		`{"experiments":{"name":"a","running":true},"vms":"b"}`,
		`{"experiments":[{"name":"a","running":true,"running":false}]}`,
		`{"experiments":[{"Name":"a","Running":true}]}`,
		`{"experiments":[{"name":"a","running":1e999}]}`,
		`{}`, `null`, `[]`, `<html>`, ``,
	} {
		f.Add([]byte(seed))
	}

	f.Fuzz(func(t *testing.T, body []byte) {
		wantErr := json.Unmarshal(body, new(map[string]any)) != nil

		for _, c := range []struct {
			key     string
			running bool
			get     func() ([]string, error)
		}{
			{"experiments", true, func() ([]string, error) {
				return getRemoteExperiments(bodyClient(string(body)), "http://phenix/api/v1/experiments")
			}},
			{"vms", false, func() ([]string, error) {
				return getRemoteVMs(bodyClient(string(body)), "http://phenix/api/v1/experiments/a/vms")
			}},
		} {
			got, err := c.get()
			if (err != nil) != wantErr {
				t.Fatalf("names of %s in %q error = %v, want error %t", c.key, body, err, wantErr)
			}

			if err != nil {
				continue
			}

			want := remoteNamesModel(body, c.key, c.running)
			if diff := cmp.Diff(want, got, cmpopts.EquateEmpty()); diff != "" {
				t.Fatalf("names of %s in %q mismatch (-want +got):\n%s", c.key, body, diff)
			}
		}
	})
}
