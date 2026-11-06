package disk

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"phenix/util/diskchain"
)

// IssueLevel is how serious a FormatIssue is.
type IssueLevel string

// The levels of a FormatIssue.
const (
	// IssueError is an image that qemu refuses to open, or to open read/write.
	IssueError IssueLevel = "error"
	// IssueWarning is an image of an old format, or of a format whose backing chain phenix does not check.
	IssueWarning IssueLevel = "warning"
)

// FormatIssue is why the format of an image needs care.
type FormatIssue struct {
	Level IssueLevel `json:"level"`
	// Problem says what is wrong in one sentence without a final period, with the image's full path.
	Problem string `json:"problem"`
	// Fix says what to do in one or two sentences without a final period.
	Fix string `json:"fix"`
	// Command is the qemu-img command of the fix, or "" when the fix needs more than one command.
	Command string `json:"command"`
}

// imageHeader is what the header of an image says about its format (see readHeader).
type imageHeader struct {
	header diskchain.Header
	check  diskchain.Result
	read   bool
}

// checkedFormats are the formats, as qemu-img names them, whose backing chain phenix checks; "" is a format not known.
var checkedFormats = []string{"", "qcow2", "qcow", "raw"} //nolint:gochecknoglobals // global constant

// qcowFormat reports whether format, as qemu-img names it, is qcow2 or qcow, whose images have a backing chain.
func qcowFormat(format string) bool {
	return format == string(diskchain.FormatQcow2) || format == string(diskchain.FormatQcow)
}

