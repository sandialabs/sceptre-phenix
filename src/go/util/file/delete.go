package file

import (
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"unicode"

	"github.com/hashicorp/go-multierror"

	"phenix/util/mm/mmcli"
)

// existingFilesDeleter is implemented by ClusterFiles that can find where
// files are before deleting them (see DeleteExistingFiles).
type existingFilesDeleter interface {
	DeleteExistingFiles(names []string) error
}

// DeleteExistingFiles deletes each of the named files (paths relative to
// minimega's files directory, matched exactly, never as globs) from every mesh
// node and the headnode, as DeleteFile would for each. When DefaultClusterFiles
// can tell where the files are, only the copies that exist are deleted, each
// on the node that has it.
func DeleteExistingFiles(names []string) error {
	if len(names) == 0 {
		return nil
	}

	defer invalidateAllExperimentFiles()

	if d, ok := DefaultClusterFiles.(existingFilesDeleter); ok {
		return d.DeleteExistingFiles(names) //nolint:wrapcheck // passthrough
	}

	for _, name := range names {
		err := DefaultClusterFiles.DeleteFile(name)
		if err != nil {
			return fmt.Errorf("deleting file %s: %w", name, err)
		}
	}

	return nil
}

// DeleteExistingFiles lists the directories the named files are in, with one
// `mesh send all file list` and one local `file list` per directory, and
// deletes only the copies found: `mesh send <nodes> file delete` for the mesh
// nodes that have a file, then `file delete` if the headnode has it. Deleting
// a file with DeleteFile takes two commands (the first a mesh round trip to
// every node) whether or not it exists anywhere, which for every VM's snapshot
// on every experiment start and stop would be most of the work.
//
// The listing is narrowed with a glob of the names' common prefix, and only
// names that match exactly are deleted, so experiment "foo" never deletes
// "foo_bar"'s files. If a directory cannot be listed on every node, its files
// are deleted everywhere, as DeleteFile does. The names are meant to be files:
// a directory is found (and deleted) only if something is in it.
func (MMClusterFiles) DeleteExistingFiles(names []string) error {
	byDir := make(map[string][]string)

	var dirs []string

	for _, name := range names {
		clean := strings.TrimPrefix(path.Clean("/"+name), "/")
		dir := path.Dir(clean)

		if _, ok := byDir[dir]; !ok {
			dirs = append(dirs, dir)
		}

		byDir[dir] = append(byDir[dir], clean)
	}

	for _, dir := range dirs {
		err := deleteExistingInDir(dir, byDir[dir])
		if err != nil {
			return err
		}
	}

	return nil
}

func deleteExistingInDir(dir string, names []string) error {
	pattern, ok := listingPattern(dir, names)
	if !ok {
		return deleteEverywhere(names)
	}

	remote, err := listFileNames("mesh send all file list " + pattern)
	if err != nil {
		return deleteEverywhere(names)
	}

	local, err := listFileNames("file list " + pattern)
	if err != nil {
		return deleteEverywhere(names)
	}

	cmd := mmcli.NewCommand()

	for _, name := range names {
		var hosts []string

		for host, listed := range remote {
			if present(listed, name) {
				hosts = append(hosts, host)
			}
		}

		// First delete file from mesh, then from headnode.
		if len(hosts) > 0 {
			slices.Sort(hosts)

			cmd.Command = fmt.Sprintf("mesh send %s file delete %s", strings.Join(hosts, ","), name)

			err := mmcli.ErrorResponse(mmcli.Run(cmd))
			if err != nil {
				return fmt.Errorf("deleting file %s from cluster nodes: %w", name, err)
			}
		}

		if present(local[""], name) {
			cmd.Command = "file delete " + name

			err := mmcli.ErrorResponse(mmcli.Run(cmd))
			if err != nil {
				return fmt.Errorf("deleting file %s from headnode: %w", name, err)
			}
		}
	}

	return nil
}

// deleteEverywhere deletes each file the way DeleteFile does.
func deleteEverywhere(names []string) error {
	for _, name := range names {
		err := MMClusterFiles{}.DeleteFile(name)
		if err != nil {
			return fmt.Errorf("deleting file %s: %w", name, err)
		}
	}

	return nil
}

// globMeta are the characters the listing pattern leaves out of the names'
// common prefix: glob syntax (minimega expands `file` paths as globs) and
// anything minimega's command line would split, unquote, or read as the start
// of a comment.
const globMeta = "*?[]\\{}\"'`# \t\r\n"

// CommandSafe reports whether p can be passed to a minimega `file` command
// (directly or through `mesh send`) as one literal path: it has no glob syntax
// and nothing minimega's command line would split (any Unicode space),
// unquote, or drop as a comment. Deleting a path that is not would delete
// other files.
func CommandSafe(p string) bool {
	return !strings.ContainsAny(p, globMeta) &&
		!strings.ContainsFunc(p, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) })
}

// listingPattern returns the `file list` argument listing (at least) the named
// files in dir: a glob of their longest common prefix, or dir itself. It
// returns false if dir cannot be listed safely.
func listingPattern(dir string, names []string) (string, bool) {
	if strings.ContainsAny(dir, globMeta) {
		return "", false
	}

	prefix := path.Base(names[0])

	for _, name := range names[1:] {
		base := path.Base(name)

		n := 0
		for n < len(prefix) && n < len(base) && prefix[n] == base[n] {
			n++
		}

		prefix = prefix[:n]
	}

	if i := strings.IndexAny(prefix, globMeta); i >= 0 {
		prefix = prefix[:i]
	}

	if dir == "." {
		dir = ""
	}

	if prefix == "" {
		return "/" + dir, true
	}

	return "/" + path.Join(dir, prefix) + "*", true
}

// present reports whether a listing shows name: the file itself, or something
// in it if it is a directory.
func present(listed map[string]bool, name string) bool {
	if listed[name] {
		return true
	}

	for rel := range listed {
		if strings.HasPrefix(rel, name+"/") {
			return true
		}
	}

	return false
}

// listFileNames runs a `file list` (possibly through `mesh send`) and returns
// the names listed, by the node that listed them. For a local `file list` the
// node is "". Any error, from any node, fails the whole listing: a node that
// did not answer may have the files.
func listFileNames(command string) (map[string]map[string]bool, error) {
	cmd := mmcli.NewCommand()
	cmd.Command = command

	local := !strings.HasPrefix(command, "mesh send ")

	var (
		listed = make(map[string]map[string]bool)
		errs   error
	)

	for resps := range mmcli.Run(cmd) {
		for _, resp := range resps.Resp {
			if resp.Error != "" {
				errs = multierror.Append(errs, fmt.Errorf("%s: %s", resp.Host, resp.Error))

				continue
			}

			host := resp.Host
			if local {
				host = ""
			}

			if listed[host] == nil {
				listed[host] = make(map[string]bool)
			}

			column := slices.Index(resp.Header, "name")
			if column < 0 && len(resp.Tabular) > 0 {
				errs = multierror.Append(errs, fmt.Errorf("%s: %w", resp.Host, errNoNameColumn))

				continue
			}

			for _, row := range resp.Tabular {
				if column < len(row) {
					listed[host][row[column]] = true
				}
			}
		}
	}

	if errs != nil {
		return nil, fmt.Errorf("running %s: %w", command, errs)
	}

	return listed, nil
}

var errNoNameColumn = errors.New("file listing has no name column")
