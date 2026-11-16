package mmcli

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"
	"github.com/sandia-minimega/minimega/v2/pkg/miniclient"
)

// hostResponses is a closed channel that holds one minimega reply for each
// item of resps.
func hostResponses(resps ...minicli.Responses) chan *miniclient.Response {
	ch := make(chan *miniclient.Response, len(resps))

	for _, r := range resps {
		ch <- &miniclient.Response{Resp: r}
	}

	close(ch)

	return ch
}

// hostAnswered is an answer of host that holds both text and user data.
func hostAnswered(host, body string, data any) *minicli.Response {
	return &minicli.Response{Host: host, Response: body, Data: data}
}

func hostError(host string, err error) *minicli.Response {
	return &minicli.Response{Host: host, Error: err.Error()}
}

// HostResponse and HostDataResponse read the answer of one host, as each host
// of a namespace answers a cc command with its own command ID and responses.
func TestHostResponse(t *testing.T) {
	t.Parallel()

	sibling := errors.New("no responses for 7")

	for name, tc := range map[string]struct {
		host      string
		responses []minicli.Responses
		want      string
		wantData  any
		wantErr   []error
	}{
		"the host's answer, not the first one": {
			host: "compute1",
			responses: []minicli.Responses{
				{hostAnswered("head", "other", 8)},
				{hostAnswered("compute1", "mine", 3)},
			},
			want:     "mine",
			wantData: 3,
		},
		"the host's first answer": {
			host:      "head",
			responses: []minicli.Responses{{hostAnswered("head", "first", 1), hostAnswered("head", "second", 2)}},
			want:      "first",
			wantData:  1,
		},
		"the host's answer despite errors of other hosts": {
			host:      "compute1",
			responses: []minicli.Responses{{hostError("head", sibling)}, {hostAnswered("compute1", "mine", 3)}},
			want:      "mine",
			wantData:  3,
		},
		"the host failed": {
			host:      "compute1",
			responses: []minicli.Responses{{hostAnswered("head", "other", 8), hostError("compute1", ErrTimeout)}},
			wantErr:   []error{ErrNoHostAnswer, ErrTimeout},
		},
		"the connection was lost": {
			host:      "compute1",
			responses: []minicli.Responses{{hostError("", ErrNoResponse)}},
			wantErr:   []error{ErrNoHostAnswer, ErrNoResponse},
		},
		"the host did not answer": {
			host:      "compute1",
			responses: []minicli.Responses{{hostAnswered("head", "other", 8)}},
			wantErr:   []error{ErrNoHostAnswer},
		},
		"no host is the first answer": {
			host:      "",
			responses: []minicli.Responses{{hostError("head", sibling)}, {hostAnswered("compute1", "a", 1)}},
			want:      "a",
			wantData:  1,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			text := hostResponses(tc.responses...)

			got, err := HostResponse(text, tc.host)
			for _, want := range tc.wantErr {
				if !errors.Is(err, want) {
					t.Errorf("HostResponse(%q) error = %v, want %v", tc.host, err, want)
				}
			}

			if tc.wantErr == nil && err != nil {
				t.Errorf("HostResponse(%q) error = %v, want nil", tc.host, err)
			}

			if got != tc.want {
				t.Errorf("HostResponse(%q) = %q, want %q", tc.host, got, tc.want)
			}

			if len(text) != 0 {
				t.Errorf("HostResponse(%q) left %d responses in the channel, want 0", tc.host, len(text))
			}

			data, err := HostDataResponse(hostResponses(tc.responses...), tc.host)
			if (err != nil) != (tc.wantErr != nil) {
				t.Errorf("HostDataResponse(%q) error = %v, want an error %t", tc.host, err, tc.wantErr != nil)
			}

			if diff := cmp.Diff(tc.wantData, data); diff != "" {
				t.Errorf("HostDataResponse(%q) mismatch (-want +got):\n%s", tc.host, diff)
			}
		})
	}
}
