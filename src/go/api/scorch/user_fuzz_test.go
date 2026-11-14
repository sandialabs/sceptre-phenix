package scorch

import (
	"encoding/json"
	"strings"
	"testing"

	"phenix/api/scorch/scorchmd"
)

// For any experiment as JSON, setComponentMetadata sets the metadata of the
// first component of each app with the name, and changes nothing else.
func FuzzSetComponentMetadata(f *testing.F) {
	for _, seed := range []string{
		`{}`,
		`{"spec":{"scenario":{"apps":"scorch"}}}`,
		`{"spec":{"scenario":{"apps":[null,"x",{"metadata":{"components":"probe"}}]}}}`,
		`{"spec":{"scenario":{"apps":[{"metadata":{"components":[{"name":"probe"},{"name":"probe","metadata":1}]}}]}}}`,
		`{"spec":{"scenario":{"apps":[{"metadata":{"components":[{"name":5},"probe",{"name":"other"}]}}]}}}`,
	} {
		f.Add(seed, "probe")
	}

	md := scorchmd.ComponentMetadata{"new": "value"}

	f.Fuzz(func(t *testing.T, body, name string) {
		var exp map[string]any
		if json.Unmarshal([]byte(body), &exp) != nil || exp == nil {
			return
		}

		before := userCloneJSON(exp).(map[string]any) //nolint:forcetypeassert // a clone of a map
		setComponentMetadata(exp, name, md)

		if err := userCheckMetadata(before, exp, name, md); err != nil {
			t.Error(err)
		}
	})
}

// For any component STDERR, processLogChannel logs the message of each JSON
// entry with a level, and the text of each other line.
func FuzzProcessLogChannel(f *testing.F) {
	for _, seed := range []string{
		`{"level":"INFO","msg":"hello"}`,
		"plain text\nmore text\r",
		`{"level":"ERROR","msg":"boom","traceback":"line 1"}`,
		"{\"level\":\"INFO\",\"msg\":\"one\",\"part\":1,\"parts\":2}\n{\"level\":\"INFO\",\"msg\":\"two\",\"part\":2,\"parts\":2}",
		"{\"level\":\"INFO\",\"msg\":\"one\",\"part\":1,\"parts\":3}\ntext",
		`{"level":5,"msg":"not a level"}`,
		`{"msg":"no level"}`,
		"{not json",
		"",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, stderr string) {
		var (
			ch   = make(chan []byte)
			done = make(chan struct{})
			msgs []string
		)

		go func() {
			defer close(done)

			processLogChannel(ch, func(level, msg string) {
				if level == "" {
					t.Errorf("processLogChannel(%q) logged %q with no level", stderr, msg)
				}

				msgs = append(msgs, msg)
			})
		}()

		lines := strings.Split(stderr, "\n")
		for _, line := range lines {
			ch <- []byte(line)
		}

		close(ch)
		<-done

		logged := strings.Join(msgs, "\n")

		for _, line := range lines {
			var entry struct {
				Level     string `json:"level"`
				Message   string `json:"msg"`
				Traceback string `json:"traceback"`
				Part      any    `json:"part"`
				Parts     any    `json:"parts"`
			}

			want := strings.TrimRight(line, "\r\n")
			if strings.HasPrefix(line, "{") && json.Unmarshal([]byte(line), &entry) == nil && entry.Level != "" {
				want = entry.Message
			}

			if !strings.Contains(logged, want) {
				t.Errorf("processLogChannel(%q) logged %q, want it to hold %q", stderr, msgs, want)
			}
		}
	})
}
