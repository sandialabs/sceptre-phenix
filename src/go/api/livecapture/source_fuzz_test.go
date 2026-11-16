package livecapture

import (
	"errors"
	"path"
	"strings"
	"testing"
)

// sourceSafe reports whether every byte of s is one safePath allows.
func sourceSafe(s string) bool {
	for i := range len(s) {
		switch c := s[i]; {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9', strings.IndexByte("._+=@,:/-", c) >= 0:
		default:
			return false
		}
	}

	return true
}

// FuzzNewMeshSource checks that newMeshSource, which guards the arguments of
// a remote shell command, accepts exactly the clean absolute paths and the
// hosts of safe characters without '/', and refuses the rest with ErrUnsafePath.
func FuzzNewMeshSource(f *testing.F) {
	for _, seed := range [][2]string{
		{"compute1", "/phenix/images/exp/files/vm-0.pcap"},
		{"compute1", "relative.pcap"},
		{"compute1", "/phenix/images/exp/files/../../../etc/shadow"},
		{"compute1", "/phenix/images/exp/files/a b.pcap"},
		{"compute1", `/phenix/images/exp/files/a"b.pcap`},
		{"compute1", "/phenix/images/exp/files/$(reboot).pcap"},
		{"compute1", "/"},
		{"compute1", "/a//b"},
		{"compute1 shell reboot", "/a.pcap"},
		{"", "/a.pcap"},
		{"/", "/a.pcap"},
		{"a/b", "/a.pcap"},
		{"compute1", "/a.pcap\n"},
	} {
		f.Add(seed[0], seed[1])
	}

	f.Fuzz(func(t *testing.T, host, capturePath string) {
		want := len(capturePath) > 1 && capturePath[0] == '/' && path.Clean(capturePath) == capturePath &&
			sourceSafe(capturePath) && host != "" && sourceSafe(host) && !strings.Contains(host, "/")

		src, err := newMeshSource(host, capturePath)
		if (err == nil) != want {
			t.Fatalf("newMeshSource(%q, %q) error = %v, want accepted %t", host, capturePath, err, want)
		}

		if err != nil {
			if !errors.Is(err, ErrUnsafePath) {
				t.Fatalf("newMeshSource(%q, %q) error = %v, want %v", host, capturePath, err, ErrUnsafePath)
			}

			return
		}

		if src.host != host || src.path != capturePath {
			t.Fatalf("newMeshSource(%q, %q) = %+v, want them unchanged", host, capturePath, src)
		}
	})
}
