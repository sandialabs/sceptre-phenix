package vm

import (
	"context"
	"errors"
	"fmt"

	"phenix/util/mm"
)

// ErrNoFilterMatch is wrapped by the error of FilterVMs for a filter that
// matches none of the experiment's VMs.
var ErrNoFilterMatch = errors.New("no VMs matched filter")

// FilterVMs returns the names of the VMs of the experiment expName that the
// filter expression filter matches, as mm.BuildTree reads it, such as
// "state=running" or "name=web*". An empty filter returns an empty list,
// which the capture functions take as every VM. A filter that matches no VM
// returns an error that wraps ErrNoFilterMatch. Once ctx is done, the VM
// listing gives up (see mm.Context).
func FilterVMs(ctx context.Context, expName, filter string) ([]string, error) {
	if filter == "" {
		return []string{}, nil
	}

	vms, err := List(ctx, expName)
	if err != nil {
		return nil, fmt.Errorf("listing the VMs of experiment %s: %w", expName, err)
	}

	var names []string

	if tree := mm.BuildTree(filter); tree != nil {
		for _, v := range vms {
			if tree.Evaluate(&v) {
				names = append(names, v.Name)
			}
		}
	}

	if len(names) == 0 {
		return nil, fmt.Errorf("%w %q", ErrNoFilterMatch, filter)
	}

	return names, nil
}
