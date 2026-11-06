package store

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"gopkg.in/yaml.v3"

	"phenix/types/version"
	"phenix/util/common"
)

// typesFullNameModel names the config that s identifies: one "/", and a kind
// part that is a stored kind in any case. It returns "" for any other s.
func typesFullNameModel(s string) string {
	kind, name, ok := strings.Cut(s, "/")
	if !ok || strings.Contains(name, "/") {
		return ""
	}

	for k := range version.StoredVersion {
		if strings.EqualFold(k, kind) {
			return k + "/" + name
		}
	}

	return ""
}

// NewConfig and both forms of ConfigFullName agree with typesFullNameModel for
// any kind and name, and a full name names itself.
func FuzzConfigName(f *testing.F) {
	for _, seed := range [][2]string{
		{"topology", "test-topo"}, {"Topology", "test-topo"}, {"TOPOLOGY", "test-topo"},
		{"notakind", "test"}, {"workflow", "wf"}, {"Workflow", "wf"}, {"WORKFLOW", "wf"},
		{"topologys", "a"}, {"scenario", "a/b"}, {"topology/a", "b"}, {"", ""}, {"setting", ""},
	} {
		f.Add(seed[0], seed[1])
	}

	f.Fuzz(func(t *testing.T, kind, name string) {
		for _, s := range []string{kind, kind + "/" + name} {
			want := typesFullNameModel(s)

			if got := ConfigFullName(s); got != want {
				t.Fatalf("ConfigFullName(%q) = %q, want %q", s, got, want)
			}

			if want != "" {
				if got := ConfigFullName(want); got != want {
					t.Fatalf("ConfigFullName(%q) = %q, want it unchanged", want, got)
				}
			}

			c, err := NewConfig(s)
			if (err == nil) != (want != "") {
				t.Fatalf("NewConfig(%q) error = %v, want error %t", s, err, want == "")
			}

			if err != nil {
				continue
			}

			stored := version.StoredVersion[c.Kind]
			_, wantName, _ := strings.Cut(s, "/")

			if c.FullName() != want || c.Metadata.Name != wantName || c.Version != APIGroup+"/"+stored ||
				c.APIGroup() != APIGroup || c.APIVersion() != stored {
				t.Fatalf("NewConfig(%q) = %s named %q at %s, want %s named %q at %s/%s",
					s, c.FullName(), c.Metadata.Name, c.Version, want, wantName, APIGroup, stored)
			}
		}

		// the two-argument form takes the name as given
		want := ""
		if k, _, _ := strings.Cut(typesFullNameModel(kind+"/x"), "/"); k != "" {
			want = k + "/" + name
		}

		if got := ConfigFullName(kind, name); got != want {
			t.Fatalf("ConfigFullName(%q, %q) = %q, want %q", kind, name, got, want)
		}
	})
}

// typesCheckClientFields fails t when a parsed config keeps a field that only
// the store sets.
func typesCheckClientFields(t *testing.T, from string, c *Config) {
	t.Helper()

	if c.Metadata.Created != "" || c.Metadata.Updated != "" || c.Metadata.Revision != 0 || c.IfRevision != 0 {
		t.Fatalf("%s kept created %q, updated %q, revision %d, if-revision %d; want all empty",
			from, c.Metadata.Created, c.Metadata.Updated, c.Metadata.Revision, c.IfRevision)
	}
}

var typesConfigSeeds = []string{ //nolint:gochecknoglobals // shared fuzz seeds
	`{"apiVersion":"phenix.sandia.gov/v1","kind":"Topology","metadata":{"name":"a","revision":7}}`,
	`{"apiVersion":"phenix.sandia.gov/v1","kind":"Topology","metadata":{"name":"a","created":"x","updated":"y"}}`,
	`{"kind":"Scenario","metadata":{"name":"s","annotations":{"k":"v"}},"spec":{"apps":[{"name":"a","n":1.5}]},"status":{}}`,
	"apiVersion: phenix.sandia.gov/v1\nkind: Topology\nmetadata:\n  name: a\n  revision: 7\n",
	"kind: Topology\nmetadata:\n  name: ${PHENIX_FUZZ_UNSET:dflt}\nspec:\n  nodes: []\n",
	`null`, `{}`, `{"metadata":{"revision":-1}}`,
}

// A JSON config never keeps a store-set field, and writing it as JSON and
// parsing that again gives the same config.
func FuzzDecodeConfigJSON(f *testing.F) {
	for _, seed := range typesConfigSeeds {
		f.Add([]byte(seed))
	}

	f.Fuzz(func(t *testing.T, body []byte) {
		c, err := DecodeConfigJSON(body)
		if err != nil {
			return
		}

		typesCheckClientFields(t, "DecodeConfigJSON", c)

		out, err := json.Marshal(c)
		if err != nil {
			t.Fatalf("json.Marshal(DecodeConfigJSON(%q)) error = %v", body, err)
		}

		again, err := DecodeConfigJSON(out)
		if err != nil {
			t.Fatalf("DecodeConfigJSON(%s) of its own output error = %v", out, err)
		}

		if diff := cmp.Diff(c, again, cmpopts.EquateEmpty()); diff != "" {
			t.Fatalf("DecodeConfigJSON(%s) round trip mismatch (-first +second):\n%s", out, diff)
		}
	})
}

// A YAML config never keeps a store-set field, and writing it as YAML and
// parsing that again gives the same kind, version and metadata.
func FuzzNewConfigFromYAML(f *testing.F) {
	for _, seed := range typesConfigSeeds {
		f.Add([]byte(seed))
	}

	f.Fuzz(func(t *testing.T, body []byte) {
		c, err := NewConfigFromYAML(body)
		if err != nil {
			return
		}

		typesCheckClientFields(t, "NewConfigFromYAML", c)

		// the store writes JSON, so it never keeps a map with a key that is not a string
		if _, err := json.Marshal(c); err != nil {
			return
		}

		out, err := yaml.Marshal(c)
		if err != nil {
			t.Fatalf("yaml.Marshal(NewConfigFromYAML(%q)) error = %v", body, err)
		}

		if common.EnvPlaceholder.Match(out) { // a second parse would expand it
			return
		}

		again, err := NewConfigFromYAML(out)
		if err != nil {
			t.Fatalf("NewConfigFromYAML(%q) of its own output error = %v", out, err)
		}

		// YAML may give a spec number another Go type on a second parse
		ignoreBody := cmpopts.IgnoreFields(Config{}, "Spec", "Status")
		if diff := cmp.Diff(c, again, ignoreBody, cmpopts.EquateEmpty()); diff != "" {
			t.Fatalf("NewConfigFromYAML(%q) round trip mismatch (-first +second):\n%s", out, diff)
		}
	})
}
