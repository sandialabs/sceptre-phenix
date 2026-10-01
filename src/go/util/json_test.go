package util_test

import (
	"errors"
	"strings"
	"testing"

	"phenix/util"
)

func TestDecodeJSONStrict(t *testing.T) {
	type value struct {
		Name string `json:"name"`
	}

	for _, tt := range []struct {
		input    string
		trailing bool
		fails    bool
	}{
		{input: `{"name":"a"}`},
		{input: " {\"name\":\"a\"} \n"},
		{input: `{"name":"a"} {"name":"b"}`, trailing: true, fails: true},
		{input: `{"name":"a"}}`, trailing: true, fails: true},
		{input: `{"name":"a"}]`, trailing: true, fails: true},
		{input: `{"name":"a"} nope`, trailing: true, fails: true},
		{input: `{"name":"a","other":1}`, fails: true},
		{input: `{"name":"a"`, fails: true},
		{input: ``, fails: true},
	} {
		var got value

		err := util.DecodeJSONStrict(strings.NewReader(tt.input), &got)

		switch {
		case (err != nil) != tt.fails:
			t.Errorf("DecodeJSONStrict(%q) error = %v, want failure %t", tt.input, err, tt.fails)
		case errors.Is(err, util.ErrTrailingJSON) != tt.trailing:
			t.Errorf("DecodeJSONStrict(%q) error = %v, want trailing content %t", tt.input, err, tt.trailing)
		case err == nil && got.Name != "a":
			t.Errorf("DecodeJSONStrict(%q) decoded %+v", tt.input, got)
		}
	}
}
