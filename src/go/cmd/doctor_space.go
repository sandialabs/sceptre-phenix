package cmd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"

	"phenix/web/health"
)

const (
	diskSpaceName      = "disk-space"
	diskSpaceInjectRow = "disk-space inject dirs"

	// A filesystem has too little free space below the larger of these.
	minFreeBytes   = 5 << 30
	minFreePercent = 10

	// minimega makes the mount point of each disk inject in its -base with [os.MkdirTemp](base, "dstImg").
	injectDirPrefix = "dstImg"

	// Phrases in the details of rows, which their hints look for.
	lowSpacePhrase   = "which is less than"
	minimegaLogLabel = "the minimega log"
	stdoutLogPhrase  = "so it logs to its standard output"
)

// diskSpacePlace is a directory or file whose filesystem disk-space measures.
type diskSpacePlace struct {
	row, path, label string
	dir              string // the directory to measure: path, or the directory of the log file
}

// diskSpaceFS is one filesystem that disk-space measures, with the places on it.
type diskSpaceFS struct {
	stat   health.DiskStat
	places []diskSpacePlace
}

// diskSpaceChecks measures the free space of the filesystems of minimega's files directory, its base directory and its
// log file, each once, and warns about the leftover mount points of minimega disk inject. It never fails.
func diskSpaceChecks(ctx context.Context, in *doctorInput) []doctorCheck {
	args := in.found.minimegaArgs

	base := in.text("base-dir.minimega")
	if b := args["base"]; filepath.IsAbs(b) {
		base = filepath.Clean(b)
	}

	places := []diskSpacePlace{
		{row: "disk-space files", path: in.imagesDir(), label: "the images directory", dir: in.imagesDir()},
		{row: "disk-space base", path: base, label: "minimega's -base", dir: base},
	}

	logfile := args["logfile"]
	if filepath.IsAbs(logfile) {
		places = append(places, diskSpacePlace{
			row: "disk-space log", path: logfile, label: minimegaLogLabel, dir: filepath.Dir(logfile),
		})
	}

	filesystems, notes := in.measurePlaces(ctx, places)
	rows := make([]doctorCheck, 0, len(filesystems)+1)

	for _, f := range filesystems {
		rows = append(rows, in.diskSpaceRow(f, args != nil && logfile == ""))
	}

	if len(rows) == 0 {
		rows = append(rows, hostSkip(strings.Join(notes, ". ")))
		rows[0].Name = "disk-space files"
	} else if len(notes) > 0 {
		rows[0].Detail += ". " + capitalize(strings.Join(notes, ". "))
	}

	return append(rows, injectDirsCheck(in, base))
}

// measurePlaces groups places by filesystem, measuring each with statfs for at most the doctor timeout. It returns a
// note for each place it cannot measure.
func (in *doctorInput) measurePlaces(ctx context.Context, places []diskSpacePlace) ([]*diskSpaceFS, []string) {
	var (
		filesystems []*diskSpaceFS
		notes       []string
	)

	for _, p := range places {
		stat, err := in.boundedStatfs(ctx, p.dir)

		switch {
		case errors.Is(err, errNotADir):
			// the dirs, files and minimega-env checks report a directory that is not there
			continue
		case err != nil:
			notes = append(notes, fmt.Sprintf("%s %s is not measured: %v", p.label, p.path, err))

			continue
		}

		found := false

		for _, f := range filesystems {
			if f.stat.Device == stat.Device {
				f.places, found = append(f.places, p), true

				break
			}
		}

		if !found {
			filesystems = append(filesystems, &diskSpaceFS{stat: stat, places: []diskSpacePlace{p}})
		}
	}

	return filesystems, notes
}

// errNotADir is the error of boundedStatfs for a path that is not a directory, or that it cannot stat.
var errNotADir = errors.New("not a directory")

// boundedStatfs measures the filesystem of the directory path, and gives up after the doctor timeout, as on a hung
// network mount. The stat of path is in the same bound, because it can also hang.
func (in *doctorInput) boundedStatfs(ctx context.Context, path string) (health.DiskStat, error) {
	type measured struct {
		stat health.DiskStat
		err  error
	}

	ctx, cancel := context.WithTimeout(ctx, in.timeout)
	defer cancel()

	result := make(chan measured, 1)

	go func() {
		if info, err := in.env.stat(path); err != nil || !info.IsDir() {
			result <- measured{stat: health.DiskStat{Device: 0, Total: 0, Avail: 0}, err: errNotADir}

			return
		}

		stat, err := in.statfs(path)
		result <- measured{stat: stat, err: err}
	}()

	select {
	case m := <-result:
		return m.stat, m.err
	case <-ctx.Done():
		return health.DiskStat{}, fmt.Errorf("the filesystem did not answer within %s", in.timeout) //nolint:err113 // one message
	}
}

