package util_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/gofrs/uuid/v5"
	"github.com/google/go-cmp/cmp"

	"phenix/util"
	"phenix/util/plog/plogtest"
)

// LogErrorGetID logs the error with a new version 4 UUID and returns that UUID.
func TestLogErrorGetID(t *testing.T) {
	logs := plogtest.Capture(t)

	id := util.LogErrorGetID(errors.New("disk full"))

	parsed, err := uuid.FromString(id)
	if err != nil || parsed.Version() != uuid.V4 {
		t.Errorf("LogErrorGetID() = %q, want a version 4 UUID (parse error %v)", id, err)
	}

	records := logs.Records(t, plogtest.Message("disk full"))
	if len(records) != 1 {
		t.Fatalf("LogErrorGetID() logged %d records with the error, want 1: %s", len(records), logs.String())
	}

	if got := records[0]["uuid"]; got != id {
		t.Errorf("LogErrorGetID() logged uuid %v, want %q", got, id)
	}

	if other := util.LogErrorGetID(errors.New("disk full")); other == id {
		t.Errorf("LogErrorGetID() gave %q two times, want a new UUID for each call", id)
	}
}

func TestHumanizeError(t *testing.T) {
	logs := plogtest.Capture(t)
	cause := errors.New("open /phenix/x: permission denied")

	h := util.HumanizeError(cause, "Unable to read %s", "x")

	if got := h.Error(); got != cause.Error() {
		t.Errorf("HumanizeError(%v).Error() = %q, want %q", cause, got, cause.Error())
	}

	if !errors.Is(h, cause) {
		t.Errorf("errors.Is(HumanizeError(%v), cause) = false, want true", cause)
	}

	want := fmt.Sprintf("Unable to read x (search error logs for %s)", h.UUID())
	if got := h.Humanize(); got != want {
		t.Errorf("Humanize() = %q, want %q", got, want)
	}

	if got := h.Humanized().Error(); got != want {
		t.Errorf("Humanized().Error() = %q, want %q", got, want)
	}

	records := logs.Records(t, plogtest.Message(cause.Error()))
	if len(records) != 1 || records[0]["uuid"] != h.UUID() {
		t.Errorf("HumanizeError() logged %v, want one record with uuid %q", records, h.UUID())
	}

	// a wrapped HumanizedError comes back as is, and is not logged again
	again := util.HumanizeError(fmt.Errorf("starting: %w", h), "Other text")
	if again != h {
		t.Errorf("HumanizeError(wrapped %p) = %p, want the wrapped error", h, again)
	}

	if got := len(logs.Records(t, nil)); got != 1 {
		t.Errorf("HumanizeError() of a HumanizedError logged %d records in all, want 1", got)
	}
}

// Without a description, Humanize gives the cause with its first letter in upper case.
func TestHumanizeWithoutDescription(t *testing.T) {
	plogtest.Capture(t)

	tests := []struct {
		cause string
		want  string
	}{
		{cause: "permission denied", want: "Permission denied"},
		{cause: "x", want: "X"},
		{cause: "Already upper", want: "Already upper"},
		{cause: "2 errors occurred", want: "2 errors occurred"},
		{cause: "", want: ""},
		{cause: " leading space", want: " leading space"},
		{cause: "état inconnu", want: "État inconnu"},
	}

	for _, tt := range tests {
		t.Run(tt.cause, func(t *testing.T) {
			h := util.HumanizeError(errors.New(tt.cause), "")

			if diff := cmp.Diff(tt.want, h.Humanize()); diff != "" {
				t.Errorf("HumanizeError(%q, \"\").Humanize() mismatch (-want +got):\n%s", tt.cause, diff)
			}
		})
	}
}

// A nil cause does not make HumanizeError, Error or Humanize panic.
func TestHumanizeNilCause(t *testing.T) {
	plogtest.Capture(t)

	tests := []struct {
		desc      string
		wantError string
		wantHuman string
	}{
		{desc: "", wantError: "unknown error", wantHuman: "Unknown error"},
		{desc: "Unable to start", wantError: "Unable to start", wantHuman: "Unable to start (search error logs for %s)"},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			h := util.HumanizeError(nil, "%s", tt.desc)

			if got := h.Error(); got != tt.wantError {
				t.Errorf("HumanizeError(nil, %q).Error() = %q, want %q", tt.desc, got, tt.wantError)
			}

			want := tt.wantHuman
			if strings.Contains(want, "%s") {
				want = fmt.Sprintf(want, h.UUID())
			}

			if got := h.Humanize(); got != want {
				t.Errorf("HumanizeError(nil, %q).Humanize() = %q, want %q", tt.desc, got, want)
			}

			if got := h.Unwrap(); got != nil {
				t.Errorf("HumanizeError(nil, %q).Unwrap() = %v, want nil", tt.desc, got)
			}
		})
	}
}
