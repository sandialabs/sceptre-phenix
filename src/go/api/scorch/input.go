package scorch

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"phenix/api/experiment"
	"phenix/api/scorch/scorchexe"
	"phenix/api/scorch/scorchmd"
	"phenix/app"
	"phenix/types"
)

var (
	// ErrComponentNotFound is the error for a component name that the SCORCH
	// app of the experiment does not configure.
	ErrComponentNotFound = errors.New("SCORCH component not found")

	// ErrBuiltinComponent is the error for a component of a type that phenix
	// has built in, which runs no program and gets no JSON.
	ErrBuiltinComponent = errors.New("built-in SCORCH component")

	// ErrComponentNotRun is the error for a run, loop, stage or count at
	// which no SCORCH run runs the component.
	ErrComponentNotRun = errors.New("SCORCH component does not run there")

	// ErrUnknownReplacement is the error for a replacement value whose key
	// is in none of the replace blocks that apply to the component.
	ErrUnknownReplacement = errors.New("unknown SCORCH replacement")
)

// InputOptions selects the point of a SCORCH run for UserComponentInput.
type InputOptions struct {
	// Run is the index of the run, and Loop the depth of the loop in it
	// (0 for the run itself). Nil selects the first one that runs the
	// component.
	Run, Loop *int
	// Stage is the stage; "" selects the first stage that runs the component.
	Stage Action
	// Count is the pass of the loop, from 0.
	Count int
	// Replace holds replacement values to use in place of random values.
	Replace scorchmd.ResolvedReplacements
	// Runtime adds the cluster hosts and the VMs of the experiment, as an
	// external app that ran earlier in the same app stage adds them.
	Runtime bool
	// StartTime is the start time of the run; zero is now.
	StartTime time.Time
}

// ComponentInput is what a SCORCH run gives the program of a user component.
type ComponentInput struct {
	Program      string                        // phenix-scorch-component-<type>
	Args         []string                      // <stage> <name> <run> <loop> <count>
	Env          []string                      // the PHENIX_* variables that phenix adds
	Stdin        []byte                        // the experiment JSON
	Replacements scorchmd.ResolvedReplacements // the replacement values in Stdin
	// Warnings tell how a live run can give other input.
	Warnings []string
}

// placement is a stage of a loop of a run that runs a component.
type placement struct {
	run, loop int
	stage     Action
	level     *scorchmd.Loop
}

func (p placement) String() string {
	return fmt.Sprintf("run %d loop %d stage %s", p.run, p.loop, p.stage)
}

// UserComponentInput returns what the program of the user component name gets
// from a SCORCH run of the stored experiment expName at the point that opts
// selects: the stdin JSON, the arguments and the PHENIX_* variables. It uses
// the code of a live run, which starts from the UI, the API or phenix
// experiment scorch. It stores nothing.
//
// A live run picks a random value for each replacement at each pass of a
// loop. UserComponentInput picks the values in the same way, and uses the
// values of opts.Replace in their place. The minimega reads of opts.Runtime
// give up once ctx is done; a read that fails wraps experiment.ErrAppRuntime.
func UserComponentInput(ctx context.Context, expName, name string, opts InputOptions) (*ComponentInput, error) {
	exp, err := experiment.Get(expName)
	if err != nil {
		return nil, err
	}

	md, err := scorchmd.DecodeMetadata(exp)
	if err != nil {
		return nil, fmt.Errorf("experiment %s: %w", expName, err)
	}

	spec, ok := md.ComponentSpecs()[name]
	if !ok {
		return nil, fmt.Errorf("%w: %s; %s", ErrComponentNotFound, name, componentNames(md))
	}

	if _, builtin := components[spec.Type]; builtin && spec.Type != "user-shell" {
		return nil, fmt.Errorf("%w: %s has the type %s, which runs no program", ErrBuiltinComponent, name, spec.Type)
	}

	at, err := findPlacement(md, name, opts)
	if err != nil {
		return nil, err
	}

	replacements, err := runReplacements(at, md.Runs[at.run], opts.Replace)
	if err != nil {
		return nil, err
	}

	if opts.Runtime {
		if err := app.PopulateObservedRuntime(ctx, exp); err != nil {
			return nil, fmt.Errorf("%w: %w", experiment.ErrAppRuntime, err)
		}
	}

	start := opts.StartTime
	if start.IsZero() {
		start = time.Now().UTC()
	}

	warnings := inputWarnings(exp, md, at.run)

	scorchexe.SetRunStatus(exp, at.run)

	o := NewOptions(
		Experiment(*exp), Name(name), Type(spec.Type), Stage(at.stage), RunID(at.run), CurrentLoop(at.loop),
		LoopCount(opts.Count), StartTime(startTimeText(start)), Metadata(componentMetadata(spec, replacements)),
	)

	stdin, err := componentStdin(o)
	if err != nil {
		return nil, err
	}

	return &ComponentInput{
		Program:      componentProgram(spec.Type),
		Args:         componentArgs(o, at.stage),
		Env:          componentEnv(o),
		Stdin:        stdin,
		Replacements: replacements,
		Warnings:     warnings,
	}, nil
}

