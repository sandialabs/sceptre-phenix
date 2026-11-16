package cmd

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"
	"github.com/spf13/cobra"
	"pgregory.net/rapid"

	"phenix/store"
	"phenix/store/storetest"
	"phenix/util/mm/mmtest"
)

// parseVMExecArgs takes the experiment and the VM, then the command, with or
// without "--" after the VM.
func TestParseVMExecArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		args     []string
		dash     int
		wantExp  string
		wantVM   string
		wantArgv []string
		wantErr  bool
	}{
		{
			name:     "no dash",
			args:     []string{"exp", "vm1", "hostname"},
			dash:     -1,
			wantExp:  "exp",
			wantVM:   "vm1",
			wantArgv: []string{"hostname"},
		},
		{name: "dash", args: []string{"exp", "vm1", "ls", "-la"}, dash: 2, wantExp: "exp", wantVM: "vm1", wantArgv: []string{"ls", "-la"}},
		{name: "dash too early", args: []string{"exp", "vm1", "ls"}, dash: 1, wantErr: true},
		{name: "dash too late", args: []string{"exp", "vm1", "ls"}, dash: 3, wantErr: true},
		{name: "no command", args: []string{"exp", "vm1"}, dash: -1, wantErr: true},
		{name: "no command after dash", args: []string{"exp", "vm1"}, dash: 2, wantErr: true},
		{name: "empty VM", args: []string{"exp", "", "ls"}, dash: -1, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			exp, vmName, argv, err := parseVMExecArgs(test.args, test.dash)
			if test.wantErr {
				if !errors.Is(err, errVMExecArgs) {
					t.Errorf("parseVMExecArgs(%q, %d) error = %v, want %v", test.args, test.dash, err, errVMExecArgs)
				}

				return
			}

			if err != nil || exp != test.wantExp || vmName != test.wantVM || !slices.Equal(argv, test.wantArgv) {
				t.Errorf("parseVMExecArgs(%q, %d) = %q, %q, %q, %v, want %q, %q, %q, nil",
					test.args, test.dash, exp, vmName, argv, err, test.wantExp, test.wantVM, test.wantArgv)
			}
		})
	}
}

// parseVMExecArgs returns every argument after the experiment and the VM as
// the command, unchanged.
func TestParseVMExecArgsProperty(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		exp := rapid.StringMatching(`[a-z][a-z0-9_-]{0,10}`).Draw(t, "exp")
		vmName := rapid.StringMatching(`[A-Za-z][A-Za-z0-9_.-]{0,10}`).Draw(t, "vm")
		argv := rapid.SliceOfN(rapid.String(), 1, 5).Draw(t, "argv")
		dash := rapid.SampledFrom([]int{-1, 2}).Draw(t, "dash")

		args := append([]string{exp, vmName}, argv...)

		gotExp, gotVM, gotArgv, err := parseVMExecArgs(args, dash)
		if err != nil || gotExp != exp || gotVM != vmName || !slices.Equal(gotArgv, argv) {
			t.Fatalf("parseVMExecArgs(%q, %d) = %q, %q, %q, %v, want %q, %q, %q, nil",
				args, dash, gotExp, gotVM, gotArgv, err, exp, vmName, argv)
		}

		if other := rapid.IntRange(0, len(args)).Draw(t, "other dash"); other != 2 {
			if _, _, _, err := parseVMExecArgs(args, other); !errors.Is(err, errVMExecArgs) {
				t.Fatalf("parseVMExecArgs(%q, %d) error = %v, want %v", args, other, err, errVMExecArgs)
			}
		}
	})
}