// diskSpaceRow reports the free space of f, and warns when it is less than the larger of minFreePercent and
// minFreeBytes. noLogfile is true when minimega gives no -logfile.
func (in *doctorInput) diskSpaceRow(f *diskSpaceFS, noLogfile bool) doctorCheck {
	names := make([]string, 0, len(f.places))
	for _, p := range f.places {
		names = append(names, fmt.Sprintf("%s (%s)", p.path, p.label))
	}

	var percent float64
	if f.stat.Total > 0 {
		percent = float64(f.stat.Avail) * 100 / float64(f.stat.Total) //nolint:mnd // a percentage
	}

	detail := fmt.Sprintf("%s: %s free of %s (%.1f%%)", strings.Join(names, " and "),
		doctorBytes(int64(f.stat.Avail)), doctorBytes(int64(f.stat.Total)), percent) //nolint:gosec // G115 sizes fit

	threshold := max(f.stat.Total/(100/minFreePercent), minFreeBytes) //nolint:mnd // a percentage
	low := f.stat.Avail < threshold

	if low {
		detail += fmt.Sprintf(", %s %s", lowSpacePhrase, doctorBytes(int64(threshold))) //nolint:gosec // G115 sizes fit
	}

	if i := slices.IndexFunc(f.places, func(p diskSpacePlace) bool { return p.label == minimegaLogLabel }); i >= 0 {
		if info, err := in.env.stat(f.places[i].path); err == nil {
			detail += fmt.Sprintf(". %s %s is %s", capitalize(minimegaLogLabel), f.places[i].path, doctorBytes(info.Size()))
		}
	}

	if !low {
		check := passCheck(detail)
		check.Name = f.places[0].row

		return check
	}

	switch found := in.found.leftovers; {
	case found == nil:
		detail += ". The leftovers check did not run, so it found no files that are safe to delete"
	case found.items > 0:
		detail += fmt.Sprintf(". The leftovers rows name %d snapshot files and directories of deleted experiments, "+
			"%s in all, that are safe to delete", found.items, doctorBytes(found.bytes))
	}

	if noLogfile {
		detail += ". minimega gives no -logfile, " + stdoutLogPhrase
	}

	check := warnCheck(detail)
	check.Name = f.places[0].row

	return check
}

// injectDirsCheck warns about the dstImg* directories in base, minimega's -base, which a disk inject that fails or
// stops can leave, and names those that are still mounted.
func injectDirsCheck(in *doctorInput, base string) doctorCheck {
	entries, err := in.env.readDir(base)

	var check doctorCheck

	switch {
	case errors.Is(err, fs.ErrNotExist):
		check = hostSkip(base + " does not exist")
	case err != nil:
		check = hostSkip(fmt.Sprintf("this user cannot list %s: %v", base, unwrapPathError(err)))
	default:
		check = injectDirsRow(in, base, entries)
	}

	check.Name = diskSpaceInjectRow

	return check
}

func injectDirsRow(in *doctorInput, base string, entries []fs.DirEntry) doctorCheck {
	mounts := procMounts(in.env)

	var items []string

	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), injectDirPrefix) {
			continue
		}

		dir := filepath.Join(base, entry.Name())
		item := dir

		for _, m := range mounts {
			if filepath.Clean(m[1]) == dir {
				item = fmt.Sprintf("%s (mounted from %s)", dir, m[0])
			}
		}

		items = append(items, item)
	}

	if len(items) == 0 {
		return passCheck(fmt.Sprintf("no %s* directories in %s", injectDirPrefix, base))
	}

	return warnCheck(fmt.Sprintf("minimega disk inject directories in %s, which an inject that fails can leave, "+
		"if no inject runs now: %s", base, shortList(items)))
}

// diskSpaceHint is what to do about a disk-space row that warns.
func diskSpaceHint(c doctorCheck) string {
	switch {
	case c.Status == doctorSkip:
		return ""
	case c.Name == diskSpaceInjectRow:
		return "when no inject runs, unmount each mounted directory with umount <directory>, disconnect its nbd device " +
			"(see the nbd row), then remove the directory with rmdir <directory>"
	}

	hints := []string{"increase the free space: delete the images and files that you do not use, and the snapshot " +
		"files and directories that the leftovers rows name"}

	if strings.Contains(c.Detail, capitalize(minimegaLogLabel)) {
		hints = append(hints, "Delete or rotate the minimega log, such as with logrotate and its copytruncate option")
	}

	if strings.Contains(c.Detail, stdoutLogPhrase) {
		hints = append(hints, "When minimega runs in Docker, limit its log with the json-file options max-size and max-file")
	}

	return strings.Join(hints, ". ")
}
