package web

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"phenix/api/experiment"
	"phenix/scheduler"
	"phenix/store"
)

// A failed schedule answers 404 for a missing experiment, 409 for a running one or a cluster without
// schedulable hosts, 400 for an unknown algorithm and 500 otherwise.
func TestScheduleErrorStatus(t *testing.T) {
	tests := map[string]struct {
		err  error
		want int
	}{
		"missing experiment": {err: fmt.Errorf("getting experiment: %w", store.ErrNotExist), want: http.StatusNotFound},
		"running experiment": {err: fmt.Errorf("%w (started at: now)", experiment.ErrExperimentRunning), want: http.StatusConflict},
		"no schedulable hosts": {
			err:  fmt.Errorf("running scheduler algorithm: %w", scheduler.ErrNoSchedulableHosts),
			want: http.StatusConflict,
		},
		"unknown algorithm": {
			err:  fmt.Errorf("running user scheduler: %w", scheduler.ErrUserSchedulerNotFound),
			want: http.StatusBadRequest,
		},
		"other failure": {err: errors.New("updating experiment config"), want: http.StatusInternalServerError},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if got := scheduleErrorStatus(tc.err); got != tc.want {
				t.Errorf("scheduleErrorStatus(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}