// parseVMCopyArgs takes exactly one side in the form <vm>:<path>.
func TestParseVMCopyArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		src, dst string
		want     vmCopy
		wantErr  bool
	}{
		{
			name: "into the VM",
			src:  "./setup.sh",
			dst:  "vm1:/root/",
			want: vmCopy{vm: "vm1", guest: "/root/", local: "./setup.sh", toVM: true},
		},
		{
			name: "out of the VM",
			src:  "vm1:/etc/hosts",
			dst:  "hosts",
			want: vmCopy{vm: "vm1", guest: "/etc/hosts", local: "hosts", toVM: false},
		},
		{name: "a Windows path", src: "a.ps1", dst: `win:C:\x.ps1`, want: vmCopy{vm: "win", guest: `C:\x.ps1`, local: "a.ps1", toVM: true}},
		{name: "a local path with a colon", src: "./a:b", dst: "vm1:/x", want: vmCopy{vm: "vm1", guest: "/x", local: "./a:b", toVM: true}},
		{name: "both in VMs", src: "vm1:/a", dst: "vm2:/b", wantErr: true},
		{name: "neither in a VM", src: "/a", dst: "./b", wantErr: true},
		{name: "an empty VM path", src: "a", dst: "vm1:", wantErr: true},
		{name: "an empty local path", src: "vm1:/a", dst: "", wantErr: true},
		{name: "a colon first", src: ":/a", dst: "b", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseVMCopyArgs(test.src, test.dst)
			if test.wantErr {
				if !errors.Is(err, errVMCopyArgs) {
					t.Errorf("parseVMCopyArgs(%q, %q) error = %v, want %v", test.src, test.dst, err, errVMCopyArgs)
				}

				return
			}

			if err != nil {
				t.Fatalf("parseVMCopyArgs(%q, %q) error = %v", test.src, test.dst, err)
			}

			if diff := cmp.Diff(test.want, got, cmp.AllowUnexported(vmCopy{})); diff != "" {
				t.Errorf("parseVMCopyArgs(%q, %q) mismatch (-want +got):\n%s", test.src, test.dst, diff)
			}
		})
	}
}

// A <vm>:<path> side parses back to its VM and path, in either position, and
// a local path with a "/" before any ":" is never a VM side.
func TestParseVMCopyArgsProperty(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		vmName := rapid.StringMatching(`[A-Za-z0-9_.-]{1,10}`).Draw(t, "vm")
		guest := rapid.StringN(1, 20, -1).Draw(t, "guest")
		local := rapid.SampledFrom([]string{"/", "./", "../"}).Draw(t, "local start") + rapid.String().Draw(t, "local")

		want := vmCopy{vm: vmName, guest: guest, local: local, toVM: true}

		got, err := parseVMCopyArgs(local, vmName+":"+guest)
		if err != nil || got != want {
			t.Fatalf("parseVMCopyArgs(%q, %q) = %+v, %v, want %+v, nil", local, vmName+":"+guest, got, err, want)
		}

		want.toVM = false

		got, err = parseVMCopyArgs(vmName+":"+guest, local)
		if err != nil || got != want {
			t.Fatalf("parseVMCopyArgs(%q, %q) = %+v, %v, want %+v, nil", vmName+":"+guest, local, got, err, want)
		}

		if _, err := parseVMCopyArgs(local, local); !errors.Is(err, errVMCopyArgs) {
			t.Fatalf("parseVMCopyArgs(%q, %q) error = %v, want %v", local, local, err, errVMCopyArgs)
		}
	})
}

// vm exec and vm cp refuse bad arguments and a timeout that is not positive,
// before they read the store or minimega.
func TestVMExecAndCopyRefuseArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cmd  func() *cobra.Command
		args []string
		want string
	}{
		{name: "exec without a command", cmd: newVMExecCmd, args: []string{"exp", "vm1"}, want: "expected <experiment>"},
		{
			name: "exec with a zero timeout",
			cmd:  newVMExecCmd,
			args: []string{"--timeout", "0s", "exp", "vm1", "ls"},
			want: "--timeout must be more than 0",
		},
		{
			name: "exec with flags before the dash",
			cmd:  newVMExecCmd,
			args: []string{"exp", "--", "vm1", "ls"},
			want: "only the experiment and the VM",
		},
		{name: "cp with two arguments", cmd: newVMCopyCmd, args: []string{"exp", "a"}, want: "received 2 arguments"},
		{name: "cp with no VM", cmd: newVMCopyCmd, args: []string{"exp", "/a", "/b"}, want: "neither"},
		{
			name: "cp with a negative timeout",
			cmd:  newVMCopyCmd,
			args: []string{"--timeout", "-1s", "exp", "/a", "vm1:/b"},
			want: "--timeout must be more than 0",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			cmd := test.cmd()
			cmd.SetArgs(test.args)
			cmd.SilenceUsage, cmd.SilenceErrors = true, true

			var out bytes.Buffer

			cmd.SetOut(&out)
			cmd.SetErr(&out)

			if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Errorf("%s %q error = %v, want one that contains %q", cmd.Name(), test.args, err, test.want)
			}
		})
	}
}

