package web

import (
	"net"
	"strconv"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

func TestNormalizeFileServerEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		endpoint string
		wantErr  bool
	}{
		{name: "empty disabled", value: ""},
		{name: "zero disabled", value: "0"},
		{name: "port only", value: "8080", endpoint: "127.0.0.1:8080"},
		{name: "ipv4", value: "127.0.0.1:8080", endpoint: "127.0.0.1:8080"},
		{name: "hostname", value: "localhost:8080", endpoint: "localhost:8080"},
		{name: "all interfaces", value: "0.0.0.0:8080", endpoint: "0.0.0.0:8080"},
		{name: "empty host", value: ":8080", endpoint: ":8080"},
		{name: "ipv6", value: "[::1]:8080", endpoint: "[::1]:8080"},
		{name: "invalid bare host", value: "abc", wantErr: true},
		{name: "missing port", value: "localhost", wantErr: true},
		{name: "invalid port", value: "127.0.0.1:notaport", wantErr: true},
		{name: "port out of range", value: "99999", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			endpoint, err := normalizeFileServerEndpoint(tt.value)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if endpoint != tt.endpoint {
				t.Fatalf("endpoint = %q, want %q", endpoint, tt.endpoint)
			}
		})
	}
}

// fileserverEndpointPort is a port as the option spells it, and whether it is
// a port number a listener takes.
type fileserverEndpointPort struct {
	spelled string
	valid   bool
}

// genFileserverEndpointPort draws numbers in and out of the port range, zero
// padded or signed at times, and words that are no number.
func genFileserverEndpointPort() *rapid.Generator[fileserverEndpointPort] {
	return rapid.Custom(func(t *rapid.T) fileserverEndpointPort {
		if rapid.Bool().Draw(t, "word") {
			return fileserverEndpointPort{spelled: rapid.StringMatching(`[0-9]{0,2}[a-z][a-z0-9]{0,3}`).Draw(t, "spelled")}
		}

		n := rapid.OneOf(rapid.IntRange(-2, 70000), rapid.SampledFrom([]int{0, 65535, 65536})).Draw(t, "n")
		spelled := strconv.Itoa(max(n, -n))

		// "0" turns the file server off, so 0 has no other spelling
		if n != 0 {
			spelled = strings.Repeat("0", rapid.IntRange(0, 2).Draw(t, "zeros")) + spelled
		}

		if n < 0 {
			spelled = "-" + spelled
		} else if n > 0 && rapid.Bool().Draw(t, "plus") {
			spelled = "+" + spelled
		}

		return fileserverEndpointPort{spelled: spelled, valid: n >= 0 && n <= 65535}
	})
}

// A host and port is kept as given when the port is in range, a bare port
// listens on localhost, and surrounding space changes nothing.
func TestNormalizeFileServerEndpointProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		port := genFileserverEndpointPort().Draw(t, "port")
		host := rapid.SampledFrom([]string{"", "127.0.0.1", "localhost", "0.0.0.0", "::1", "phenix.example"}).Draw(t, "host")
		space := rapid.SampledFrom([]string{"", " ", "\t", " \n"}).Draw(t, "space")

		value := net.JoinHostPort(host, port.spelled)

		got, err := normalizeFileServerEndpoint(space + value + space)
		if port.valid && (err != nil || got != value) {
			t.Fatalf("normalizeFileServerEndpoint(%q) = %q, %v, want %q", space+value+space, got, err, value)
		}

		if !port.valid && err == nil {
			t.Fatalf("normalizeFileServerEndpoint(%q) = %q, want an error", space+value+space, got)
		}

		want := net.JoinHostPort("127.0.0.1", port.spelled)
		if port.spelled == "0" {
			want = ""
		}

		got, err = normalizeFileServerEndpoint(space + port.spelled + space)
		if port.valid && (err != nil || got != want) {
			t.Fatalf("normalizeFileServerEndpoint(%q) = %q, %v, want %q", space+port.spelled+space, got, err, want)
		}

		if !port.valid && err == nil {
			t.Fatalf("normalizeFileServerEndpoint(%q) = %q, want an error", space+port.spelled+space, got)
		}
	})
}
