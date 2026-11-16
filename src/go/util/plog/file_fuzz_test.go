package plog

import (
	"encoding/json"
	"strings"
	"testing"
)

// LogEntry decodes only a JSON object, and its level, type and message agree
// with a plain decode of the same object.
func FuzzLogEntryUnmarshal(f *testing.F) {
	for _, seed := range []string{
		`{"time":1788000000123000,"level":"INFO","type":"SYSTEM","msg":"hi"}`,
		`{"time":"2026-08-26 10:00:00.123","msg":"hi"}`,
		`{"time":"2026-08-26T10:00:00.5Z","msg":"hi"}`,
		`{"msg":"boom","b":1,"c":"x","@timestamp":"t","traceback":"line1\nline2"}`,
		`{"msg":"boom","traceback":5}`,
		`{"level":"a","level":"b","msg":"x","msg":"y"}`,
		`{"time":"yesterday"}`, `{"time":true}`, `{"level":1}`, `{"type":["SYSTEM"]}`, `{"msg":null}`,
		`[{"msg":"hi"}]`, `"hi"`, `null`, `{}`,
	} {
		f.Add([]byte(seed))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		var got LogEntry
		if json.Unmarshal(data, &got) != nil {
			return
		}

		var ref map[string]any
		if err := json.Unmarshal(data, &ref); err != nil || ref == nil {
			t.Fatalf("json.Unmarshal(%q) into LogEntry error = nil, but a plain decode gives %v, %v", data, ref, err)
		}

		level, _ := ref["level"].(string)
		typ, _ := ref[logAttrType].(string)
		msg, _ := ref["msg"].(string)

		if got.Level != level || got.Type != typ || !strings.HasPrefix(got.Message, msg+" ") {
			t.Fatalf("json.Unmarshal(%q) = level %q, type %q, message %q, want level %q, type %q, message %q first",
				data, got.Level, got.Type, got.Message, level, typ, msg)
		}
	})
}
