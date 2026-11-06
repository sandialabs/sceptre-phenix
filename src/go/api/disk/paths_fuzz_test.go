package disk

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"phenix/util/common"
	"phenix/util/mm"
	"phenix/util/mm/mmtest"
)

// pathsFuzzResolve is a model of Resolve: the cleaned path p names, relative to dir or absolute, when it is below dir
// and no segment below dir is one the listing leaves out.
func pathsFuzzResolve(dir, p string) (string, bool) {
	if p == "" {
		return "", false
	}

	path := p
	if !strings.HasPrefix(p, "/") {
		path = dir + "/" + p
	}

	path = filepath.Clean(path)

	rel, ok := strings.CutPrefix(path, dir+"/")
	if !ok {
		return "", false
	}

	for i, seg := range strings.Split(rel, "/") {
		switch {
		case strings.HasPrefix(seg, "."), seg == "lost+found",
			i == 0 && (seg == "saved" || strings.HasPrefix(seg, "transfer_")),
			i == 1 && (seg == "files" || seg == "tmp" || seg == "miniccc_responses"):
			return "", false
		}
	}

	return path, true
}

// Resolve accepts a path, relative or absolute, only when the model does, and then as the model's cleaned path, which
// it accepts again as it is; it refuses every other path with ErrNotManaged.
func FuzzResolve(f *testing.F) {
	mmtest.Use(f, nil)

	original := common.PhenixBase
	common.PhenixBase = f.TempDir() //nolint:reassign // the files directory's parent

	f.Cleanup(func() { common.PhenixBase = original }) //nolint:reassign // restore

	dir := mm.GetMMFullPath("")

	for _, seed := range []string{
		"a.qc2", "win/10/a b.qc2", dir + "/win/./x/../a.qc2", "files/a.qc2", "a/b/files/a.qc2", "a/transfer_1/a.qc2",
		"win/../a.qc2", "", ".", dir, "../a.qc2", "win/../../a.qc2", "/etc/a.qc2", dir + "-other/a.qc2",
		"exp/files/s.hdd", "exp/tmp/snapshot-1.qc2", "exp/miniccc_responses/x.qc2", ".hidden/a.qc2", "win/.a.qc2",
		"..a.qc2", "win/lost+found/a.qc2", "saved/vm/a.hdd", "transfer_1/a.qc2", "a//b/", "/", "a\x00b",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, p string) {
		want, ok := pathsFuzzResolve(dir, p)

		got, err := Resolve(p)
		if ok != (err == nil) || got != want {
			t.Fatalf("Resolve(%q) = %q, %v; want %q, accepted: %t", p, got, err, want, ok)
		}

		if err != nil {
			if !errors.Is(err, ErrNotManaged) {
				t.Fatalf("Resolve(%q) error = %v, want %v", p, err, ErrNotManaged)
			}

			return
		}

		if again, err := Resolve(got); again != got || err != nil {
			t.Fatalf("Resolve(%q) = %q, %v; want %q, nil", got, again, err, got)
		}
	})
}
