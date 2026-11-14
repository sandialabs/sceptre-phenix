package soh

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"unicode"

	"github.com/google/go-cmp/cmp"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"
	"pgregory.net/rapid"

	"phenix/util/mm"
)

//nolint:gochecknoinits // minicli keeps its command handlers in package state
func init() {
	minicli.MustRegister(&minicli.Handler{
		Patterns: []string{"cc <exec,> <command>..."},
		Call:     func(*minicli.Command, chan<- minicli.Responses) {},
	})
}

// ccExecArgv returns the words that miniccc runs for the `cc exec` text cmd.
// minicli splits the text, the minimega preprocessor expands each word and
// ccCommandPreProcess removes the stdio words. A NUL marks a changed word.
func ccExecArgv(cmd string) ([]string, error) {
	c, err := minicli.Compile("cc exec " + cmd)
	if err != nil {
		return nil, fmt.Errorf("compiling cc exec %s: %w", cmd, err)
	}

	words := slices.Clone(c.ListArgs["command"])

	for i, w := range words {
		w = os.Expand(w, func(name string) string { return "\x00env:" + name })

		if u, err := url.Parse(w); err == nil && slices.Contains([]string{"file", "http", "https", "tar"}, u.Scheme) {
			w = "\x00url:" + w
		}

		words[i] = w
	}

	for i := range min(3, len(words)) {
		if key, _, ok := strings.Cut(words[i], "="); !ok || !slices.Contains([]string{"stdin", "stdout", "stderr"}, key) {
			return words[i:], nil
		}
	}

	return words[min(3, len(words)):], nil
}

// psID makes Get-Process print only the ID of each process it finds.
const psID = " | Select-Object -ExpandProperty Id"

// psSingleQuotes are the characters that PowerShell reads as a single quote.
const psSingleQuotes = "'\u2018\u2019\u201a\u201b"

// psUnquote reads s as one PowerShell single-quoted string, as the PowerShell
// tokenizer does. It reports false when s is not exactly one such string.
func psUnquote(s string) (string, bool) {
	r := []rune(s)
	if len(r) < 2 || !strings.ContainsRune(psSingleQuotes, r[0]) {
		return "", false
	}

	var b strings.Builder

	for i := 1; i < len(r); i++ {
		if !strings.ContainsRune(psSingleQuotes, r[i]) {
			b.WriteRune(r[i])

			continue
		}

		if i+1 < len(r) && strings.ContainsRune(psSingleQuotes, r[i+1]) {
			i++
			b.WriteRune(r[i])

			continue
		}

		return b.String(), i == len(r)-1
	}

	return "", false
}

// ccExecCommand quotes each word so that miniccc runs exactly the words given,
// and refuses a word that minimega would change.
func TestCCExecCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		want    string
		wantErr bool
	}{
		{name: "bare words", args: []string{"pgrep", "-f", "--", "nginx"}, want: "pgrep -f -- nginx"},
		{name: "space", args: []string{"pgrep", "python3 server.py"}, want: "pgrep 'python3 server.py'"},
		{name: "single quote", args: []string{"pgrep", "it's"}, want: `pgrep "it's"`},
		{name: "double quote", args: []string{"pgrep", `say "hi"`}, want: `pgrep 'say "hi"'`},
		{name: "both quotes", args: []string{"pgrep", `it's "x"`}, want: `pgrep 'it\'s "x"'`},
		{name: "backslash", args: []string{"pgrep", `\d+\.py`}, want: `pgrep '\\d+\\.py'`},
		{name: "comment leader", args: []string{"pgrep", "#1"}, want: "pgrep '#1'"},
		{name: "control characters", args: []string{"pgrep", "a\nb\tc\rd"}, want: `pgrep 'a\nb\tc\rd'`},
		{name: "empty word", args: []string{"pgrep", ""}, want: "pgrep ''"},
		{name: "stdio word", args: []string{"stdout=x"}, want: "stdout=x"},
		{name: "trailing dollar", args: []string{"pgrep", "server.py$"}, want: "pgrep 'server.py$'"},
		{name: "dollar before a space", args: []string{"pgrep", "a$ b"}, want: "pgrep 'a$ b'"},
		{name: "environment variable", args: []string{"pgrep", "$HOME/app"}, wantErr: true},
		{name: "braced variable", args: []string{"pgrep", "${x}"}, wantErr: true},
		{name: "special variable", args: []string{"pgrep", "a$1"}, wantErr: true},
		{name: "file url", args: []string{"pgrep", "file:x"}, wantErr: true},
		{name: "http url", args: []string{"pgrep", "HTTP://x"}, wantErr: true},
		{name: "nul", args: []string{"pgrep", "a\x00b"}, wantErr: true},
		{name: "invalid utf-8", args: []string{"pgrep", "a\xffb"}, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := ccExecCommand(test.args...)
			if test.wantErr {
				if !errors.Is(err, errUnsafeC2Arg) {
					t.Fatalf("ccExecCommand(%q) = %q, %v, want error %v", test.args, got, err, errUnsafeC2Arg)
				}

				return
			}

			if err != nil || got != test.want {
				t.Fatalf("ccExecCommand(%q) = %q, %v, want %q, nil", test.args, got, err, test.want)
			}

			argv, err := ccExecArgv(got)
			if err != nil {
				t.Fatal(err)
			}

			want := test.args
			if test.name == "stdio word" {
				want = []string{}
			}

			if diff := cmp.Diff(want, argv); diff != "" {
				t.Errorf("ccExecArgv(%q) mismatch (-want +got):\n%s", got, diff)
			}
		})
	}
}

