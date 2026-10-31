package diskchain_test

import (
	"bytes"
	"fmt"
	"os"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"pgregory.net/rapid"

	"phenix/util/diskchain"
	dct "phenix/util/diskchain/diskchaintest"
)

// propertiesImage is one file of a random layout: missing, a raw image, or a qcow2 or qcow version 1 image backed by
// another file of the layout, named by its absolute or relative path.
type propertiesImage struct {
	missing  bool
	backing  int // -1 for a raw image
	relative bool
	v1       bool
}

// propertiesLayout draws a layout of up to six files, f0 to f5, whose backing edges may point anywhere, so it holds
// missing files, loops and long chains.
func propertiesLayout(t *rapid.T) []propertiesImage {
	n := rapid.IntRange(1, 6).Draw(t, "files")
	images := make([]propertiesImage, n)

	for i := range images {
		images[i] = propertiesImage{
			missing:  rapid.IntRange(0, 4).Draw(t, fmt.Sprintf("missing %d", i)) == 0,
			backing:  rapid.IntRange(-1, n-1).Draw(t, fmt.Sprintf("backing %d", i)),
			relative: rapid.Bool().Draw(t, fmt.Sprintf("relative %d", i)),
			v1:       rapid.Bool().Draw(t, fmt.Sprintf("v1 %d", i)),
		}
	}

	return images
}

// propertiesModel walks the layout as qemu would, from f0.
func propertiesModel(dir string, images []propertiesImage) diskchain.Result {
	var (
		res  diskchain.Result
		seen = map[int]bool{}
	)

	for i := 0; ; {
		path := fmt.Sprintf("%s/f%d", dir, i)

		switch {
		case images[i].missing:
			res.Problem, res.At = diskchain.Missing, path

			return res
		case seen[i]:
			res.Problem, res.At = diskchain.Loop, path

			return res
		}

		seen[i] = true
		res.Members = append(res.Members, path)

		if images[i].backing < 0 {
			return res
		}

		if images[i].v1 {
			res.Notes = append(res.Notes, diskchain.Note{Path: path, Kind: diskchain.QcowV1})
		}

		i = images[i].backing
	}
}

// Walk agrees with a model walk of any layout of missing files, loops and chains of qcow2 and qcow version 1 images.
func TestWalkMatchesModel(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(rt *rapid.T) {
		images := propertiesLayout(rt)
		dir := t.TempDir()

		for i, image := range images {
			path := fmt.Sprintf("%s/f%d", dir, i)

			backing := fmt.Sprintf("%s/f%d", dir, image.backing)
			if image.relative {
				backing = fmt.Sprintf("f%d", image.backing)
			}

			switch {
			case image.missing:
			case image.backing < 0:
				dct.WriteFile(t, path, []byte("raw"))
			case image.v1:
				dct.WriteQcow(t, path, dct.Qcow{Backing: backing})
			default:
				dct.WriteQcow2(t, path, dct.Qcow2{Backing: backing})
			}
		}

		got := diskchain.Walk(t.Context(), dir+"/f0")
		if diff := cmp.Diff(propertiesModel(dir, images), got, cmpopts.EquateEmpty()); diff != "" {
			rt.Fatalf("Walk mismatch (-model +got):\n%s", diff)
		}

		_ = os.RemoveAll(dir)
	})
}

// Check finds at any qcow2 image what Walk finds there: the notes about the image, and the problem when the walk
// stops at it rather than at its missing backing file.
func TestCheckMatchesWalk(t *testing.T) {
	t.Parallel()

	path := t.TempDir() + "/top.qc2"

	rapid.Check(t, func(rt *rapid.T) {
		q := dct.Qcow2{
			Version:    rapid.SampledFrom([]int{2, 3}).Draw(rt, "version"),
			Backing:    rapid.SampledFrom([]string{"", "missing.qc2"}).Draw(rt, "backing"),
			Encryption: uint32(rapid.IntRange(0, 3).Draw(rt, "encryption")),
			Features:   rapid.Uint64Range(0, 1<<7-1).Draw(rt, "features"),
			Zstd:       rapid.Bool().Draw(rt, "zstd"),
		}
		data := q.Bytes()
		dct.WriteFile(t, path, data)

		walk := diskchain.Walk(t.Context(), path)
		want := diskchain.Result{Members: []string{path}, Notes: walk.Notes}

		if walk.At == path {
			want.Problem, want.At, want.Detail = walk.Problem, walk.At, walk.Detail
		}

		h, err := diskchain.ParseHeader(bytes.NewReader(data))
		if diff := cmp.Diff(want, diskchain.Check(path, h, err), cmpopts.EquateEmpty()); diff != "" {
			rt.Fatalf("Check(%+v) mismatch with Walk (-want +got):\n%s", q, diff)
		}
	})
}

// ParseHeader never panics, and what it parses is a header qemu could parse: random bytes, often after a qcow2 magic
// or a seed header.
func TestParseHeaderProperties(t *testing.T) {
	t.Parallel()

	seeds := headerSeeds()

	rapid.Check(t, func(t *rapid.T) {
		data := rapid.SliceOfN(rapid.Byte(), 0, 512).Draw(t, "data")

		switch rapid.IntRange(0, 2).Draw(t, "prefix") {
		case 1:
			data = append([]byte("QFI\xfb"), data...)
		case 2:
			seed := rapid.SampledFrom(seeds).Draw(t, "seed")
			at := rapid.IntRange(0, len(seed)).Draw(t, "at")
			data = append(append([]byte{}, seed[:at]...), data...)
		}

		h, err := diskchain.ParseHeader(bytes.NewReader(data))
		if problem := checkHeader(data, h, err); problem != nil {
			t.Fatalf("ParseHeader(%q): %v", data, problem)
		}
	})
}
