package util_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/util"
)

func TestDecodeJSONStrict(t *testing.T) {
	type value struct {
		Name string `json:"name"`
	}

	for _, tt := range []struct {
		name     string
		input    string
		trailing bool
		fails    bool
	}{
		{name: "one object", input: `{"name":"a"}`},
		{name: "surrounding space", input: " {\"name\":\"a\"} \n"},
		{name: "a second object", input: `{"name":"a"} {"name":"b"}`, trailing: true, fails: true},
		{name: "a closing brace", input: `{"name":"a"}}`, trailing: true, fails: true},
		{name: "a closing bracket", input: `{"name":"a"}]`, trailing: true, fails: true},
		{name: "trailing text", input: `{"name":"a"} nope`, trailing: true, fails: true},
		{name: "an unknown field", input: `{"name":"a","other":1}`, fails: true},
		{name: "a truncated object", input: `{"name":"a"`, fails: true},
		{name: "empty", input: ``, fails: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var got value

			err := util.DecodeJSONStrict(strings.NewReader(tt.input), &got)

			switch {
			case (err != nil) != tt.fails:
				t.Errorf("DecodeJSONStrict(%q) error = %v, want failure %t", tt.input, err, tt.fails)
			case errors.Is(err, util.ErrTrailingJSON) != tt.trailing:
				t.Errorf("DecodeJSONStrict(%q) error = %v, want trailing content %t", tt.input, err, tt.trailing)
			case err == nil:
				if diff := cmp.Diff(value{Name: "a"}, got); diff != "" {
					t.Errorf("DecodeJSONStrict(%q) mismatch (-want +got):\n%s", tt.input, diff)
				}
			}
		})
	}
}
