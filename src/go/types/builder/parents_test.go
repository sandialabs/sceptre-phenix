package builder_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

	"phenix/types/builder"
)

// withGroups adds a group for each entry of parents to doc. An entry is the
// index of the group's parent among them, or -1 for none.
func withGroups(doc *builder.Document, parents []int) {
	ids := make([]string, len(parents))
	for i := range parents {
		ids[i] = builder.LegacyNodeID(builder.NodeKindGroup, "chain-"+strconv.Itoa(i))
	}

	for i, parent := range parents {
		node := builder.Node{
			ID:       ids[i],
			Kind:     builder.NodeKindGroup,
			Position: builder.Position{X: float64(i), Y: 0},
			Group:    &builder.Group{Title: "group " + strconv.Itoa(i)},
		}

		if parent >= 0 {
			node.ParentID = ids[parent]
		}

		doc.Nodes = append(doc.Nodes, node)
	}
}

// cyclePaths returns the paths of the group cycle issues of err.
func cyclePaths(err error) []string {
	var validationErr *builder.ValidationError
	if !errors.As(err, &validationErr) {
		return nil
	}

	var paths []string

	for _, issue := range validationErr.Issues {
		if issue.Code == builder.CodeNodeParentCycle {
			paths = append(paths, issue.Path)
		}
	}

	slices.Sort(paths)

	return paths
}

// walkedCyclePaths returns the paths of the groups (added after first nodes)
// whose chain of parents comes back to a group, by a walk from each group.
func walkedCyclePaths(first int, parents []int) []string {
	var paths []string

	for i, parent := range parents {
		if parent < 0 || parent == i {
			continue
		}

		seen := map[int]bool{i: true}

		for current := parent; current >= 0; current = parents[current] {
			if seen[current] {
				paths = append(paths, fmt.Sprintf("nodes[%d].parentId", first+i))

				break
			}

			seen[current] = true
		}
	}

	slices.Sort(paths)

	return paths
}

func TestValidateParentCycleMatchesWalk(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "document.json"))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}

	rapid.Check(t, func(rt *rapid.T) {
		count := rapid.IntRange(1, 12).Draw(rt, "count")
		parents := rapid.SliceOfN(rapid.IntRange(-1, count-1), count, count).Draw(rt, "parents")

		doc, err := builder.Decode(data)
		if err != nil {
			rt.Fatalf("Decode(document.json) = %v", err)
		}

		first := len(doc.Nodes)
		withGroups(doc, parents)

		got := cyclePaths(doc.Validate())
		if diff := cmp.Diff(walkedCyclePaths(first, parents), got); diff != "" {
			rt.Errorf("Validate() of groups with parents %v: cycle issues mismatch (-want +got):\n%s", parents, diff)
		}
	})
}

// TestValidateLongChainOfGroups checks that the parent check walks each group
// one time only. A walk from each group takes more than a minute here.
func TestValidateLongChainOfGroups(t *testing.T) {
	const count = 50000

	doc := loadDocumentFixture(t, "document.json")

	parents := make([]int, count)
	for i := range parents {
		parents[i] = i - 1
	}

	withGroups(doc, parents)

	start := time.Now()

	if err := doc.Validate(); err != nil {
		t.Errorf("Validate() of %d nested groups = %v, want nil", count, err)
	}

	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Errorf("Validate() of %d nested groups took %v, want at most 20s", count, elapsed)
	}
}
