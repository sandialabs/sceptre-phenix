package cmd

import (
	"fmt"
	"os"
	"path/filepath"
)

const experimentFilesName = "experiment-files"

// experimentFilesHint is what to do about an experiment-files row that warns.
func experimentFilesHint(c doctorCheck) string {
	if c.Status != doctorWarn {
		return ""
	}

	return "set minimega's -filepath to <base-dir.phenix>/images, " +
		"or set base-dir.phenix so that <base-dir.phenix>/images is minimega's -filepath"
}

// experimentFilesCheck compares <base-dir.phenix>/images, where phenix keeps the files of each experiment, with
// minimega's -filepath, as minimega-env read it, under which the Files tab lists them (file list). Two paths that
// differ pass when phenix finds them to be one directory, such as through a symlink.
func experimentFilesCheck(in *doctorInput) doctorCheck {
	switch {
	case in.found == nil || !in.found.minimegaUp:
		return hostSkip("minimega gave no version")
	case in.found.minimegaArgs == nil:
		return hostSkip("minimega-env did not read minimega's flags")
	}

	mmFiles := in.found.minimegaArgs["filepath"]
	if !filepath.IsAbs(mmFiles) {
		return hostSkip("minimega gives no absolute -filepath (see minimega-env)")
	}

	mmFiles = filepath.Clean(mmFiles)

	base := in.text("base-dir.phenix")
	if !filepath.IsAbs(base) && filepath.IsAbs(in.cwd) {
		base = filepath.Join(in.cwd, base)
	}

	images := filepath.Join(base, "images")

	if images == mmFiles {
		return passCheck(images + " is minimega's -filepath")
	}

	if in.sameDir(images, mmFiles) {
		return passCheck(fmt.Sprintf("%s is the directory of minimega's -filepath %s", images, mmFiles))
	}

	return warnCheck(fmt.Sprintf(
		"phenix keeps experiment files in %s (<base-dir.phenix>/images), but minimega's -filepath is %s. "+
			"The Files tab lists files from %s, so it does not show the files that phenix writes, such as VM screenshots",
		images, mmFiles, mmFiles,
	))
}

// sameDir reports whether a and b are one directory for phenix, which follows symlinks to find them.
func (in *doctorInput) sameDir(a, b string) bool {
	aInfo, err := in.env.stat(a)
	if err != nil || !aInfo.IsDir() {
		return false
	}

	bInfo, err := in.env.stat(b)
	if err != nil || !bInfo.IsDir() {
		return false
	}

	return os.SameFile(aInfo, bInfo)
}
