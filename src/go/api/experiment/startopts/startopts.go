// Package startopts reads and checks the options of an experiment start. The
// flags of phenix experiment start and the JSON body of
// POST /experiments/{name}/start both become an Options, so the CLI and the
// API check them in the same way.
package startopts

import (
	"errors"
	"fmt"
	"io"

	"phenix/api/experiment"
	"phenix/api/vlan"
	"phenix/util"
)

// ErrInvalid is wrapped by the errors of Decode and Check for options that
// phenix does not accept.
var ErrInvalid = errors.New("invalid start options")

// Options are the options of one experiment start.
type Options struct {
	// DryRun does all of the start but call minimega.
	DryRun bool `json:"dryRun"`
	// TreatMinimegaErrorsAsWarnings continues the start past minimega errors,
	// which become warnings.
	TreatMinimegaErrorsAsWarnings bool `json:"treatMinimegaErrorsAsWarnings"`
	// HonorRunPeriodically runs the running stage of the apps on the schedule
	// of the scenario after the start. Nil leaves the choice to the caller:
	// the CLI does not run the schedule, and the server does.
	HonorRunPeriodically *bool `json:"honorRunPeriodically,omitempty"`
	// VLANMin and VLANMax replace the ends of the VLAN range of the
	// experiment for this start. 0 keeps the end of the experiment.
	VLANMin int `json:"vlanMin"`
	VLANMax int `json:"vlanMax"`
}

// Decode reads Options from a JSON object in r, and checks them with Check. An
// empty body, null and {} are the options of a start as phenix always did it.
// An unknown field, a value of the wrong type, or anything after the object
// returns an error that wraps ErrInvalid.
func Decode(r io.Reader) (Options, error) {
	var o Options

	err := util.DecodeJSONStrict(r, &o)

	switch {
	case errors.Is(err, io.EOF):
		return Options{}, nil
	case err != nil:
		return Options{}, fmt.Errorf("%w: the body must be a JSON object of start options: %w", ErrInvalid, err)
	}

	if err := o.Check(); err != nil {
		return Options{}, err
	}

	return o, nil
}

// Check returns an error that wraps ErrInvalid unless each end of the VLAN
// range is 0 or a VLAN ID from 1 to 4094, and the range is lowest first when
// both ends are given.
func (o Options) Check() error {
	if err := vlan.CheckRange(o.VLANMin, o.VLANMax); err != nil {
		return fmt.Errorf("%w: VLAN range: %w", ErrInvalid, err)
	}

	return nil
}

// Periodic reports whether the start runs the running stage of the apps on
// their schedule: HonorRunPeriodically, or byDefault when it is not set.
func (o Options) Periodic(byDefault bool) bool {
	if o.HonorRunPeriodically == nil {
		return byDefault
	}

	return *o.HonorRunPeriodically
}

// StartOptions are the options of experiment.Start for a start of the
// experiment name with o.
func (o Options) StartOptions(name string) []experiment.StartOption {
	return []experiment.StartOption{
		experiment.StartWithName(name),
		experiment.StartWithDryRun(o.DryRun),
		experiment.StartWithVLANMin(o.VLANMin),
		experiment.StartWithVLANMax(o.VLANMax),
		experiment.StartWithMMErrorsAsWarnings(o.TreatMinimegaErrorsAsWarnings),
	}
}

// Fields are the options as key/value pairs for an audit event or a log,
// without the options that are not set.
func (o Options) Fields() []any {
	var fields []any

	if o.DryRun {
		fields = append(fields, "phenix.start.dry_run", true)
	}

	if o.TreatMinimegaErrorsAsWarnings {
		fields = append(fields, "phenix.start.mm_errors_as_warnings", true)
	}

	if o.HonorRunPeriodically != nil {
		fields = append(fields, "phenix.start.honor_run_periodically", *o.HonorRunPeriodically)
	}

	if o.VLANMin != 0 {
		fields = append(fields, "phenix.start.vlan_min", o.VLANMin)
	}

	if o.VLANMax != 0 {
		fields = append(fields, "phenix.start.vlan_max", o.VLANMax)
	}

	return fields
}
