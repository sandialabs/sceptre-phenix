package broker

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	putil "phenix/util"
)

func TestErrorResultCarriesMessage(t *testing.T) {
	cause := errors.New("component foo failed")

	tests := map[string]struct {
		err  error
		want string
	}{
		"plain":     {err: fmt.Errorf("running run 0: %w", cause), want: "running run 0: component foo failed"},
		"humanized": {err: putil.HumanizeError(cause, "Unable to run SCORCH"), want: "Unable to run SCORCH (search error logs for "},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var result map[string]string
			if err := json.Unmarshal(errorResult(tc.err), &result); err != nil {
				t.Fatal(err)
			}

			if got := result["error"]; !strings.HasPrefix(got, tc.want) {
				t.Fatalf("error = %q, want prefix %q", got, tc.want)
			}
		})
	}
}
