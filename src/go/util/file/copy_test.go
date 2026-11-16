package file

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	"phenix/util/mm/mmtest"
)

// copyStatusReplies has minimega answer the n-th file status with rows[n] as the completed column
// of the copied file, and with no row for it after the last.
func copyStatusReplies(t *testing.T, rows ...string) {
	t.Helper()

	var polls int

	mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
		if !strings.HasSuffix(cmd.Base, "file status") {
			return nil
		}

		polls++
		if polls > len(rows) {
			return []*minicli.Response{mmtest.Tabular("compute1", []string{"filename", "completed"})}
		}

		return []*minicli.Response{mmtest.Tabular("compute1", []string{"filename", "completed"},
			[]string{"/exp/files/a.pcap", rows[polls-1]})}
	})
}

// CopyFile reports the progress of each poll in which the transfer is listed with a valid
// completed column, and leaves out the others without failing.
//
//nolint:paralleltest // replaces minimega
func TestCopyFileReportsProgress(t *testing.T) {
	copyStatusReplies(t, "1/4", "bad", "2/4", "3/0", "x/4", "4/4")

	var reports []float64

	files := MMClusterFiles{PollInterval: time.Millisecond}
	if err := files.CopyFile("/exp/files/a.pcap", "compute1", func(p float64) { reports = append(reports, p) }); err != nil {
		t.Fatalf("CopyFile(/exp/files/a.pcap, compute1) error = %v, want nil", err)
	}

	if diff := cmp.Diff([]float64{0.25, 0.5, 1}, reports); diff != "" {
		t.Errorf("CopyFile(/exp/files/a.pcap, compute1) progress reports mismatch (-want +got):\n%s", diff)
	}
}

// CopyFile gives up with ErrCopyStalled when the completed count stays the same for StallTimeout.
//
//nolint:paralleltest // replaces minimega
func TestCopyFileStalls(t *testing.T) {
	rows := make([]string, 1000)
	for i := range rows {
		rows[i] = "1/4"
	}

	copyStatusReplies(t, rows...)

	files := MMClusterFiles{PollInterval: time.Millisecond, StallTimeout: 10 * time.Millisecond}

	err := files.CopyFile("/exp/files/a.pcap", "compute1", nil)
	if !errors.Is(err, ErrCopyStalled) {
		t.Errorf("CopyFile(/exp/files/a.pcap, compute1) error = %v, want %v", err, ErrCopyStalled)
	}
}

// A transfer that makes progress does not stall, even when it takes longer than StallTimeout.
//
//nolint:paralleltest // replaces minimega
func TestCopyFileProgressResetsStall(t *testing.T) {
	rows := make([]string, 100)
	for i := range rows {
		rows[i] = strconv.Itoa(i) + "/100"
	}

	copyStatusReplies(t, rows...)

	files := MMClusterFiles{PollInterval: time.Millisecond, StallTimeout: 30 * time.Millisecond}
	start := time.Now()

	if err := files.CopyFile("/exp/files/a.pcap", "compute1", nil); err != nil {
		t.Errorf("CopyFile(/exp/files/a.pcap, compute1) error = %v, want nil", err)
	}

	if took := time.Since(start); took < files.StallTimeout {
		t.Logf("the copy took %s, less than StallTimeout %s, so this run did not check the reset", took, files.StallTimeout)
	}
}

func TestCompletedFraction(t *testing.T) {
	tests := []struct {
		in     string
		want   float64
		wantOK bool
	}{
		{"1/4", 0.25, true},
		{"4/4", 1, true},
		{"0/3", 0, true},
		{"3", 0, false},
		{"", 0, false},
		{"1/0", 0, false},
		{"1/-2", 0, false},
		{"x/4", 0, false},
		{"1/x", 0, false},
		{"NaN/4", 0, false},
		{"Inf/4", 0, false},
		{"1/Inf", 0, false},
	}

	for _, tt := range tests {
		got, ok := completedFraction(tt.in)
		if got != tt.want || ok != tt.wantOK {
			t.Errorf("completedFraction(%q) = %v, %t, want %v, %t", tt.in, got, ok, tt.want, tt.wantOK)
		}
	}
}

// completedFraction never panics, and a fraction it accepts is a finite number.
func FuzzCompletedFraction(f *testing.F) {
	for _, seed := range []string{"1/4", "3", "1/0", "/", "1//2", "-1/2", "1e308/1e-308", "NaN/1", "0x10/2"} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, in string) {
		got, ok := completedFraction(in)
		if !ok {
			if got != 0 {
				t.Errorf("completedFraction(%q) = %v, false, want 0", in, got)
			}

			return
		}

		if math.IsNaN(got) || !strings.Contains(in, "/") {
			t.Errorf("completedFraction(%q) = %v, true, want a finite fraction of a parts/total value", in, got)
		}
	})
}
