package plog

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

// LogEntry decodes a log line: a number or a text time, the level, type and
// message, the other keys after the message and the traceback at the end.
func TestLogEntryUnmarshalJSON(t *testing.T) {
	const micros = 1_788_000_000_123_000

	local, err := time.ParseInLocation(TimestampFormat, "2026-08-26 10:00:00.123", time.Local) //nolint:gosmopolitan // the writer's zone
	if err != nil {
		t.Fatalf("time.ParseInLocation() error = %v", err)
	}

	rfc := time.Date(2026, 8, 26, 10, 0, 0, 500_000_000, time.UTC)

	tests := []struct {
		name string
		data string
		want LogEntry

		wantErr bool
		// the error wraps a *time.ParseError
		wantParseErr bool
	}{
		{
			name: "number time",
			data: `{"time":1788000000123000,"level":"INFO","type":"SYSTEM","msg":"hi"}`,
			want: LogEntry{
				Time: micros, Timestamp: time.UnixMicro(micros).Format(TimestampFormat),
				Level: "INFO", Type: "SYSTEM", Message: "hi ",
			},
		},
		{
			name: "local text time",
			data: `{"time":"2026-08-26 10:00:00.123","msg":"hi"}`,
			want: LogEntry{Time: local.UnixMicro(), Timestamp: "2026-08-26 10:00:00.123", Message: "hi "},
		},
		{
			name: "RFC 3339 text time",
			data: `{"time":"2026-08-26T10:00:00.5Z","msg":"hi"}`,
			want: LogEntry{Time: rfc.UnixMicro(), Timestamp: "2026-08-26 10:00:00.500", Message: "hi "},
		},
		{
			name: "other keys and a traceback",
			data: `{"msg":"boom","b":1,"c":"x","@timestamp":"t","traceback":"line1\nline2"}`,
			want: LogEntry{Message: "boom b=1 c=x\nline1\nline2"},
		},
		{
			name: "traceback not text",
			data: `{"msg":"boom","traceback":5}`,
			want: LogEntry{Message: "boom "},
		},
		{name: "time text in no format", data: `{"time":"yesterday"}`, wantErr: true, wantParseErr: true},
		{name: "time not a number or text", data: `{"time":true}`, wantErr: true},
		{name: "level not text", data: `{"level":1}`, wantErr: true},
		{name: "type not text", data: `{"type":["SYSTEM"]}`, wantErr: true},
		{name: "msg not text", data: `{"msg":null}`, wantErr: true},
		{name: "list", data: `[{"msg":"hi"}]`, wantErr: true},
		{name: "text", data: `"hi"`, wantErr: true},
		{name: "null", data: `null`, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var got LogEntry

			err := json.Unmarshal([]byte(test.data), &got)

			if (err != nil) != test.wantErr {
				t.Fatalf("json.Unmarshal(%s) error = %v, want error %t", test.data, err, test.wantErr)
			}

			var parseErr *time.ParseError
			if errors.As(err, &parseErr) != test.wantParseErr {
				t.Errorf("json.Unmarshal(%s) error = %v, want a *time.ParseError %t", test.data, err, test.wantParseErr)
			}

			if test.wantErr {
				return
			}

			if diff := cmp.Diff(test.want, got); diff != "" {
				t.Errorf("json.Unmarshal(%s) mismatch (-want +got):\n%s", test.data, diff)
			}
		})
	}
}
