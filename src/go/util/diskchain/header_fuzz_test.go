package diskchain_test

import (
	"bytes"
	"testing"

	"phenix/util/diskchain"
)

// FuzzParseHeader checks that ParseHeader never panics on an untrusted image header, and that what it parses is a
// header qemu could parse (see checkHeader).
func FuzzParseHeader(f *testing.F) {
	for _, seed := range headerSeeds() {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		h, err := diskchain.ParseHeader(bytes.NewReader(data))
		if problem := checkHeader(data, h, err); problem != nil {
			t.Fatalf("ParseHeader(%q): %v", data, problem)
		}
	})
}
