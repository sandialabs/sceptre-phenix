package soh

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/util/plog"
)

// TestSohLoggerAddsExperimentName checks that soh log records carry the
// experiment name and keep the attributes of the logger in the context, such
// as the key the scorch SoH component uses to collect the output.
func TestSohLoggerAddsExperimentName(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer

	base := slog.New(slog.NewJSONHandler(&logs, nil)).With(plog.ScorchSohKey, "exp-1-0-soh")
	ctx := plog.ContextWithLogger(t.Context(), base, plog.TypeScorch)

	sohLogger(ctx, "exp-1").Warn("skipping packet capture for host", hostKey, "rtu")

	var record map[string]any
	if err := json.Unmarshal(logs.Bytes(), &record); err != nil {
		t.Fatalf("decoding soh log record %q: %v", logs.String(), err)
	}

	want := map[string]any{
		plog.ScorchSohKey: "exp-1-0-soh",
		expKey:            "exp-1",
		hostKey:           "rtu",
	}

	got := map[string]any{
		plog.ScorchSohKey: record[plog.ScorchSohKey],
		expKey:            record[expKey],
		hostKey:           record[hostKey],
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("soh log record attributes mismatch (-want +got):\n%s", diff)
	}
}
