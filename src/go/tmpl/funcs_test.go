package tmpl

import (
	"strings"
	"testing"
	"text/template"
)

func TestTemplateFuncs(t *testing.T) {
	t.Parallel()

	yes, no := true, false

	tests := []struct {
		name string
		text string
		data any
		want string
	}{
		{name: "addInt", text: `{{addInt 2 3}}`, want: "5"},
		{name: "addInt negative", text: `{{addInt -4 1}}`, want: "-3"},
		{name: "derefBool true", text: `{{derefBool .}}`, data: &yes, want: "true"},
		{name: "derefBool false", text: `{{derefBool .}}`, data: &no, want: "false"},
		{name: "derefBool nil", text: `{{derefBool .}}`, data: (*bool)(nil), want: "false"},
		{name: "cidrToMask /24", text: `{{cidrToMask "10.0.0.0/24"}}`, want: "255.255.255.0"},
		{name: "cidrToMask /30", text: `{{cidrToMask "192.168.1.5/30"}}`, want: "255.255.255.252"},
		{name: "cidrToMask /0", text: `{{cidrToMask "0.0.0.0/0"}}`, want: "0.0.0.0"},
		{name: "cidrToMask without length", text: `{{cidrToMask "10.0.0.1"}}`, want: "0.0.0.0"},
		{name: "cidrToMask not a CIDR", text: `{{cidrToMask "bad"}}`, want: "0.0.0.0"},
		{name: "toBool true string", text: `{{toBool "true"}}`, want: "true"},
		{name: "toBool 1 string", text: `{{toBool "1"}}`, want: "true"},
		{name: "toBool false string", text: `{{toBool "F"}}`, want: "false"},
		{name: "toBool other string", text: `{{toBool "yes"}}`, want: "false"},
		{name: "toBool int", text: `{{toBool 7}}`, want: "true"},
		{name: "toBool zero int", text: `{{toBool 0}}`, want: "false"},
		{name: "toBool bool", text: `{{toBool true}}`, want: "true"},
		{name: "toBool float", text: `{{toBool 1.5}}`, want: "false"},
		{name: "toBool nil", text: `{{toBool nil}}`, want: "false"},
		{name: "stringsJoin", text: `{{stringsJoin . ","}}`, data: []string{"a", "b", "c"}, want: "a,b,c"},
		{name: "stringsJoin empty", text: `[{{stringsJoin . ","}}]`, data: []string{}, want: "[]"},
		{name: "escapeQuoted", text: `{{escapeQuoted .}}`, data: "a\"b\\c\nd", want: `a\"b\\c\nd`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tpl, err := template.New(tt.name).Funcs(templateFuncs()).Parse(tt.text)
			if err != nil {
				t.Fatalf("parsing %q: %v", tt.text, err)
			}

			var out strings.Builder
			if err := tpl.Execute(&out, tt.data); err != nil {
				t.Fatalf("executing %q: %v", tt.text, err)
			}

			if got := out.String(); got != tt.want {
				t.Errorf("%s with %v = %q, want %q", tt.text, tt.data, got, tt.want)
			}
		})
	}
}