// Each check builds the exact cc exec command for the VM's OS, quotes the
// value, and refuses a value that cannot reach the VM unchanged.
func TestCheckCommands(t *testing.T) {
	t.Parallel()

	const (
		dockerTmpl = `{{if .State.Health}}{{.State.Health.Status}}` +
			`{{else}}{{if .State.Running}}running{{else}}{{.State.Status}}{{end}}{{end}}`
		netPrefix = `@(Get-NetTCPConnection -State Listen -ErrorAction SilentlyContinue; ` +
			`Get-NetUDPEndpoint -ErrorAction SilentlyContinue)`
	)

	tests := []struct {
		name    string
		build   func(osType, value string) (string, error)
		osType  string
		value   string
		want    string
		wantErr error
	}{
		{
			name: "process", build: procCheckCommand, osType: "linux", value: "nginx",
			want: "pgrep -f -- nginx",
		},
		{
			name: "process with a space", build: procCheckCommand, osType: "linux", value: "python3 server.py",
			want: "pgrep -f -- 'python3 server.py'",
		},
		{
			name: "process with quotes", build: procCheckCommand, osType: "linux", value: `O'Brien "x"`,
			want: `pgrep -f -- 'O\'Brien "x"'`,
		},
		{
			name: "process with an anchor", build: procCheckCommand, osType: "linux", value: `^python3 .*\.py$`,
			want: `pgrep -f -- '^python3 .*\\.py$'`,
		},
		{
			name: "process with a variable", build: procCheckCommand, osType: "linux", value: "$HOME/app",
			wantErr: errUnsafeC2Arg,
		},
		{
			name: "windows process", build: procCheckCommand, osType: "windows", value: "svchost",
			want: `powershell -NoProfile -Command "Get-Process -Name 'svchost' -ErrorAction SilentlyContinue` + psID + `"`,
		},
		{
			name: "windows process with quotes", build: procCheckCommand, osType: "Windows", value: "O'Brien\u2019s \"x\"",
			want: `powershell -NoProfile -Command 'Get-Process -Name \'O\'\'Brien` + "\u2019\u2019" +
				`s "x"\' -ErrorAction SilentlyContinue` + psID + `'`,
		},
		{
			name: "windows process with a dollar", build: procCheckCommand, osType: "windows", value: "cost$",
			want: `powershell -NoProfile -Command "Get-Process -Name 'cost$' -ErrorAction SilentlyContinue` + psID + `"`,
		},
		{
			name: "windows process with a variable", build: procCheckCommand, osType: "windows", value: "$env:x",
			wantErr: errUnsafeC2Arg,
		},
		{
			name: "file", build: fileCheckCommand, osType: "linux", value: "/opt/user's file",
			want: `stat -c present -- "/opt/user's file"`,
		},
		{
			name: "windows file", build: fileCheckCommand, osType: "windows", value: `C:\Users\O'Brien\data.txt`,
			want: `powershell -NoProfile -Command "if (Test-Path -LiteralPath 'C:\\Users\\O''Brien\\data.txt' ` +
				`-PathType Leaf) { 'present' }"`,
		},
		{
			name: "windows file with a variable", build: fileCheckCommand, osType: "windows", value: `C:\$Recycle.Bin\x`,
			wantErr: errUnsafeC2Arg,
		},
		{
			name: "service", build: serviceCheckCommand, osType: "linux", value: "user's service",
			want: `systemctl is-active -- "user's service"`,
		},
		{
			name: "windows service", build: serviceCheckCommand, osType: "windows", value: "O'Brien Service",
			want: `powershell -NoProfile -Command "if ((Get-Service -Name 'O''Brien Service' ` +
				`-ErrorAction SilentlyContinue).Status -eq 'Running') { 'active' }"`,
		},
		{
			name: "container", build: containerCheckCommand, osType: "linux", value: "o'brien's-app",
			want: `docker inspect '--format=` + dockerTmpl + `' -- "o'brien's-app"`,
		},
		{
			name: "windows container", build: containerCheckCommand, osType: "windows", value: "o'brien's-app",
			want: `powershell -NoProfile -Command "docker inspect --format='` + dockerTmpl +
				`' -- 'o''brien''s-app' 2>&1 | Where-Object -Property Exception -Not"`,
		},
		{
			name: "port", build: portCheckCommand, osType: "linux", value: "22",
			want: "ss -lntu state all 'sport = 22'",
		},
		{
			name: "any address", build: portCheckCommand, osType: "linux", value: ":22",
			want: "ss -lntu state all 'sport = 22'",
		},
		{
			name: "address", build: portCheckCommand, osType: "linux", value: "10.0.0.1:",
			want: "ss -lntu state all 'src = 10.0.0.1'",
		},
		{
			name: "address and port", build: portCheckCommand, osType: "linux", value: "10.0.0.1:443",
			want: "ss -lntu state all 'src = 10.0.0.1 and sport = 443'",
		},
		{
			name: "windows port", build: portCheckCommand, osType: "windows", value: ":22",
			want: `powershell -NoProfile -Command '` + netPrefix +
				` | Where-Object LocalPort -eq 22 | Select-Object -First 1'`,
		},
		{
			name: "windows address and port", build: portCheckCommand, osType: "windows", value: "10.0.0.1:443",
			want: `powershell -NoProfile -Command "` + netPrefix +
				` | Where-Object LocalAddress -eq '10.0.0.1' | Where-Object LocalPort -eq 443 | Select-Object -First 1"`,
		},
		{
			name: "windows address and named port", build: portCheckCommand, osType: "windows", value: "host-1:http",
			want: `powershell -NoProfile -Command "` + netPrefix +
				` | Where-Object LocalAddress -eq 'host-1' | Where-Object LocalPort -eq 'http' | Select-Object -First 1"`,
		},
		{
			name: "listener with a space", build: portCheckCommand, osType: "linux", value: "80 or sport = 22",
			wantErr: errUnsafeC2Arg,
		},
		{
			name: "listener with a quote", build: portCheckCommand, osType: "windows", value: "10.0.0.1':80",
			wantErr: errUnsafeC2Arg,
		},
		{
			name: "listener with a dollar", build: portCheckCommand, osType: "linux", value: "$PORT",
			wantErr: errUnsafeC2Arg,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := test.build(test.osType, test.value)
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("build(%q, %q) = %q, %v, want error %v", test.osType, test.value, got, err, test.wantErr)
				}

				return
			}

			if err != nil || got != test.want {
				t.Fatalf("build(%q, %q) = %q, %v, want %q, nil", test.osType, test.value, got, err, test.want)
			}
		})
	}
}

