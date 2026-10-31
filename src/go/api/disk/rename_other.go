//go:build !linux

package disk

// renameNoReplace renames src to dst unless dst exists.
func renameNoReplace(src, dst string) error {
	return linkRename(src, dst)
}
