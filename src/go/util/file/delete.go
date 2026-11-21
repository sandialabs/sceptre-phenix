package file

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
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

// numberedFilesDeleter is implemented by ClusterFiles that can find the
// numbered files DeleteNumberedFiles deletes.
type numberedFilesDeleter interface {
	DeleteNumberedFiles(names, prefixes []string) error
}

// DeleteNumberedFiles deletes the named files as DeleteExistingFiles does. When DefaultClusterFiles can tell where files
// are, it also deletes each existing file named one of prefixes, "_", and one or more ASCII digits. It ignores a prefix
// that is not CommandSafe.
func DeleteNumberedFiles(names, prefixes []string) error {
	d, ok := DefaultClusterFiles.(numberedFilesDeleter)
	if !ok {
		return DeleteExistingFiles(names)
	}

	defer invalidateAllExperimentFiles()

	return d.DeleteNumberedFiles(names, prefixes) //nolint:wrapcheck // passthrough
}

// DeleteExistingFiles deletes only the copies of the named files that exist, after listing their directories on the mesh
// and the headnode, rather than sending DeleteFile's two commands per file. Only exact name matches are deleted, so "foo"
// never deletes "foo_bar"'s files; a directory that cannot be listed everywhere has its files deleted everywhere.
func (MMClusterFiles) DeleteExistingFiles(names []string) error {
	return deleteExisting(names, nil)
}

// DeleteNumberedFiles is DeleteExistingFiles, also deleting the listed files named a prefix, "_", and digits. A
// directory that cannot be listed everywhere has only the named files deleted everywhere.
func (MMClusterFiles) DeleteNumberedFiles(names, prefixes []string) error {
	return deleteExisting(names, slices.DeleteFunc(slices.Clone(prefixes), func(p string) bool { return !CommandSafe(p) }))
}

func deleteExisting(names, prefixes []string) error {
	type group struct{ names, prefixes []string }

	byDir := make(map[string]*group)

	var dirs []string

	add := func(name string, numbered bool) {
		clean := strings.TrimPrefix(path.Clean("/"+name), "/")
		dir := path.Dir(clean)

		if _, ok := byDir[dir]; !ok {
			dirs = append(dirs, dir)
			byDir[dir] = new(group)
		}

		if numbered {
			byDir[dir].prefixes = append(byDir[dir].prefixes, clean)
		} else {
			byDir[dir].names = append(byDir[dir].names, clean)
		}
	}

	for _, name := range names {
		add(name, false)
	}

	for _, prefix := range prefixes {
		add(prefix, true)
	}

	for _, dir := range dirs {
		err := deleteExistingInDir(dir, byDir[dir].names, byDir[dir].prefixes)
		if err != nil {
			return err
		}
	}

	return nil
}

func deleteExistingInDir(dir string, names, prefixes []string) error {
	pattern, ok := listingPattern(dir, slices.Concat(names, prefixes))
	if !ok {
		return deleteEverywhere(names)
	}

	if err := mmcli.SafeArg(pattern); err != nil {
		return fmt.Errorf("listing the files to delete in %s: %w", dir, err)
	}

	remote, err := listFileNames("mesh send all file list " + pattern)
	if err != nil {
		return deleteEverywhere(names)
	}

	local, err := listFileNames("file list " + pattern)
	if err != nil {
		return deleteEverywhere(names)
	}

	names = append(slices.Clip(names), numberedNames(prefixes, names, remote, local)...)

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
		err := new(MMClusterFiles).DeleteFile(name)
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

	if dir != "" {
		dir += "/"
	}

	// joined by hand: path.Join would clean away a prefix of "." or ".."
	return "/" + dir + prefix + "*", true
}

// numberedNames returns, sorted, the names in the listings that are one of prefixes, "_", and one or more ASCII digits,
// leaving out those in names.
func numberedNames(prefixes, names []string, listings ...map[string]map[string]bool) []string {
	if len(prefixes) == 0 {
		return nil
	}

	quoted := make([]string, len(prefixes))
	for i, prefix := range prefixes {
		quoted[i] = regexp.QuoteMeta(prefix)
	}

	numbered := regexp.MustCompile(`^(?:` + strings.Join(quoted, "|") + `)_[0-9]+$`)

	var found []string

	for _, listing := range listings {
		for _, listed := range listing {
			for name := range listed {
				if numbered.MatchString(name) && !slices.Contains(names, name) && !slices.Contains(found, name) {
					found = append(found, name)
				}
			}
		}
	}

	slices.Sort(found)

	return found
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

	line := mmcli.MeshRead
	if local {
		line = mmcli.HeadRead
	}

	var (
		listed = make(map[string]map[string]bool)
		errs   error
	)

	for resps := range mmcli.RunReadOnly(context.Background(), line, cmd) {
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
