package mmcli

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"
	"github.com/sandia-minimega/minimega/v2/pkg/miniclient"
)

// SingleDataResponse returns the first non-nil data, else the last error that
// no nil data entry cleared, and it drains the channel.
func TestSingleDataResponse(t *testing.T) {
	var (
		data    = func(d any) *minicli.Response { return &minicli.Response{Data: d} }
		failure = func(err error) *minicli.Response {
			return &minicli.Response{Error: err.Error()}
		}
	)

	tests := []struct {
		name      string
		responses []minicli.Responses
		want      any
		wantErr   error
	}{
		{
			name:      "data first",
			responses: []minicli.Responses{{data("a")}, {failure(ErrTimeout)}},
			want:      "a",
		},
		{
			name:      "the first data of many",
			responses: []minicli.Responses{{data("a"), data("b")}, {data("c")}},
			want:      "a",
		},
		{
			name:      "error then data in one response",
			responses: []minicli.Responses{{failure(ErrTimeout), data("a")}},
			want:      "a",
		},
		{
			name:      "error then data in a later response",
			responses: []minicli.Responses{{failure(ErrTimeout)}, {data("a")}},
			want:      "a",
		},
		{
			name:      "only errors",
			responses: []minicli.Responses{{failure(ErrTimeout)}, {failure(ErrNoResponse)}},
			wantErr:   ErrNoResponse,
		},
		{
			name:      "nil data then an error in a later response",
			responses: []minicli.Responses{{data(nil)}, {failure(ErrTimeout)}},
			wantErr:   ErrTimeout,
		},
		{
			name:      "nil data then data in a later response",
			responses: []minicli.Responses{{data(nil)}, {data("b")}},
			want:      "b",
		},
		{
			name:      "nil data stops the scan of its response",
			responses: []minicli.Responses{{failure(ErrNoResponse), data(nil), failure(ErrTimeout)}},
		},
		{
			name: "closed empty channel",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ch := make(chan *miniclient.Response, len(test.responses))
			for _, r := range test.responses {
				ch <- &miniclient.Response{Resp: r}
			}

			close(ch)

			got, err := SingleDataResponse(ch)
			if !errors.Is(err, test.wantErr) {
				t.Errorf("SingleDataResponse() error = %v, want %v", err, test.wantErr)
			}

			if diff := cmp.Diff(test.want, got); diff != "" {
				t.Errorf("SingleDataResponse() mismatch (-want +got):\n%s", diff)
			}

			if len(ch) != 0 {
				t.Errorf("SingleDataResponse() left %d responses in the channel, want 0", len(ch))
			}
		})
	}
}
