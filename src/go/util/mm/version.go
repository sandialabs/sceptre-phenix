package mm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/mod/semver"

	"phenix/util/mm/mmcli"
	"phenix/util/plog"
)

// MinCaptureVersion is the first minimega release with experiment captures and the refreshed VM capture commands.
const MinCaptureVersion = "v3.4.0"

// CaptureVersionRequired tells whether the capture support needs MinCaptureVersion. While it is false, the support
// starts forced on, as with ui.features minimega-captures, so phenix uses the capture commands of every minimega.
const CaptureVersionRequired = false

// ErrCaptureUnsupported is wrapped by the error of a capture call that the head node's minimega cannot do.
var ErrCaptureUnsupported = errors.New("experiment captures are not supported")

// errEmptyVersion is the error of a version query that gives an empty answer, which phenix does not keep.
var errEmptyVersion = errors.New("minimega gave an empty version answer")

// CaptureSupport tells whether phenix uses the capture commands of minimega 3.4.0 and, when it does not, why not.
type CaptureSupport struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
	Version   string `json:"version,omitempty"`
}

//nolint:gochecknoglobals // tests replace them
var (
	// versionRetry is how long the startup check waits between two version queries, and how long each one may take.
	versionRetry = 10 * time.Second

	// queryVersion asks the head node's minimega for its version answer.
	queryVersion = askMinimegaVersion
)

// minimegaVersion is the first version answer of minimega in this process, kept until phenix restarts.
//
//nolint:gochecknoglobals // process-wide answer
var minimegaVersion = struct {
	mu       sync.Mutex
	forced   bool   // ui.features has minimega-captures, or CaptureVersionRequired is false
	answered bool   // minimega gave answer
	answer   string // the whole answer, such as "minimega 3.4.0 abc123 2026-10-01"
	watching bool   // StartVersionCheck runs, so a capture call does not ask itself
}{mu: sync.Mutex{}, forced: !CaptureVersionRequired, answered: false, answer: "", watching: false}

// versionAsk lets one capture call at a time ask minimega for its version.
var versionAsk sync.Mutex //nolint:gochecknoglobals // shared by all callers

// askMinimegaVersion sends the version command to the head node.
func askMinimegaVersion(ctx context.Context) (string, error) {
	cmd := mmcli.NewCommand()
	cmd.Command = "version"

	answer, err := mmcli.SingleResponse(mmcli.RunContext(ctx, cmd))
	if err != nil {
		return "", fmt.Errorf("asking minimega for its version: %w", err)
	}

	return answer, nil
}

// parseMinimegaVersion returns the semantic version, such as "v3.4.0", in the second field of a version answer
// (`minimega <version> <revision> <date>`), and false when that field is not a semantic version.
func parseMinimegaVersion(answer string) (string, bool) {
	const versionField = 1 // after the program name

	fields := strings.Fields(answer)
	if len(fields) <= versionField {
		return "", false
	}

	v := "v" + strings.TrimPrefix(fields[versionField], "v")
	if !semver.IsValid(v) {
		return "", false
	}

	return v, true
}

// CaptureSupportOf is the capture support of a minimega that gave answer to the version command. forced is true
// when ui.features has minimega-captures, which turns the support on whatever the answer.
func CaptureSupportOf(answer string, forced bool) CaptureSupport {
	return captureSupportOf(true, answer, forced)
}

func captureSupportOf(answered bool, answer string, forced bool) CaptureSupport {
	minimum := strings.TrimPrefix(MinCaptureVersion, "v")
	v, ok := parseMinimegaVersion(answer)
	shown := strings.TrimPrefix(v, "v")

	switch {
	case forced:
		return CaptureSupport{Available: true, Reason: "", Version: shown}
	case !answered:
		return CaptureSupport{
			Available: false, Reason: "requires minimega " + minimum + " or later; minimega did not answer", Version: "",
		}
	case !ok:
		return CaptureSupport{
			Available: false,
			Reason: fmt.Sprintf(
				"requires minimega %s or later; cannot read the minimega version %q", minimum, strings.TrimSpace(answer),
			),
			Version: "",
		}
	case semver.Compare(v, MinCaptureVersion+"-0") < 0:
		return CaptureSupport{
			Available: false, Reason: "requires minimega " + minimum + " or later; the head node runs " + shown, Version: shown,
		}
	}

	return CaptureSupport{Available: true, Reason: "", Version: shown}
}

// CaptureSupportStatus is the capture support of the head node's minimega as this process knows it now. It never
// asks minimega: phenix ui asks at startup (see StartVersionCheck), and another process at its first capture call.
func CaptureSupportStatus() CaptureSupport {
	minimegaVersion.mu.Lock()
	defer minimegaVersion.mu.Unlock()

	return captureSupportOf(minimegaVersion.answered, minimegaVersion.answer, minimegaVersion.forced)
}

// CaptureFeatures reports whether phenix uses the capture commands of minimega 3.4.0 (see CaptureSupportStatus).
func CaptureFeatures() bool {
	return CaptureSupportStatus().Available
}

// SetCaptureFeaturesForced turns the capture support on whatever minimega's version, for the ui.features name
// minimega-captures and a development build of minimega that still gives an older version.
func SetCaptureFeaturesForced(on bool) {
	minimegaVersion.mu.Lock()
	defer minimegaVersion.mu.Unlock()

	minimegaVersion.forced = on
}

