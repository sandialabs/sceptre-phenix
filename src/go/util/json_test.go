package util_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/util"
)

func TestDecodeJSONStrict(t *testing.T) {
	type value struct {
		Name string `json:"name"`
	}

	long := `{"name":"` + strings.Repeat("b", 70) + `"}`

	for _, tt := range []struct {
		name     string
		input    string
		trailing bool
		fails    bool
		// message is the whole error text, where the test checks it.
		message string
		want    value
	}{
		{name: "one object", input: `{"name":"a"}`, want: value{Name: "a"}},
		{name: "surrounding space", input: " {\"name\":\"a\"} \n", want: value{Name: "a"}},
		{name: "Unicode", input: `{"name":"día é 🚀"}`, want: value{Name: "día é 🚀"}},
		{
			name: "a second object", input: `{"name":"a"} {"name":"b"}`, trailing: true, fails: true,
			message: `unexpected trailing content: {"name":"b"}`,
		},
		{
			name: "a long second object", input: `{"name":"a"}` + long, trailing: true, fails: true,
			message: "unexpected trailing content: " + long[:64] + "...",
		},
		{name: "a closing brace", input: `{"name":"a"}}`, trailing: true, fails: true},
		{name: "a closing bracket", input: `{"name":"a"}]`, trailing: true, fails: true},
		{name: "trailing text", input: `{"name":"a"} nope`, trailing: true, fails: true},
		{name: "a truncated second object", input: `{"name":"a"} {"name"`, trailing: true, fails: true},
		{name: "an unknown field", input: `{"name":"a","other":1}`, fails: true, message: `json: unknown field "other"`},
		{name: "a truncated object", input: `{"name":"a"`, fails: true, message: "unexpected EOF"},
		{name: "a wrong type", input: `{"name":1}`, fails: true},
		{name: "empty", input: ``, fails: true, message: "EOF"},
		{name: "only space", input: " \n\t", fails: true, message: "EOF"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var got value

			err := util.DecodeJSONStrict(strings.NewReader(tt.input), &got)

			switch {
			case (err != nil) != tt.fails:
				t.Errorf("DecodeJSONStrict(%q) error = %v, want failure %t", tt.input, err, tt.fails)
			case errors.Is(err, util.ErrTrailingJSON) != tt.trailing:
				t.Errorf("DecodeJSONStrict(%q) error = %v, want trailing content %t", tt.input, err, tt.trailing)
			case tt.message != "" && err.Error() != tt.message:
				t.Errorf("DecodeJSONStrict(%q) error = %q, want %q", tt.input, err, tt.message)
			case err == nil:
				if diff := cmp.Diff(tt.want, got); diff != "" {
					t.Errorf("DecodeJSONStrict(%q) mismatch (-want +got):\n%s", tt.input, diff)
				}
			}
		})
	}

	// Other errors are the decoder's own, not wrapped.
	err := util.DecodeJSONStrict(strings.NewReader(`{"name":"a"`), new(value))
	if err != io.ErrUnexpectedEOF { //nolint:errorlint // checks it is not wrapped
		t.Errorf("DecodeJSONStrict(truncated) error = %#v, want io.ErrUnexpectedEOF itself", err)
	}
}

// FuzzDecodeJSONStrict checks that DecodeJSONStrict accepts exactly the input
// that holds one JSON value, and that it decodes that value as it is.
func FuzzDecodeJSONStrict(f *testing.F) {
	for _, seed := range []string{`{"a":[1,2.5,"x"]}`, ` null `, `{"a":1}}`, `1 2`, `"\ud800"`, `[`} {
		f.Add([]byte(seed))
	}

	f.Fuzz(func(t *testing.T, input []byte) {
		var raw json.RawMessage

		err := util.DecodeJSONStrict(bytes.NewReader(input), &raw)
		if valid := json.Valid(input); (err == nil) != valid {
			t.Fatalf("DecodeJSONStrict(%q) error = %v, but json.Valid = %t", input, err, valid)
		}

		if err != nil {
			return
		}

		if want := bytes.Trim(input, " \t\r\n"); !bytes.Equal(raw, want) {
			t.Errorf("DecodeJSONStrict(%q) = %q, want the value %q", input, raw, want)
		}

		var again json.RawMessage
		if err := util.DecodeJSONStrict(bytes.NewReader(raw), &again); err != nil || !bytes.Equal(again, raw) {
			t.Errorf("DecodeJSONStrict(%q), the value it decoded, = %q, %v, want it again", raw, again, err)
		}
	})
}