// componentNames tells the names of the components of md.
func componentNames(md scorchmd.ScorchMetadata) string {
	if len(md.Components) == 0 {
		return "the scenario has no SCORCH components"
	}

	names := make([]string, 0, len(md.Components))
	for _, c := range md.Components {
		names = append(names, c.Name)
	}

	return "the SCORCH components of the scenario are " + strings.Join(names, ", ")
}

// componentPlacements returns each stage of each loop of each run of md that
// runs the component name, in the order of the runs, the loops and the stages.
func componentPlacements(md scorchmd.ScorchMetadata, name string) []placement {
	var found []placement

	for r, run := range md.Runs {
		depth := 0

		for level := run; level != nil; level = level.Loop {
			stages := []struct {
				stage Action
				names []string
			}{
				{ActionConfigure, level.Configure}, {ActionStart, level.Start},
				{ActionStop, level.Stop}, {ActionCleanup, level.Cleanup},
			}

			for _, s := range stages {
				if slices.Contains(s.names, name) {
					found = append(found, placement{run: r, loop: depth, stage: s.stage, level: level})
				}
			}

			depth++
		}
	}

	return found
}

// findPlacement returns the first placement of the component name that opts
// selects, and checks that opts.Count is a pass of its loop.
func findPlacement(md scorchmd.ScorchMetadata, name string, opts InputOptions) (placement, error) {
	all := componentPlacements(md, name)

	for _, p := range all {
		if (opts.Run != nil && *opts.Run != p.run) || (opts.Loop != nil && *opts.Loop != p.loop) ||
			(opts.Stage != "" && opts.Stage != p.stage) {
			continue
		}

		if opts.Count < 0 || opts.Count >= p.level.Count {
			return placement{}, fmt.Errorf(
				"%w: count %d: run %d loop %d has the counts 0 to %d", ErrComponentNotRun, opts.Count, p.run, p.loop,
				p.level.Count-1,
			)
		}

		return p, nil
	}

	if len(all) == 0 {
		return placement{}, fmt.Errorf("%w: no stage of a SCORCH run runs the component %s", ErrComponentNotRun, name)
	}

	where := make([]string, 0, len(all))
	for _, p := range all {
		where = append(where, p.String())
	}

	return placement{}, fmt.Errorf("%w: the component %s runs only at %s", ErrComponentNotRun, name, strings.Join(where, ", "))
}

// runReplacements resolves the replacements of each loop of run down to the
// loop of at, as the passes of a live run do, and then sets the values of
// pinned.
func runReplacements(
	at placement,
	run *scorchmd.Loop,
	pinned scorchmd.ResolvedReplacements,
) (scorchmd.ResolvedReplacements, error) {
	var resolved scorchmd.ResolvedReplacements

	level := run

	for range at.loop + 1 {
		var err error

		if resolved, err = loopReplacements(resolved, level); err != nil {
			return nil, err
		}

		level = level.Loop
	}

	for _, key := range slices.Sorted(maps.Keys(pinned)) {
		if _, ok := resolved[key]; !ok {
			return nil, fmt.Errorf(
				"%w %q: the replacements of run %d loop %d are %q", ErrUnknownReplacement, key, at.run, at.loop,
				slices.Sorted(maps.Keys(resolved)),
			)
		}

		resolved[key] = pinned[key]
	}

	return resolved, nil
}

// loopReplacements resolves the replacements of exe and merges them over
// parent, as each pass of a SCORCH loop does.
func loopReplacements(
	parent scorchmd.ResolvedReplacements,
	exe *scorchmd.Loop,
) (scorchmd.ResolvedReplacements, error) {
	own, err := scorchmd.ResolveReplacements(exe.Replace)
	if err != nil {
		return nil, fmt.Errorf("resolving replacements: %w", err)
	}

	return scorchmd.MergeReplacements(parent, own), nil
}

// componentMetadata returns the metadata of the component spec with the
// replacements applied.
func componentMetadata(spec scorchmd.ComponentSpec, replacements scorchmd.ResolvedReplacements) scorchmd.ComponentMetadata {
	return scorchmd.ApplyReplacements(spec.Metadata, replacements)
}

// startTimeText returns start as the components of a run get it.
func startTimeText(start time.Time) string {
	return start.Format(time.RubyDate)
}

// inputWarnings tells how the input of a component of run can differ in a
// live run of exp.
func inputWarnings(exp *types.Experiment, md scorchmd.ScorchMetadata, run int) []string {
	var warnings []string

	if !exp.Running() {
		warnings = append(warnings, fmt.Sprintf(
			"the experiment %s does not run; SCORCH starts a run only in a running experiment", exp.Metadata.Name,
		))
	}

	var taps []string

	for _, c := range md.Components {
		if (c.Type == new(Tap).Type() || c.Type == new(Break).Type()) && md.Runs[run].ContainsComponent(c.Name) {
			taps = append(taps, c.Name)
		}
	}

	if len(taps) > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"in a live run, the components %s can add taps to status.apps.scorch.taps; this input does not show them",
			strings.Join(taps, ", "),
		))
	}

	return warnings
}
