package experiment

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"phenix/util/mm"
)

// ErrVLANInUse is matched by the error Start returns when minimega gives a VLAN ID that the experiment pins to another
// alias (see VLANInUseError).
var ErrVLANInUse = errors.New("VLAN ID in use")

// minimegaVLANInUse is minimega's error for `vlans add` of an ID that another alias has.
const minimegaVLANInUse = "vlan already in use"

// VLANInUseError is a start that failed because minimega gives the VLAN ID that the alias pins to another alias,
// of this or another namespace. It matches ErrVLANInUse and wraps minimega's error.
type VLANInUseError struct {
	ID    int
	Alias string
	Err   error
}

func (e *VLANInUseError) Error() string {
	return fmt.Sprintf("VLAN ID %d of alias %s is already in use in minimega", e.ID, e.Alias)
}

func (e *VLANInUseError) Unwrap() error { return e.Err }

// Is reports whether target is ErrVLANInUse.
func (e *VLANInUseError) Is(target error) bool {
	return target == ErrVLANInUse
}

// explainVLANInUse returns err, or a *VLANInUseError that names the pinned ID when err is minimega's error that an ID
// is in use. It reads minimega's aliases, so call it before the failed start clears the namespace.
func explainVLANInUse(ctx context.Context, ns string, pins map[string]int, err error) error {
	if err == nil || !strings.Contains(err.Error(), minimegaVLANInUse) {
		return err
	}

	namespaces, lerr := mm.GetNamespaces(mm.Context(ctx))
	if lerr != nil {
		return err
	}

	owners := map[int][2]string{} // the namespace and alias of each ID

	for _, n := range namespaces {
		aliases, lerr := mm.GetVLANs(mm.NS(n.Name), mm.Context(ctx))
		if lerr != nil {
			return err
		}

		for alias, id := range aliases {
			owners[id] = [2]string{n.Name, alias}
		}
	}

	for _, alias := range slices.Sorted(maps.Keys(pins)) {
		id := pins[alias]

		if owner, ok := owners[id]; ok && id != 0 && owner != [2]string{ns, alias} {
			return &VLANInUseError{ID: id, Alias: alias, Err: err}
		}
	}

	return err
}
