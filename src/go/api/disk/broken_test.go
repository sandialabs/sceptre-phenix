package disk

import (
	"testing"

	"phenix/util/diskchain/diskchaintest"
)

// writeOverlay writes a qcow2 header at path backed by backing, for the walker, and the sidecar the fake qemu-img
// follows; a sidecar of "" leaves it out.
func writeOverlay(t *testing.T, path, backing, sidecar string) {
	t.Helper()

	diskchaintest.WriteQcow2(t, path, diskchaintest.Qcow2{Backing: backing})

	if sidecar != "" {
		writeFile(t, path+".backing", sidecar)
	}
}

func getImage(t *testing.T, path string) Details {
	t.Helper()

	got, err := GetImage(path)
	if err != nil {
		t.Fatalf("GetImage(%s) error = %v", path, err)
	}

	return got
}
