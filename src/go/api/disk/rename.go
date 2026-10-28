package disk

import (
	"fmt"
	"os"
)

// linkRename renames src to dst by linking dst to src, which fails when dst
// exists, then removing src.
func linkRename(src, dst string) error {
	if err := os.Link(src, dst); err != nil {
		return fmt.Errorf("renaming %s: %w", src, err)
	}

	if err := os.Remove(src); err != nil {
		return fmt.Errorf("renaming %s: %w", src, err)
	}

	return nil
}
