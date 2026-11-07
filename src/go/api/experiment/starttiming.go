package experiment

import (
	"time"

	"phenix/util/plog"
)

// startStepsMessage is the message of the log record that gives the time of each step of a start.
const startStepsMessage = "experiment start steps"

// startTimer records how long each step of a start of experiment exp takes. It logs the name of each step at
// debug level as the step begins, and done logs the time of each step and the total at info level.
type startTimer struct {
	exp   string
	now   func() time.Time
	first time.Time // when the start began
	begun time.Time // when the current step began
	step  string    // the current step, "" before the first
	steps []any     // step name and time pairs, for the log record
}

func newStartTimer(exp string, now func() time.Time) *startTimer {
	t := now()

	return &startTimer{exp: exp, now: now, first: t, begun: t, step: "", steps: nil}
}

// begin ends the current step and begins the step name.
func (t *startTimer) begin(name string) {
	t.end()

	t.step = name

	plog.Debug(plog.TypeSystem, "experiment start step", "exp", t.exp, "step", name)
}

// end records the time of the current step, if there is one.
func (t *startTimer) end() {
	now := t.now()

	if t.step != "" {
		t.steps = append(t.steps, t.step, roundDuration(now.Sub(t.begun)))
	}

	t.step, t.begun = "", now
}

// done ends the current step and logs the time of each step and the total. The last step logged is the one that
// was running when a failed start ended.
func (t *startTimer) done() {
	t.end()

	args := append([]any{"exp", t.exp}, t.steps...)
	args = append(args, "total", roundDuration(t.now().Sub(t.first)))

	plog.Info(plog.TypeSystem, startStepsMessage, args...)
}

// roundDuration returns d to the millisecond, as text such as 1.25s.
func roundDuration(d time.Duration) string {
	return d.Round(time.Millisecond).String()
}
