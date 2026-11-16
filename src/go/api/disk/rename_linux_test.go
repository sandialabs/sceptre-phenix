package disk

import (
	"fmt"
	"testing"

	"golang.org/x/sys/unix"
)

// Only the errors that tell that RENAME_NOREPLACE is not supported make
// renameNoReplace link and remove instead.
func TestNoReplaceUnsupported(t *testing.T) {
	t.Parallel()

	for err, want := range map[error]bool{
		unix.EINVAL:     true,
		unix.ENOSYS:     true,
		unix.EOPNOTSUPP: true,
		fmt.Errorf("renameat2: %w", unix.EOPNOTSUPP): true,
		unix.EEXIST: false,
		unix.EXDEV:  false,
		unix.EACCES: false,
	} {
		if got := noReplaceUnsupported(err); got != want {
			t.Errorf("noReplaceUnsupported(%v) = %t, want %t", err, got, want)
		}
	}
}
