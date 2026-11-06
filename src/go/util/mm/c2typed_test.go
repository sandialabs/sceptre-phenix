package mm

import (
	"maps"
	"strings"
	"testing"

	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	"phenix/util/mm/mmtest"
)

// c2Listing is a `cc response` listing as minimega writes it: each file's
// path, a colon and a newline, then the file and a newline.
func c2Listing(files ...[2]string) string {
	var b strings.Builder

	for _, file := range files {
		b.WriteString(file[0] + ":\n" + file[1] + "\n")
	}

	return b.String()
}

func TestTypedC2Response(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("x", 100*1024)

	for name, tc := range map[string]struct {
		resp string
		typ  C2ResponseType
		want string
	}{
		"stdout of the client": {
			resp: c2Listing([2]string{"7/u1/stderr", "warn"}, [2]string{"7/u1/stdout", "a\nb"}, [2]string{"7/u2/stdout", "other"}),
			typ:  C2ResponseStdout,
			want: "a\nb",
		},
		"stderr of the client": {
			resp: c2Listing([2]string{"7/u2/stderr", "other"}, [2]string{"7/u1/stderr", "warn"}, [2]string{"7/u1/stdout", "a"}),
			typ:  C2ResponseStderr,
			want: "warn",
		},
		"no file of that type": {
			resp: c2Listing([2]string{"7/u1/stdout", "a"}, [2]string{"7/u2/stderr", "other"}),
			typ:  C2ResponseStderr,
			want: "",
		},
		"keeps empty lines": {
			resp: c2Listing([2]string{"7/u1/stdout", "a\n\nb\n"}),
			typ:  C2ResponseStdout,
			want: "a\n\nb\n",
		},
		"a line longer than the default scanner limit": {
			resp: c2Listing([2]string{"7/u1/stdout", long}, [2]string{"7/u2/stdout", "other"}),
			typ:  C2ResponseStdout,
			want: long,
		},
		"a line that only holds a header-like path": {
			resp: c2Listing([2]string{"7/u1/stdout", "build/logs/stderr: none\nok"}),
			typ:  C2ResponseStdout,
			want: "build/logs/stderr: none\nok",
		},
		"an empty file of the client does not take the next client's": {
			resp: "7/u1/stdout:\n7/u2/stdout:\nother\n",
			typ:  C2ResponseStdout,
			want: "",
		},
		"the first of several commands": {
			resp: c2Listing([2]string{"7/u1/stdout", "first"}, [2]string{"8/u1/stdout", "second"}),
			typ:  C2ResponseStdout,
			want: "first",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := typedC2Response(tc.resp, "u1", tc.typ); got != tc.want {
				t.Errorf("typedC2Response(%.80q, u1, %s) = %.80q, want %.80q", tc.resp, tc.typ, got, tc.want)
			}
		})
	}
}

// GetC2Response with a type reads the command's responses, then the VM's
// UUID by its exact name, and returns that client's file of the type.
func TestGetC2ResponseOfAType(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	listing := c2Listing([2]string{"7/uuid-A/stdout", "upper"}, [2]string{"7/uuid-a/stderr", "warn"},
		[2]string{"7/uuid-a/stdout", "lower"})

	for name, tc := range map[string]struct {
		opts    []C2Option
		answers map[string][]*minicli.Response
		want    string
		sent    []string
		wantMsg string
	}{
		"stdout": {
			opts: []C2Option{C2VM("a"), C2ResponseTypeStdout()},
			want: "lower",
			sent: []string{inExp("cc response 7"), inExp(`.columns "name","uuid" .filter name=a vm info`)},
		},
		"stderr": {
			opts: []C2Option{C2VM("a"), C2ResponseTypeStderr()},
			want: "warn",
			sent: []string{inExp("cc response 7"), inExp(`.columns "name","uuid" .filter name=a vm info`)},
		},
		"needs a VM": {
			opts:    []C2Option{C2ResponseTypeStdout()},
			wantMsg: "must provide VM",
		},
		"reports a missing VM": {
			opts:    []C2Option{C2VM("b"), C2ResponseTypeStdout()},
			answers: map[string][]*minicli.Response{vmInfoCmd: nil},
			sent:    []string{inExp("cc response 7"), inExp(`.columns "name","uuid" .filter name=b vm info`)},
			wantMsg: "vm b does not exist",
		},
		"reports a failed listing": {
			opts:    []C2Option{C2VM("a"), C2ResponseTypeStdout()},
			answers: map[string][]*minicli.Response{"cc response 7": failed("no responses for 7")},
			sent:    []string{inExp("cc response 7")},
			wantMsg: "no responses for 7",
		},
	} {
		t.Run(name, func(t *testing.T) {
			answers := map[string][]*minicli.Response{
				"cc response 7": {mmtest.Text("head", listing)},
				// The `.filter` lookup folds case, so both VMs answer.
				vmInfoCmd: {mmtest.Tabular("head", []string{"name", "uuid"}, []string{"A", "uuid-A"}, []string{"a", "uuid-a"})},
			}

			maps.Copy(answers, tc.answers)

			commandCase{
				call: func() (any, error) {
					return GetC2Response(append([]C2Option{C2NS("exp"), C2CommandID("7")}, tc.opts...)...)
				},
				answers: answers,
				sent:    tc.sent,
				want:    tc.want,
				wantMsg: tc.wantMsg,
			}.run(t)
		})
	}
}