// shellSafe matches a path that a shell reads as one word without quotes.
var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_./+:@%=,-]+$`)

// readHeader reads the header of the image at file and checks it (see diskchain.Check), naming the image path in the
// result. Its read field is false when phenix cannot open or read the file.
func readHeader(file, path string) imageHeader {
	f, err := diskchain.OpenRegular(file)
	if err != nil {
		return imageHeader{}
	}

	h, err := diskchain.ParseHeader(f)
	_ = f.Close()

	if err != nil && !errors.Is(err, diskchain.ErrBadHeader) && !errors.Is(err, diskchain.ErrUnsupportedVersion) {
		return imageHeader{}
	}

	return imageHeader{header: h, check: diskchain.Check(path, h, err), read: true}
}

// setFormat records in d its format, as qemu-img or minimega reported it ("" when unknown), and what its header ih
// says.
func (d *Details) setFormat(format string, ih imageHeader) {
	d.Format = format
	if d.Format == "" && ih.read {
		d.Format = string(ih.header.Format)
	}

	if ih.read {
		d.FormatVersion = ih.header.Version
	}

	d.FormatIssue = formatIssue(d.FullPath, d.Format, ih)
}

// formatIssue returns the most serious issue of the image at path, of format, whose header is ih, or nil for none.
func formatIssue(path, format string, ih imageHeader) *FormatIssue {
	quoted := shellQuote(path)

	if ih.read {
		if issue := headerIssue(path, quoted, ih.check); issue != nil {
			return issue
		}
	}

	if !slices.Contains(checkedFormats, format) {
		return uncheckedIssue(path, quoted, format)
	}

	return nil
}

// headerIssue returns the most serious issue that res, the check of the header of the image at path, shows, or nil.
// quoted is path as a shell word.
func headerIssue(path, quoted string, res diskchain.Result) *FormatIssue {
	const newerQemu = "A newer qemu made it. Write a copy with the qemu-img of that qemu"

	convert := "qemu-img convert -O qcow2 -o compat=1.1 " + quoted + " <new image>"

	switch res.Problem {
	case diskchain.BadVersion:
		return &FormatIssue{
			Level: IssueError, Problem: res.Sentence(),
			Fix: "If a newer qemu made it, write a copy with the qemu-img of that qemu", Command: convert,
		}
	case diskchain.UnknownFeatures:
		return &FormatIssue{Level: IssueError, Problem: res.Sentence(), Fix: newerQemu, Command: convert}
	case diskchain.AESEncrypted, diskchain.LUKSEncrypted:
		return &FormatIssue{
			Level: IssueError, Problem: res.Sentence(),
			Fix:     "Write a copy without encryption. The Image Format Versions section of the disks docs shows how",
			Command: "",
		}
	case diskchain.Unreadable:
		return &FormatIssue{
			Level: IssueWarning, Problem: fmt.Sprintf("phenix cannot read the header of %s (%s)", path, res.Detail),
			Fix: "Examine it with qemu-img", Command: "qemu-img check " + quoted,
		}
	case diskchain.Whole, diskchain.Missing, diskchain.Loop, diskchain.TooDeep, diskchain.Unsupported,
		diskchain.NotAFile, diskchain.DataFile:
	}

	repair := "qemu-img check -r all " + quoted

	var issue *FormatIssue

	for _, note := range res.Notes {
		var next *FormatIssue

		switch note.Kind {
		case diskchain.Corrupt:
			return &FormatIssue{
				Level: IssueError, Problem: note.Sentence(), Fix: "Copy the image, then repair it", Command: repair,
			}
		case diskchain.QcowV1:
			next = &FormatIssue{
				Level:   IssueWarning,
				Problem: path + " is a qcow version 1 image, an old format that some qemu builds cannot open",
				Fix:     "Convert it to qcow2, then rebase each image that it backs onto the copy",
				Command: "qemu-img convert -f qcow -O qcow2 " + quoted + " <new image>",
			}
		case diskchain.Qcow2V2:
			next = &FormatIssue{
				Level: IssueWarning, Problem: path + " is a qcow2 version 2 image, an old version of the format",
				Fix:     "Stop each VM that uses it, then upgrade it to version 3",
				Command: "qemu-img amend -f qcow2 -o compat=1.1 " + quoted,
			}
		case diskchain.Dirty:
			next = &FormatIssue{
				Level: IssueWarning, Problem: "qemu did not close " + path + " cleanly",
				Fix: "Copy the image, then repair it", Command: repair,
			}
		case diskchain.QED:
			next = uncheckedIssue(path, quoted, string(diskchain.FormatQED))
		case diskchain.VMDK:
			next = uncheckedIssue(path, quoted, string(diskchain.FormatVMDK))
		case diskchain.Zstd, diskchain.ExtendedL2:
		}

		if issue == nil {
			issue = next
		}
	}

	return issue
}

// uncheckedIssue is the warning about the image at path, of format, whose backing chain phenix does not check.
// quoted is path as a shell word.
func uncheckedIssue(path, quoted, format string) *FormatIssue {
	return &FormatIssue{
		Level:   IssueWarning,
		Problem: fmt.Sprintf("%s is a %s image, a format whose backing chain phenix does not check", path, format),
		Fix:     "Convert it to qcow2",
		Command: fmt.Sprintf("qemu-img convert -f %s -O qcow2 -o compat=1.1 %s <new image>", shellQuote(format), quoted),
	}
}

// setRefusedBacking records in d that qemu refuses res.At, an image of d's backing chain, as the check or walk res
// shows. It keeps an error issue of d's own.
func (d *Details) setRefusedBacking(res diskchain.Result) {
	if !res.Refused() || res.At == d.FullPath || (d.FormatIssue != nil && d.FormatIssue.Level == IssueError) {
		return
	}

	d.FormatIssue = headerIssue(res.At, shellQuote(res.At), res)
}

// setRefusedBackings records in each image of chain, the image first, the nearest image below it that qemu refuses,
// from headers, the header of each image.
func setRefusedBackings(chain []Details, headers []imageHeader) {
	var refused diskchain.Result

	for i := len(chain) - 1; i >= 0; i-- {
		chain[i].setRefusedBacking(refused)

		if headers[i].read && headers[i].check.Refused() {
			refused = headers[i].check
		}
	}
}

// shellQuote returns s as one shell word.
func shellQuote(s string) string {
	if shellSafe.MatchString(s) {
		return s
	}

	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// refusedImage returns the image at path alone, read from its header, when qemu refuses it (see
// diskchain.Result.Refused), so the list shows an image that qemu-img cannot inspect. locked holds the inodes of
// files in use, as lockedInodes returns them, or is nil.
func refusedImage(path string, locked map[uint64]bool) (Details, bool) {
	ih := readHeader(path, path)
	if !ih.read || !ih.check.Refused() {
		return Details{}, false
	}

	stamp, err := stat(path)
	if err != nil {
		return Details{}, false
	}

	image := Details{ //nolint:exhaustruct // partial initialization
		Kind:          VMImage,
		Name:          filepath.Base(path),
		FullPath:      path,
		Size:          humanReadableBytes(stamp.size),
		BackingImages: []string{},
		InUse:         locked[stamp.inode],
	}

	image.setFormat(string(ih.header.Format), ih)

	return image, true
}
