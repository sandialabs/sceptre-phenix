package mmcli

import (
	"errors"
	"strconv"
	"testing"

	"github.com/sandia-minimega/minimega/v2/pkg/minicli"
)

// FuzzHostResponse checks HostResponse against a model: each byte of layout is
// one host's answer or error, and a byte of 255 starts the next reply.
func FuzzHostResponse(f *testing.F) {
	hosts := []string{"head", "compute1", "compute2", ""}

	f.Add("compute1", []byte{0, 1, 255, 5})
	f.Add("head", []byte{4, 255, 0})
	f.Add("compute2", []byte{})
	f.Add("", []byte{4, 1})

	f.Fuzz(func(t *testing.T, host string, layout []byte) {
		var (
			replies  = []minicli.Responses{nil}
			want     string
			answered bool
			anyErr   bool
		)

		for i, b := range layout {
			if b == 255 {
				replies = append(replies, nil)

				continue
			}

			from := hosts[int(b)%len(hosts)]

			r := hostAnswered(from, strconv.Itoa(i), i)
			if b&4 != 0 {
				r = hostError(from, ErrTimeout)
				anyErr = true
			}

			if !answered && r.Error == "" && (from == host || host == "") {
				want, answered = r.Response, true
			}

			replies[len(replies)-1] = append(replies[len(replies)-1], r)
		}

		ch := hostResponses(replies...)

		got, err := HostResponse(ch, host)

		switch {
		case len(ch) != 0:
			t.Fatalf("HostResponse(%q) left %d replies in the channel", host, len(ch))
		case answered && (err != nil || got != want):
			t.Fatalf("HostResponse(%q) = %q, %v, want %q, nil", host, got, err, want)
		case !answered && host != "" && !errors.Is(err, ErrNoHostAnswer):
			t.Fatalf("HostResponse(%q) error = %v, want %v", host, err, ErrNoHostAnswer)
		case !answered && host != "" && anyErr != errors.Is(err, ErrTimeout):
			t.Fatalf("HostResponse(%q) error = %v, want it to hold the hosts' errors", host, err)
		case !answered && host == "" && got != "":
			t.Fatalf("HostResponse(\"\") = %q, %v with no answer to take", got, err)
		}
	})
}
