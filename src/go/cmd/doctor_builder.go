package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

const (
	builderTemplatesKey = "base-dir.builder-templates"

	// builderSkipsPhrase starts the detail of a row that names template files
	// phenix ui skips, which the hint looks for.
	builderSkipsPhrase = "phenix ui skips "
)

// builderTemplatesCheck checks the directory of Builder template files that phenix ui reads, and never makes, at
// start. A missing directory has no templates, so it passes when base-dir.builder-templates is not set. Each problem
// only warns, because phenix ui starts without the templates. The row names the files phenix ui skips, never why:
// the reason can quote the content of a file.
func builderTemplatesCheck(in *doctorInput) doctorCheck {
	value := in.text(builderTemplatesKey)
	dir := derivedBaseDir(value, in.text("base-dir.phenix"), builderTemplatesSubdir)

	info, err := in.env.stat(dir)

	switch {
	case errors.Is(err, os.ErrNotExist) && value == "":
		return passCheck(dir + " does not exist, so the Builder has no server templates")
	case errors.Is(err, os.ErrNotExist):
		return warnCheck(dir + " does not exist, so phenix ui reads no template files")
	case err != nil:
		return warnCheck(err.Error() + ", so phenix ui reads no template files")
	case !info.IsDir():
		return warnCheck(dir + " is not a directory, so phenix ui reads no template files")
	case in.env.access(dir, unix.R_OK|unix.X_OK) != nil:
		return warnCheck(dir + " is not readable and searchable by this user, so phenix ui reads no template files")
	case !filepath.IsAbs(dir):
		return warnCheck(dir + " is relative, so it depends on the working directory of phenix ui")
	}

	found, err := in.templates(dir)
	if err != nil {
		return warnCheck(err.Error() + ", so phenix ui reads no template files")
	}

	if len(found.Problems) == 0 {
		return passCheck(dir + ", with " + plural(len(found.Collections), "template file", "template files"))
	}

	files := make([]string, 0, len(found.Problems))
	for _, problem := range found.Problems {
		files = append(files, problem.File)
	}

	return warnCheck(builderSkipsPhrase + plural(len(files), "template file", "template files") + " in " + dir + ": " +
		shortList(files))
}

// builderTemplatesHint is what to do about a base-dir.builder-templates row
// that warns.
func builderTemplatesHint(c doctorCheck) string {
	if strings.HasPrefix(c.Detail, builderSkipsPhrase) {
		return "fix or remove each file the row names; at start, phenix ui logs why it skips each file"
	}

	return "make " + builderTemplatesKey + " an absolute path to a directory that the user of phenix ui can read, " +
		"or unset it to use <base-dir.phenix>/" + builderTemplatesSubdir
}