// An empty process pattern or listener, or a listener of more than two parts,
// is refused.
func TestCheckCommandsRefuseInvalidValues(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		build func(osType, value string) (string, error)
		value string
	}{
		{procCheckCommand, ""},
		{portCheckCommand, ""},
		{portCheckCommand, ":"},
		{portCheckCommand, "1:2:3"},
	} {
		for _, osType := range []string{"linux", "windows"} {
			if got, err := test.build(osType, test.value); err == nil {
				t.Errorf("build(%q, %q) = %q, nil, want an error", osType, test.value, got)
			}
		}
	}
}

// linuxCheck names a Linux check command and the words that come before the
// value in the command that miniccc runs.
type linuxCheck struct {
	build  func(osType, value string) (string, error)
	prefix []string
}

func linuxChecks() map[string]linuxCheck {
	format := "--format={{if .State.Health}}{{.State.Health.Status}}" +
		"{{else}}{{if .State.Running}}running{{else}}{{.State.Status}}{{end}}{{end}}"

	return map[string]linuxCheck{
		"process":   {procCheckCommand, []string{"pgrep", "-f", "--"}},
		"file":      {fileCheckCommand, []string{"stat", "-c", "present", "--"}},
		"service":   {serviceCheckCommand, []string{"systemctl", "is-active", "--"}},
		"container": {containerCheckCommand, []string{"docker", "inspect", format, "--"}},
	}
}

