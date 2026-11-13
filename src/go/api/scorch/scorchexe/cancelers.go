package scorchexe

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
)

var (
	cancelers   = make(map[string]context.CancelFunc) //nolint:gochecknoglobals // global state
	cancelersMu sync.Mutex                            //nolint:gochecknoglobals // global lock
)

func AddCanceler(ctx context.Context, exp string, run int) context.Context {
	key := fmt.Sprintf("%s/%d", exp, run)

	cancelersMu.Lock()
	defer cancelersMu.Unlock()

	ctx, cancel := context.WithCancel(ctx)
	cancelers[key] = cancel

	return ctx
}

func HasCanceler(exp string, run int) bool {
	key := fmt.Sprintf("%s/%d", exp, run)

	cancelersMu.Lock()
	defer cancelersMu.Unlock()

	_, ok := cancelers[key]

	return ok
}

// ExecutingRuns returns, in order, the runs of experiment exp that have a
// canceler: the runs that this process executes.
func ExecutingRuns(exp string) []int {
	runs := []int{}

	cancelersMu.Lock()
	defer cancelersMu.Unlock()

	for key := range cancelers {
		rest, ok := strings.CutPrefix(key, exp+"/")
		if !ok {
			continue
		}

		if run, err := strconv.Atoi(rest); err == nil {
			runs = append(runs, run)
		}
	}

	slices.Sort(runs)

	return runs
}

func GetExperimentCancelers(exp string) []context.CancelFunc {
	var expCancelers []context.CancelFunc

	cancelersMu.Lock()
	defer cancelersMu.Unlock()

	for run := range cancelers {
		// run keys are prefixed with the name of the experiment
		if strings.HasPrefix(run, exp+"/") {
			expCancelers = append(expCancelers, cancelers[run])
			delete(cancelers, run)
		}
	}

	return expCancelers
}

func GetCanceler(exp string, run int) context.CancelFunc {
	key := fmt.Sprintf("%s/%d", exp, run)

	cancelersMu.Lock()
	defer cancelersMu.Unlock()

	cancel := cancelers[key]
	delete(cancelers, key)

	return cancel
}
