package vm

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// FuzzNewDiskName checks newDiskName for any image name a topology can give: the new name always has a qcow
// extension and the new time, and a name without an earlier time keeps all of its text.
func FuzzNewDiskName(f *testing.F) {
	for _, base := range []string{
		"kali.qc2", "win/win10.qcow2", "old.qcow", "kali_20191117102805.qcow", "kali.img", "kali", "", ".", "a/b.c/d",
	} {
		f.Add(base)
	}

	qcow := []string{".qcow2", ".qc2", ".qcow"}
	now := time.Date(2026, 10, 31, 12, 30, 45, 0, time.UTC)
	stamp := "_" + now.Format("20060102150405")
	earlier := regexp.MustCompile(`_\d{14}`)

	f.Fuzz(func(t *testing.T, base string) {
		got := newDiskName(base, now)

		if !slices.Contains(qcow, filepath.Ext(got)) {
			t.Fatalf("newDiskName(%q) = %q, want a .qcow2, .qc2 or .qcow extension", base, got)
		}

		if !strings.Contains(got, stamp) {
			t.Fatalf("newDiskName(%q) = %q, want it to hold %s", base, got, stamp)
		}

		name := strings.TrimSuffix(base, filepath.Ext(base))
		if earlier.MatchString(name) {
			return
		}

		want := name + stamp + filepath.Ext(base)
		if !slices.Contains(qcow, filepath.Ext(want)) {
			want += qcow[1]
		}

		if got != want {
			t.Fatalf("newDiskName(%q) = %q, want %q", base, got, want)
		}
	})
}
