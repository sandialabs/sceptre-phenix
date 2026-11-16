package web

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"phenix/api/vm"
	"phenix/store"
	"phenix/util/mm"
)

// vmStateError answers each state error of a VM action with its status and message, and leaves other errors to the
// handler. An experiment that is gone after vmTarget read it, for example one deleted during the action, is a 404.
func TestVMStateError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		err         error
		wantStatus  int // 0 for no answer
		wantMessage string
	}{
		{
			name: "a missing snapshot", err: fmt.Errorf("restoring: %w", vm.ErrSnapshotNotFound),
			wantStatus: http.StatusNotFound, wantMessage: "snapshot not found for VM web in experiment exp",
		},
		{
			name: "a VM that minimega does not have", err: fmt.Errorf("getting VM: %w", mm.ErrVMNotFound),
			wantStatus: http.StatusNotFound, wantMessage: "VM web not found in experiment exp",
		},
		{
			name: "an experiment that is gone", err: fmt.Errorf("getting experiment exp: %w", store.ErrNotExist),
			wantStatus: http.StatusNotFound, wantMessage: "experiment exp not found",
		},
		{
			name: "a VM that does not run", err: fmt.Errorf("snapshotting: %w", vm.ErrVMNotRunning),
			wantStatus: http.StatusConflict, wantMessage: "VM web is not running",
		},
		{
			name: "a VM without its snapshot flag", err: fmt.Errorf("checking VM web: %w", vm.ErrSnapshotDisabled),
			wantStatus: http.StatusConflict, wantMessage: "checking VM web: snapshot flag is not set",
		},
		{
			name: "a VM with two disks", err: fmt.Errorf("checking VM web: %w", vm.ErrMultipleDisks),
			wantStatus: http.StatusConflict, wantMessage: "checking VM web: snapshots support only VMs with one disk",
		},
		{name: "another error", err: errors.New("minimega unreachable")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := vmStateError("exp", "web", tt.err)

			switch {
			case tt.wantStatus == 0 && got != nil:
				t.Errorf("vmStateError(%v) = %d %q, want nil", tt.err, got.Status, got.Message)
			case tt.wantStatus != 0 && got == nil:
				t.Errorf("vmStateError(%v) = nil, want %d %q", tt.err, tt.wantStatus, tt.wantMessage)
			case got != nil && (got.Status != tt.wantStatus || got.Message != tt.wantMessage):
				t.Errorf("vmStateError(%v) = %d %q, want %d %q", tt.err, got.Status, got.Message, tt.wantStatus, tt.wantMessage)
			}
		})
	}
}
