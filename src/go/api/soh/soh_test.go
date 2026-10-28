package soh

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"unicode"

	"github.com/mitchellh/mapstructure"
	"pgregory.net/rapid"
)

func TestHostStateAllStatesIncludesFiles(t *testing.T) {
	t.Parallel()

	networking := State{Success: "network configured"}
	file := State{Success: "file found"}
	fileAbsent := State{Success: "file not found"}
	service := State{Success: "service running"}
	process := State{Success: "process running"}
	container := State{Success: "container running"}

	state := HostState{
		Networking:  []State{networking},
		Files:       []State{file},
		FilesAbsent: []State{fileAbsent},
		Services:    []State{service},
		Processes:   []State{process},
		Containers:  []State{container},
	}

	want := []State{networking, file, fileAbsent, service, process, container}
	if got := state.AllStates(); !reflect.DeepEqual(got, want) {
		t.Fatalf("AllStates() = %#v, want %#v", got, want)
	}
}

func TestSOHMetadataDecodesHostFiles(t *testing.T) {
	t.Parallel()

	input := map[string]any{
		"hostFiles": map[string][]string{
			"server": {"/etc/injected.conf", "/opt/data.txt"},
		},
	}

	var metadata sohMetadata
	if err := mapstructure.Decode(input, &metadata); err != nil {
		t.Fatalf("decoding metadata: %v", err)
	}

	if !reflect.DeepEqual(metadata.HostFiles, input["hostFiles"]) {
		t.Fatalf("HostFiles = %#v, want %#v", metadata.HostFiles, input["hostFiles"])
	}
}

func TestSOHMetadataDecodesHostFilesAbsent(t *testing.T) {
	t.Parallel()

	input := map[string]any{
		"hostFilesAbsent": map[string][]string{
			"server": {"/tmp/should-not-exist.txt", "/opt/removed.txt"},
		},
	}

	var metadata sohMetadata
	if err := mapstructure.Decode(input, &metadata); err != nil {
		t.Fatalf("decoding metadata: %v", err)
	}

	if !reflect.DeepEqual(metadata.HostFilesAbsent, input["hostFilesAbsent"]) {
		t.Fatalf(
			"HostFilesAbsent = %#v, want %#v",
			metadata.HostFilesAbsent,
			input["hostFilesAbsent"],
		)
	}
}

func TestSOHMetadataDecodesDockerContainers(t *testing.T) {
	t.Parallel()

	input := map[string]any{
		"dockerContainers": map[string][]string{
			"server": {"nginx", "redis"},
		},
	}

	var metadata sohMetadata
	if err := mapstructure.Decode(input, &metadata); err != nil {
		t.Fatalf("decoding metadata: %v", err)
	}

	if !reflect.DeepEqual(metadata.HostContainers, input["dockerContainers"]) {
		t.Fatalf(
			"HostContainers = %#v, want %#v",
			metadata.HostContainers,
			input["dockerContainers"],
		)
	}
}

func TestFileCheckCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		osType string
		path   string
		want   string
	}{
		{
			name:   "linux",
			osType: "linux",
			path:   "/opt/user's file",
			want:   `stat -c present -- '/opt/user'"'"'s file'`,
		},
		{
			name:   "windows",
			osType: "windows",
			path:   `C:\Users\O'Brien\data.txt`,
			want:   `powershell -NoProfile -Command "if (Test-Path -LiteralPath 'C:\Users\O''Brien\data.txt' -PathType Leaf) { 'present' }"`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := fileCheckCommand(test.osType, test.path); got != test.want {
				t.Fatalf("fileCheckCommand() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestSOHMetadataDecodesHostServices(t *testing.T) {
	t.Parallel()

	input := map[string]any{
		"hostServices": map[string][]string{
			"server": {"sshd", "nginx"},
		},
	}

	var metadata sohMetadata
	if err := mapstructure.Decode(input, &metadata); err != nil {
		t.Fatalf("decoding metadata: %v", err)
	}

	if !reflect.DeepEqual(metadata.HostServices, input["hostServices"]) {
		t.Fatalf("HostServices = %#v, want %#v", metadata.HostServices, input["hostServices"])
	}
}

func TestServiceCheckCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		osType  string
		service string
		want    string
	}{
		{
			name:    "linux",
			osType:  "linux",
			service: "user's service",
			want:    `systemctl is-active -- 'user'"'"'s service'`,
		},
		{
			name:    "windows",
			osType:  "windows",
			service: `O'Brien Service`,
			want:    `powershell -NoProfile -Command "if ((Get-Service -Name 'O''Brien Service' -ErrorAction SilentlyContinue).Status -eq 'Running') { 'active' }"`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := serviceCheckCommand(test.osType, test.service); got != test.want {
				t.Fatalf("serviceCheckCommand() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestContainerCheckCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		osType    string
		container string
		want      string
	}{
		{
			name:      "linux",
			osType:    "linux",
			container: "o'brien's-app",
			want: `docker inspect --format='{{if .State.Health}}{{.State.Health.Status}}` +
				`{{else}}{{if .State.Running}}running{{else}}{{.State.Status}}{{end}}{{end}}' ` +
				`-- 'o'"'"'brien'"'"'s-app'`,
		},
		{
			name:      "windows",
			osType:    "windows",
			container: "o'brien's-app",
			want: `powershell -NoProfile -Command "docker inspect --format='` +
				`{{if .State.Health}}{{.State.Health.Status}}` +
				`{{else}}{{if .State.Running}}running{{else}}{{.State.Status}}{{end}}{{end}}' ` +
				`-- 'o''brien''s-app' 2>$null"`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := containerCheckCommand(test.osType, test.container); got != test.want {
				t.Fatalf("containerCheckCommand() = %q, want %q", got, test.want)
			}
		})
	}
}

// sohShellWords splits a POSIX shell command into its words, as sh does for
// quotes and backslashes, reporting false for anything sh would expand.
func sohShellWords(cmd string) ([]string, bool) {
	var (
		words []string
		word  strings.Builder
		in    bool // in a word
		runes = []rune(cmd)
	)

	for i := 0; i < len(runes); i++ {
		switch r := runes[i]; {
		case r == ' ' || r == '\t' || r == '\n':
			if in {
				words, in = append(words, word.String()), false
				word.Reset()
			}

			continue
		case r == '\'':
			end := slices.Index(runes[i+1:], '\'')
			if end < 0 {
				return nil, false
			}

			word.WriteString(string(runes[i+1 : i+1+end]))
			i += end + 1
		case r == '"':
			for i++; i < len(runes) && runes[i] != '"'; i++ {
				switch {
				case runes[i] == '$' || runes[i] == '`':
					return nil, false
				case runes[i] == '\\' && i+1 < len(runes) && strings.ContainsRune("$`\"\\", runes[i+1]):
					i++
				}

				word.WriteRune(runes[i])
			}

			if i == len(runes) {
				return nil, false
			}
		case r == '\\' && i+1 < len(runes):
			if i++; runes[i] != '\n' {
				word.WriteRune(runes[i])
			}
		case strings.ContainsRune("\\$`|&;<>()*?[#~{}!", r):
			return nil, false
		default:
			word.WriteRune(r)
		}

		in = true
	}

	if in {
		words = append(words, word.String())
	}

	return words, true
}

// genSOHName draws a name heavy in what a shell would quote, expand or split on.
func genSOHName() *rapid.Generator[string] {
	odd := rapid.SampledFrom([]rune("'\"\\$`! \t\n*?[]{}#~;&|<>()-"))

	return rapid.StringOf(rapid.OneOf(odd, rapid.RuneFrom(nil, unicode.Letter, unicode.Digit, unicode.Space)))
}

// On Linux each check reaches its command as one argument after "--", whatever its name.
func TestCheckCommandsProperties(t *testing.T) {
	t.Parallel()

	format := "--format={{if .State.Health}}{{.State.Health.Status}}" +
		"{{else}}{{if .State.Running}}running{{else}}{{.State.Status}}{{end}}{{end}}"

	for name, tc := range map[string]struct {
		command func(osType, name string) string
		want    []string
	}{
		"file":      {fileCheckCommand, []string{"stat", "-c", "present", "--"}},
		"service":   {serviceCheckCommand, []string{"systemctl", "is-active", "--"}},
		"container": {containerCheckCommand, []string{"docker", "inspect", format, "--"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rapid.Check(t, func(t *rapid.T) {
				osType := rapid.SampledFrom([]string{"linux", "", "rhel"}).Draw(t, "os")
				arg := genSOHName().Draw(t, "name")
				cmd := tc.command(osType, arg)

				got, ok := sohShellWords(cmd)
				if want := append(slices.Clone(tc.want), arg); !ok || !slices.Equal(got, want) {
					t.Fatalf("%sCheckCommand(%q, %q) = %q, splits into %q, %t; want %q, true", name, osType, arg, cmd, got, ok, want)
				}
			})
		})
	}
}