// vmExecCmdMinimega answers the commands of vm exec for a running VM vm1 whose
// command prints out and err and exits with code.
func vmExecCmdMinimega(out, errOut, code string) mmtest.Handler {
	const uuid = "0a1b2c3d-0000-4000-8000-000000000002"

	return func(cmd mmtest.Command) []*minicli.Response {
		switch {
		case cmd.Base == "vm info":
			return []*minicli.Response{mmtest.Tabular("head", []string{"host", "name", "uuid", "state"},
				[]string{"head", "vm1", uuid, "RUNNING"})}
		case cmd.Base == "cc client":
			return []*minicli.Response{mmtest.Tabular("head", []string{"host", "uuid", "hostname"},
				[]string{"head", uuid, "vm1"})}
		case cmd.Base == "cc commands":
			return []*minicli.Response{mmtest.Tabular("head", []string{"host", "id", "responses", "filter"},
				[]string{"head", "1", "1", "uuid=" + uuid})}
		case strings.HasPrefix(cmd.Base, "cc exec "):
			return []*minicli.Response{{Host: "head", Data: 1}}
		case cmd.Base == "cc response 1":
			return []*minicli.Response{mmtest.Text("head", "1/"+uuid+"/stdout:\n"+out+"\n1/"+uuid+"/stderr:\n"+errOut+"\n")}
		case strings.HasPrefix(cmd.Base, "cc exitcode 1 "):
			return []*minicli.Response{mmtest.Text("head", code)}
		}

		return nil
	}
}

// vm exec prints the command's stdout and stderr, and exits with its code.
func TestVMExecCmd(t *testing.T) { //nolint:paralleltest // replaces the store and minimega
	db := storetest.UseBolt(t)

	exp := &store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Experiment",
		Metadata: store.ConfigMetadata{Name: "exp"},
		Spec: map[string]any{
			"experimentName": "exp",
			"topology": map[string]any{"nodes": []any{map[string]any{
				"type": "VirtualMachine", "general": map[string]any{"hostname": "vm1"},
				"hardware": map[string]any{"os_type": "linux"},
			}}},
		},
		Status: map[string]any{"startTime": "2026-01-01T00:00:00Z"},
	}

	if err := db.Create(exp); err != nil {
		t.Fatalf("store.Create(experiment/exp) error = %v", err)
	}

	for _, test := range []struct {
		code     string
		wantCode int
	}{
		{code: "0", wantCode: 0},
		{code: "7", wantCode: 7},
		{code: "-1", wantCode: 1},
	} {
		t.Run("exit code "+test.code, func(t *testing.T) {
			received := mmtest.Use(t, vmExecCmdMinimega("hello", "warning", test.code))

			cmd := newVMExecCmd()
			cmd.SetArgs([]string{"exp", "vm1", "--", "ls", "-la"})
			cmd.SilenceUsage, cmd.SilenceErrors = true, true

			var stdout, stderr bytes.Buffer

			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)

			err := cmd.Execute()

			code := 0
			if err != nil {
				code = exitCode(err)
			}

			if code != test.wantCode {
				t.Errorf("vm exec exit code = %d (%v), want %d", code, err, test.wantCode)
			}

			if stdout.String() != "hello\n" || stderr.String() != "warning\n" {
				t.Errorf("vm exec printed %q and %q, want %q and %q", stdout.String(), stderr.String(), "hello\n", "warning\n")
			}

			if got := mmtest.Count(received(), "cc exec ls -la"); got != 1 {
				t.Errorf("vm exec sent %d cc exec ls -la commands, want 1", got)
			}
		})
	}
}
