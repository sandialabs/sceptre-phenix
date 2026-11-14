package disk

import (
	"errors"
	"fmt"
	"os"
)

// linkRename renames src to dst by linking dst to src, which fails when dst
// exists, then removing src. If phenix cannot remove src, it removes dst
// again, so that the two names are as before.
func linkRename(src, dst string) error {
	if err := os.Link(src, dst); err != nil {
		return fmt.Errorf("renaming %s: %w", src, err)
	}

	if err := os.Remove(src); err != nil {
		if rbErr := os.Remove(dst); rbErr != nil {
			err = errors.Join(err, fmt.Errorf("removing link %s: %w", dst, rbErr))
		}

		return fmt.Errorf("renaming %s: %w", src, err)
	}

	return nil
}