// QueryCaptureSupport returns the capture support. A process with no StartVersionCheck, such as a CLI command, asks
// minimega for its version first, until minimega answers once; ctx bounds that question.
func QueryCaptureSupport(ctx context.Context) CaptureSupport {
	return captureSupport(ctx)
}

// captureSupport is the capture support for a capture call in ctx. A process with no StartVersionCheck asks minimega
// for its version here until minimega answers once.
func captureSupport(ctx context.Context) CaptureSupport {
	minimegaVersion.mu.Lock()
	known := minimegaVersion.answered || minimegaVersion.watching || minimegaVersion.forced
	minimegaVersion.mu.Unlock()

	if !known {
		versionAsk.Lock()
		_, _ = knownVersion(ctx, queryVersion)
		versionAsk.Unlock()
	}

	return CaptureSupportStatus()
}

// unsupported returns an error wrapping ErrCaptureUnsupported when s is not available, else nil.
func (s CaptureSupport) unsupported() error {
	if s.Available {
		return nil
	}

	return fmt.Errorf("%w: %s", ErrCaptureUnsupported, s.Reason)
}

// knownVersion returns minimega's first version answer, asking it through query when it has not answered yet. An
// empty answer is an error, so the next use asks again.
func knownVersion(ctx context.Context, query func(context.Context) (string, error)) (string, error) {
	minimegaVersion.mu.Lock()
	answered, answer := minimegaVersion.answered, minimegaVersion.answer
	minimegaVersion.mu.Unlock()

	if answered {
		return answer, nil
	}

	answer, err := query(ctx)
	if err != nil {
		return "", err
	}

	if strings.TrimSpace(answer) == "" {
		return "", errEmptyVersion
	}

	minimegaVersion.mu.Lock()
	defer minimegaVersion.mu.Unlock()

	if minimegaVersion.answered {
		return minimegaVersion.answer, nil
	}

	minimegaVersion.answered, minimegaVersion.answer = true, answer
	logCaptureSupport(captureSupportOf(true, answer, false), answer, minimegaVersion.forced)

	return answer, nil
}

// logCaptureSupport writes the one log line about minimega's first version answer. With forced, it logs only the
// answer, because phenix uses the capture commands whatever the version.
func logCaptureSupport(s CaptureSupport, answer string, forced bool) {
	switch {
	case forced:
		plog.Info(plog.TypeSystem, "minimega answered the version query", "answer", strings.TrimSpace(answer))
	case s.Available:
		plog.Info(plog.TypeSystem, "minimega supports experiment captures", "version", s.Version)
	case s.Version == "":
		plog.Warn(plog.TypeSystem, "cannot read the minimega version", "answer", strings.TrimSpace(answer), "reason", s.Reason)
	default:
		plog.Info(plog.TypeSystem, "minimega does not support experiment captures", "reason", s.Reason)
	}
}

// MinimegaVersion returns minimega's whole version answer, asking minimega only until it has answered once.
func MinimegaVersion() (string, error) {
	return knownVersion(context.Background(), queryVersion)
}

// StartVersionCheck asks minimega for its version in the background, again every 10 s until minimega answers or ctx
// is done. phenix ui calls it once at startup; later calls do nothing until ForgetCluster.
func StartVersionCheck(ctx context.Context) {
	_ = startVersionCheck(ctx, queryVersion, versionRetry)
}

// startVersionCheck is StartVersionCheck with its query and interval, returning a channel closed when the check ends,
// or nil when a check already ran.
func startVersionCheck(
	ctx context.Context,
	query func(context.Context) (string, error),
	interval time.Duration,
) <-chan struct{} {
	minimegaVersion.mu.Lock()
	defer minimegaVersion.mu.Unlock()

	if minimegaVersion.watching {
		return nil
	}

	minimegaVersion.watching = true
	done := make(chan struct{})

	go func() {
		defer close(done)

		watchVersion(ctx, query, interval)
	}()

	return done
}

// watchVersion asks for minimega's version every interval until it answers or ctx is done.
func watchVersion(ctx context.Context, query func(context.Context) (string, error), interval time.Duration) {
	for attempt := 0; ; attempt++ {
		ask, cancel := context.WithTimeout(ctx, interval)
		_, err := knownVersion(ask, query)

		cancel()

		if err == nil {
			return
		}

		if attempt == 0 {
			plog.Warn(plog.TypeSystem, "minimega did not answer the version query; asking again in the background",
				"interval", interval, "error", err)
		} else {
			plog.Debug(plog.TypeSystem, "minimega did not answer the version query", "error", err)
		}

		timer := time.NewTimer(interval)

		select {
		case <-ctx.Done():
			timer.Stop()

			return
		case <-timer.C:
		}
	}
}

// forgetVersion drops minimega's version answer and the mark of a startup check, so the next use asks again; see
// ForgetCluster.
func forgetVersion() {
	minimegaVersion.mu.Lock()
	defer minimegaVersion.mu.Unlock()

	minimegaVersion.answered, minimegaVersion.answer, minimegaVersion.watching = false, "", false
}
