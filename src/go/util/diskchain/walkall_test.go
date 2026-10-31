package diskchain_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/util/diskchain"
	dct "phenix/util/diskchain/diskchaintest"
)

// WalkAll gives each path the result of Walk, or of Exists when openOnly names it, and finishes when a path repeats.
func TestWalkAll(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	over, broken, iso := filepath.Join(dir, "over.qc2"), filepath.Join(dir, "broken.qc2"), filepath.Join(dir, "cd.iso")

	dct.Chain(t, over, filepath.Join(dir, "base.qc2"))
	dct.WriteQcow2(t, broken, dct.Qcow2{Backing: filepath.Join(dir, "gone.qc2")})
	dct.WriteQcow2(t, iso, dct.Qcow2{Backing: filepath.Join(dir, "gone.qc2")})

	paths := []string{over, broken, iso, over, filepath.Join(dir, "none.qc2")}

	want := make(map[string]diskchain.Result)
	for _, path := range paths {
		want[path] = diskchain.Walk(t.Context(), path)
	}

	want[iso] = diskchain.Exists(t.Context(), iso)

	got := diskchain.WalkAll(t.Context(), paths, map[string]bool{iso: true})

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("WalkAll mismatch (-want +got):\n%s", diff)
	}
}

// A walk whose context is done gives no results.
func TestWalkAllWhenCanceled(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "a.qc2")
	dct.WriteFile(t, path, []byte("raw"))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if got := diskchain.WalkAll(ctx, []string{path}, nil); len(got) != 0 {
		t.Errorf("WalkAll() = %v, want no results", got)
	}

	if got := diskchain.WalkAll(t.Context(), nil, nil); len(got) != 0 {
		t.Errorf("WalkAll(nil) = %v, want no results", got)
	}
}
