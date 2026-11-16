package mm

import (
	"regexp"
	"strings"
	"testing"
)

// diskSpecGrammar is minimega's diskspec grammar as a regular expression, an
// oracle independent of parseDiskSpec.
var diskSpecGrammar = regexp.MustCompile(
	`^[^,]+(,(ahci|ide|scsi|sd|mtd|floppy|pflash|virtio))?(,(none|writeback|unsafe|directsync|writethrough))?$`,
)

// FuzzParseDiskSpec checks parseDiskSpec against minimega's diskspec grammar:
// it accepts a spec only when the grammar does, and String gives the spec back.
func FuzzParseDiskSpec(f *testing.F) {
	for _, seed := range []string{
		"/images/a.qc2", "/images/a.qc2,writeback", "/images/a.qc2,virtio", "a.qc2,ide,unsafe",
		"a.qc2,writeback,virtio", "a.qc2,none,none", "a.qc2,bogus", "/images/a.qc2,", ",virtio", "",
		"a.qc2,virtio,writeback,none", "a b,virtio", "a.qc2 b.qc2,none",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, spec string) {
		got, err := parseDiskSpec(spec)
		if accepted := err == nil; accepted != diskSpecGrammar.MatchString(spec) {
			t.Fatalf("parseDiskSpec(%q) error = %v, want accepted %t", spec, err, !accepted)
		}

		if err != nil {
			return
		}

		if s := got.String(); s != spec {
			t.Fatalf("parseDiskSpec(%q).String() = %q, want %q", spec, s, spec)
		}

		if again, err := parseDiskSpec(got.String()); err != nil || again != got {
			t.Fatalf("parseDiskSpec(%q) = %#v, %v, want %#v", got.String(), again, err, got)
		}

		if specs, err := parseDiskSpecs(spec); err == nil && joinDiskSpecs(specs) != strings.Join(strings.Fields(spec), " ") {
			t.Fatalf("joinDiskSpecs(parseDiskSpecs(%q)) = %q, want its fields", spec, joinDiskSpecs(specs))
		}
	})
}
