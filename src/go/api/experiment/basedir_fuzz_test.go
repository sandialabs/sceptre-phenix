package experiment

import (
	"strings"
	"testing"
)

// FuzzCheckBaseDir checks checkBaseDir against the folder that mkdir makes for
// each path below the world folder of newBaseDirWorld (see
// checkBaseDirAgainstMkdir).
func FuzzCheckBaseDir(f *testing.F) {
	for _, seed := range []string{
		"phenix/experiments/exp", "phenix/experiments/new/sub", "phenix/experiments/..x",
		"phenix/experiments", "phenix/experiments/", "phenix/experiments/exp/..",
		"phenix/experiments/out/x", "phenix/experiments/rel-out/x", "phenix/experiments/out/../x",
		"phenix/experiments/dangling/x", "phenix/experiments/loop/x", "phenix/experiments/file/x",
		"outside/in/x", "outside/in/..", "rootlink/x", "phenix/experiments-other/x", "..", "",
	} {
		f.Add(seed)
	}

	w := newBaseDirWorld(f)

	f.Fuzz(func(t *testing.T, rel string) {
		parts := strings.Split(rel, "/")
		if len(rel) > 256 || len(parts) > 32 || dotDots(parts) > 8 {
			t.Skip("the path could leave the world folder")
		}

		for _, root := range []string{w.root, w.top + "/rootlink"} {
			if msg, ok := checkBaseDirAgainstMkdir(w.top+"/"+rel, root); !ok {
				t.Fatal(msg)
			}
		}
	})
}

// dotDots counts the ".." parts.
func dotDots(parts []string) int {
	n := 0

	for _, part := range parts {
		if part == ".." {
			n++
		}
	}

	return n
}
