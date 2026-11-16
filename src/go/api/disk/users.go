package disk

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// ErrInUse is the cause of a delete refused because other images or experiments use the image (see InUseError).
var ErrInUse = errors.New("disk image is in use")

// MaxNamedUsers is the most images and experiments that UsersText names before it gives the count of the others.
const MaxNamedUsers = 10

// InUseError is a delete of the image at Path, refused because other images or experiments use it.
type InUseError struct {
	// Path is the full path of the image.
	Path string
	// Images are the images whose backing chain holds Path, sorted by full path.
	Images []Details
	// Experiments are the experiments whose VMs use Path or an image backed by it, sorted by name.
	Experiments []ExperimentUse
}

func (e *InUseError) Error() string {
	paths := make([]string, 0, len(e.Images))
	for _, image := range e.Images {
		paths = append(paths, image.FullPath)
	}

	return fmt.Sprintf("disk image %s is in use by %s", e.Path, UsersText(paths, e.Experiments, 0))
}

func (e *InUseError) Unwrap() error {
	return ErrInUse
}

// Users returns the images whose backing chain holds the image at path, a cleaned absolute path, and the experiments
// whose VMs use it directly or through a backing chain. It reads them from the disk list (see GetImages), so it reuses
// the chains the list has in its cache.
func Users(path string) ([]Details, []ExperimentUse, error) {
	images, err := GetImages("")
	if err != nil {
		return nil, nil, err
	}

	var (
		backed []Details
		uses   []ExperimentUse
	)

	for _, image := range images {
		switch {
		case image.FullPath == path:
			uses = append(uses, image.Experiments...)
		case slices.Contains(image.BackingImages, path):
			backed = append(backed, image)
		}
	}

	slices.SortFunc(backed, func(a, b Details) int { return cmp.Compare(a.FullPath, b.FullPath) })
	slices.SortFunc(uses, func(a, b ExperimentUse) int { return cmp.Compare(a.Name, b.Name) })

	return backed, slices.CompactFunc(uses, func(a, b ExperimentUse) bool { return a.Name == b.Name }), nil
}

// UsersText names the images (full paths) and experiments, a running one with "(running)", in one phrase:
// "image /a.qc2, experiment exp (running) and 2 more". It names at most MaxNamedUsers of them; hidden others that the
// reader may not see add to the count of the ones not named.
func UsersText(images []string, experiments []ExperimentUse, hidden int) string {
	names := make([]string, 0, len(images)+len(experiments))

	for _, image := range images {
		names = append(names, "image "+image)
	}

	for _, exp := range experiments {
		name := "experiment " + exp.Name
		if exp.Running {
			name += " (running)"
		}

		names = append(names, name)
	}

	more := hidden
	if len(names) > MaxNamedUsers {
		more += len(names) - MaxNamedUsers
		names = names[:MaxNamedUsers]
	}

	switch {
	case len(names) == 0:
		return fmt.Sprintf("%d %s", more, plural(more, "other", "others"))
	case more > 0:
		return fmt.Sprintf("%s and %d more", strings.Join(names, ", "), more)
	default:
		return strings.Join(names, ", ")
	}
}

// plural is one when n is 1, and many otherwise.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}

	return many
}
