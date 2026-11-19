package web

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/store"
	"phenix/util/envsubst"
)

// requestVariablesModel decodes data with encoding/json and reports whether
// it is a JSON object of strings whose names a request may set. It cannot see
// a duplicate name, which [json.Unmarshal] takes as the last value.
func requestVariablesModel(data []byte) (map[string]string, bool) {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil || raw == nil {
		return nil, false
	}

	vars := make(map[string]string, len(raw))

	for name, value := range raw {
		text, ok := value.(string)
		if !ok || !envsubst.ValidName(name) || name == store.BranchNameVar {
			return nil, false
		}

		vars[name] = text
	}

	return vars, true
}

// parseRequestVariables takes a variables part exactly when the model does,
// unless the part sets a name twice, and then gives the model's values.
func FuzzParseRequestVariables(f *testing.F) {
	for _, tt := range parseVariablesCases() {
		f.Add([]byte(tt.data))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		want, wantOK := requestVariablesModel(data)

		got, err := parseRequestVariables(data)
		if err != nil {
			if wantOK && !strings.Contains(err.Error(), "is set more than once") {
				t.Fatalf("parseRequestVariables(%q) error = %v, want %v", data, err, want)
			}

			return
		}

		if !wantOK {
			t.Fatalf("parseRequestVariables(%q) = %v, want an error", data, got)
		}

		if diff := cmp.Diff(want, got); diff != "" {
			t.Fatalf("parseRequestVariables(%q) mismatch (-want +got):\n%s", data, diff)
		}
	})
}
