package mm

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/hashicorp/go-multierror"
)

// ShellStep is one command of the script that MeshShellSteps or MeshShellEach
// runs. Action names what the command does, for the error when it fails.
type ShellStep struct {
	Action  string
	Command string
}

// firstStepStatus is the exit status of a MeshShellSteps script when its first
// step fails; step i fails with firstStepStatus+i.
const firstStepStatus = 100

// maxShellSteps keeps the exit status of the last step at or below 255.
const maxShellSteps = 255 - firstStepStatus

var (
	// shellWord matches a value sh reads as one literal word.
	shellWord = regexp.MustCompile(`^[A-Za-z0-9_./:-]+$`)

	// exitStatus matches the error minimega returns for a shell command that
	// exits with a status other than 0.
	exitStatus = regexp.MustCompile(`exit status (\d+)`)
)

// ValidateShellWord returns an error wrapping ErrUnsafeArg unless sh reads s as
// one literal word: s is not empty and has only letters, digits and "_./:-".
func ValidateShellWord(s string) error {
	if s == "" {
		return fmt.Errorf("%w: empty value", ErrUnsafeArg)
	}

	if !shellWord.MatchString(s) {
		return fmt.Errorf("%w: %q is not a plain shell word", ErrUnsafeArg, s)
	}

	return nil
}

// MeshShellSteps runs the commands of steps in order on host (the headnode when
// "") with one minimega shell command, and stops at the first command that
// fails. The error names the Action of that step.
func MeshShellSteps(host string, steps ...ShellStep) error {
	return shellSteps(DefaultMM.MeshShell, host, steps)
}

// MeshShellEach runs the commands of steps on host (the headnode when "") with
// one minimega shell command, and continues after a command fails. The error
// names the Action of each step that failed. The commands' error output is
// discarded.
func MeshShellEach(ctx context.Context, host string, steps ...ShellStep) error {
	return shellEach(ctx, DefaultMM.MeshShellResponse, host, steps)
}

func shellSteps(run func(host, cmd string) error, host string, steps []ShellStep) error {
	parts := make([]string, len(steps))
	for i, s := range steps {
		parts[i] = fmt.Sprintf("%s || exit %d", s.Command, firstStepStatus+i)
	}

	script, err := shellScript(parts)
	if err != nil || script == "" {
		return err
	}

	err = run(host, script)
	if err == nil {
		return nil
	}

	if i, ok := failedStep(err, len(steps)); ok {
		return fmt.Errorf("%s: %w", steps[i].Action, err)
	}

	return err
}

func shellEach(
	ctx context.Context,
	run func(ctx context.Context, host, cmd string) (string, error),
	host string,
	steps []ShellStep,
) error {
	parts := make([]string, len(steps))
	for i, s := range steps {
		parts[i] = fmt.Sprintf("%s 2>/dev/null || echo %d", s.Command, i)
	}

	script, err := shellScript(parts)
	if err != nil || script == "" {
		return err
	}

	out, err := run(ctx, host, script)
	if err != nil {
		return fmt.Errorf("running %d shell commands on host %s: %w", len(steps), host, err)
	}

	var errs error

	for field := range strings.FieldsSeq(out) {
		i, err := strconv.Atoi(field)
		if err != nil || i < 0 || i >= len(steps) {
			errs = multierror.Append(errs, fmt.Errorf("unexpected shell output %q on host %s", field, host))

			continue
		}

		errs = multierror.Append(errs, fmt.Errorf("%s: command %q failed on host %s", steps[i].Action, steps[i].Command, host))
	}

	return errs
}

// shellScript returns the minimega shell arguments that run parts as one sh
// script, or "" when there are no parts.
func shellScript(parts []string) (string, error) {
	if len(parts) == 0 {
		return "", nil
	}

	if len(parts) > maxShellSteps {
		return "", fmt.Errorf("%d shell steps is more than the limit of %d", len(parts), maxShellSteps)
	}

	script := strings.Join(parts, "; ")

	// the script goes to minimega in double quotes, so it must be one argument there
	if err := ValidateQuotedArg(script); err != nil {
		return "", fmt.Errorf("shell script: %w", err)
	}

	return `sh -c "` + script + `"`, nil
}

// failedStep returns the index of the step whose exit status err reports.
func failedStep(err error, steps int) (int, bool) {
	matches := exitStatus.FindAllStringSubmatch(err.Error(), -1)
	if len(matches) == 0 {
		return 0, false
	}

	status, convErr := strconv.Atoi(matches[len(matches)-1][1])
	if convErr != nil {
		return 0, false
	}

	i := status - firstStepStatus
	if i < 0 || i >= steps {
		return 0, false
	}

	return i, true
}
