package scorchmd

import (
	"encoding/json"
	"testing"

	"phenix/store"
	"phenix/types"
	v1 "phenix/types/version/v1"
	v2 "phenix/types/version/v2"
)

// For any SCORCH app metadata, DecodeMetadata does not panic. A decoded
// metadata has each component under its name, each run and nested loop has a
// count other than 0, and the replacements of each resolve or fail.
func FuzzDecodeMetadata(f *testing.F) {
	for _, seed := range []string{
		`{}`,
		`{"runs":[{"start":["a"],"loop":{"count":2,"loop":{}}}],"components":[{"name":"a","type":"soh"}]}`,
		`{"runs":[{"replace":{"{{N}}":[1,2],"{{R}}":{"type":"int","uniform":{"minimum":1,"maximum":5}}}}]}`,
		`{"runs":[{"replace":{"{{G}}":{"gaussian":{},"exponential":{}}}}]}`,
		`{"runs":[{"replace":{"{{E}}":[]}}]}`,
		`{"runs":[{"loop":null}]}`,
		`{"components":[{"name":"a"},{"name":"a","background":true}]}`,
		`{"filebeat":{"enabled":true,"config":{"x":1}},"runs":[{"filebeat":{"enabled":false}}]}`,
		`{"runs":"all"}`,
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, body string) {
		var md map[string]any
		if json.Unmarshal([]byte(body), &md) != nil || md == nil {
			return
		}

		exp := &types.Experiment{
			Metadata: store.ConfigMetadata{Name: "exp"},
			Spec: &v1.ExperimentSpec{
				ExperimentNameF: "exp",
				ScenarioF:       &v2.ScenarioSpec{AppsF: []*v2.ScenarioApp{{NameF: "scorch", MetadataF: md}}},
			},
		}

		got, err := DecodeMetadata(exp)
		if err != nil {
			return
		}

		for _, c := range got.Components {
			if _, ok := got.ComponentSpecs()[c.Name]; !ok {
				t.Errorf("DecodeMetadata(%s) component specs have no %q", body, c.Name)
			}
		}

		for _, run := range got.Runs {
			for loop := run; loop != nil; loop = loop.Loop {
				if loop.Count == 0 {
					t.Errorf("DecodeMetadata(%s) loop %+v has count 0", body, loop)
				}

				_, _ = ResolveReplacements(loop.Replace)
			}
		}
	})
}
