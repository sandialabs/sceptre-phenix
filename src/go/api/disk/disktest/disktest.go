// Package disktest runs a fake qemu-img for tests of the disk listing. It is
// imported only by tests, as net/http/httptest is.
package disktest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeQemuImg reports each image by the path it was given, as qemu-img does:
// a qcow2 when its name ends in .qc2 or .qcow2, and raw otherwise. An image
// with a sidecar <image>.backing file is backed by the image that file names,
// relative to the image's folder or absolute, which the chain reports joined
// onto that folder without cleaning, as qemu-img does. The chain of an image
// named broken.* cannot be read, nor can a chain naming a missing file.
const fakeQemuImg = `#!/bin/sh
echo "$*" >> "$FAKE_QEMU_IMG_CALLS"
chain=false
for arg; do
	[ "$arg" = --backing-chain ] && chain=true
	image=$arg
done
info() {
	[ -e "$1" ] || { echo "could not open $1" >&2; exit 1; }
	case $1 in
	*.qc2|*.qcow2) format=qcow2 ;;
	*) format=raw ;;
	esac
	printf '{"filename":"%s","format":"%s","virtual-size":1073741824,"actual-size":2048}' "$1" "$format"
}
if [ "$chain" = false ]; then
	info "$image"
	echo
	exit
fi
case ${image##*/} in
broken.*) echo "could not open backing file" >&2; exit 1 ;;
esac
out=[$(info "$image") || exit 1
while [ -f "$image.backing" ]; do
	read -r backing < "$image.backing"
	case $backing in
	/*) image=$backing ;;
	*) image=${image%/*}/$backing ;;
	esac
	out=$out,$(info "$image") || exit 1
done
echo "$out]"
`

// UseFakeQemuImg puts first on PATH, until the test ends, a qemu-img that
// reports images as fakeQemuImg describes, and returns a function counting
// the times it has run. Callers clear the disk package's cache themselves.
func UseFakeQemuImg(tb testing.TB) func() int {
	tb.Helper()

	dir := tb.TempDir()
	calls := filepath.Join(dir, "calls")

	if err := os.WriteFile(filepath.Join(dir, "qemu-img"), []byte(fakeQemuImg), 0o700); err != nil {
		tb.Fatal(err)
	}

	tb.Setenv("FAKE_QEMU_IMG_CALLS", calls)
	tb.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	return func() int {
		log, _ := os.ReadFile(calls)

		return strings.Count(string(log), "\n")
	}
}