// checkRoundTrip reports how the command for value fails to reach miniccc
// as the words prefix and value. A refused value must be one that minimega
// changes, which ccExecArgv marks with a NUL.
func checkRoundTrip(c linuxCheck, osType, value string) error {
	cmd, err := c.build(osType, value)
	if err != nil {
		if !errors.Is(err, errUnsafeC2Arg) && value != "" {
			return fmt.Errorf("build(%q, %q) error = %w, want nil or %w", osType, value, err, errUnsafeC2Arg)
		}

		return nil
	}

	got, err := ccExecArgv(cmd)
	if err != nil {
		return fmt.Errorf("build(%q, %q) = %q, which minicli refuses: %w", osType, value, cmd, err)
	}

	if diff := cmp.Diff(append(slices.Clone(c.prefix), value), got); diff != "" {
		return fmt.Errorf("build(%q, %q) = %q, argv mismatch (-want +got):\n%s", osType, value, cmd, diff)
	}

	return nil
}

// windowsRoundTrip reports how the Windows process command for value fails to
// reach PowerShell with value as its -Name string.
func windowsRoundTrip(value string) error {
	cmd, err := procCheckCommand(osTypeWindows, value)
	if err != nil {
		if !errors.Is(err, errUnsafeC2Arg) && value != "" {
			return fmt.Errorf("procCheckCommand(windows, %q) error = %w, want nil or %w", value, err, errUnsafeC2Arg)
		}

		return nil
	}

	got, err := ccExecArgv(cmd)
	if err != nil {
		return fmt.Errorf("procCheckCommand(windows, %q) = %q, which minicli refuses: %w", value, cmd, err)
	}

	quoted := psQuote(value)
	want := []string{"powershell", "-NoProfile", "-Command", "Get-Process -Name " + quoted + " -ErrorAction SilentlyContinue" + psID}

	if diff := cmp.Diff(want, got); diff != "" {
		return fmt.Errorf("procCheckCommand(windows, %q) = %q, argv mismatch (-want +got):\n%s", value, cmd, diff)
	}

	if name, ok := psUnquote(quoted); !ok || name != value {
		return fmt.Errorf("psUnquote(psQuote(%q)) = %q, %t, want %q, true", value, name, ok, value)
	}

	return nil
}

// genCheckValue draws a value heavy in what minicli, minimega or PowerShell
// would quote, escape, expand or split on.
func genCheckValue() *rapid.Generator[string] {
	odd := rapid.SampledFrom([]rune("'\"\\$`! \t\n\r#{}:/=-\u2018\u2019\u201a\u201b"))

	return rapid.StringOf(rapid.OneOf(odd, rapid.RuneFrom(nil, unicode.Letter, unicode.Digit, unicode.Space)))
}

// Each Linux check reaches miniccc as its words and then the value as one
// word, and each Windows process pattern reaches PowerShell unchanged.
func TestCheckCommandsProperties(t *testing.T) {
	t.Parallel()

	for name, c := range linuxChecks() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rapid.Check(t, func(t *rapid.T) {
				osType := rapid.SampledFrom([]string{"linux", "", "rhel"}).Draw(t, "os")
				if err := checkRoundTrip(c, osType, genCheckValue().Draw(t, "value")); err != nil {
					t.Fatal(err)
				}
			})
		})
	}

	t.Run("windows process", func(t *testing.T) {
		t.Parallel()

		rapid.Check(t, func(t *rapid.T) {
			if err := windowsRoundTrip(genCheckValue().Draw(t, "value")); err != nil {
				t.Fatal(err)
			}
		})
	})
}

// pingCommand pings once with the count flag of the node's OS, and refuses a
// target that minimega would change.
func TestPingCommand(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		os, target, want string
	}{
		{os: "linux", target: "10.0.0.1", want: "ping -c 1 10.0.0.1"},
		{os: "windows", target: "10.0.0.1", want: "ping -n 1 10.0.0.1"},
		{os: "linux", target: "a b", want: "ping -c 1 'a b'"},
	} {
		got, err := pingCommand(sohNode("web", tc.os), tc.target)
		if err != nil || got != tc.want {
			t.Errorf("pingCommand(%s, %q) = %q, %v, want %q, nil", tc.os, tc.target, got, err, tc.want)
		}
	}

	for _, target := range []string{"$GATEWAY", "${IP}", "file:x"} {
		if got, err := pingCommand(sohNode("web", "linux"), target); !errors.Is(err, mm.ErrUnsafeC2Arg) {
			t.Errorf("pingCommand(linux, %q) = %q, %v, want %v", target, got, err, mm.ErrUnsafeC2Arg)
		}
	}
}
