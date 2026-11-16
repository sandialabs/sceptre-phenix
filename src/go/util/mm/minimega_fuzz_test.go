package mm

import (
	"errors"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	"phenix/util/mm/mmtest"
)

// c2FuzzHeaderLike reports whether line has the form of a `cc response` file
// header, `<digits>/<anything>/stdout:` or the stderr equivalent. A file line
// of that form cannot be told from a header.
func c2FuzzHeaderLike(line string) bool {
	rest, ok := strings.CutSuffix(line, "/stdout:")
	if !ok {
		rest, ok = strings.CutSuffix(line, "/stderr:")
	}

	id, client, found := strings.Cut(rest, "/")

	return ok && found && id != "" && client != "" && strings.Trim(id, "0123456789") == ""
}

// c2FuzzRead is a file as typedC2Response reads it back: line by line, with
// one carriage return dropped from the end of each line, as [bufio.ScanLines]
// does.
func c2FuzzRead(file string) string {
	lines := strings.Split(file, "\n")

	for i, line := range lines {
		lines[i] = strings.TrimSuffix(line, "\r")
	}

	return strings.Join(lines, "\n")
}

// FuzzTypedC2Response writes the stdout and stderr files of two clients as
// minimega lists them (it leaves out empty files), and checks that each
// client's file of each type reads back as written.
func FuzzTypedC2Response(f *testing.F) {
	for _, seed := range [][4]string{
		{"a", "b", "c", "d"},
		{"line 1\nline 2\n", "", "", "warn"},
		{"build/logs/stderr: none", "7/u2/stdout", "", ""},
		{"crlf\r\nline\r\n", "\r", "\n\n", "x"},
		{strings.Repeat("y", 70000), "", "z", ""},
	} {
		f.Add(seed[0], seed[1], seed[2], seed[3])
	}

	f.Fuzz(func(t *testing.T, out1, err1, out2, err2 string) {
		files := []struct{ path, content string }{
			{"7/u1/stderr", err1}, {"7/u1/stdout", out1}, {"7/u2/stderr", err2}, {"7/u2/stdout", out2},
		}

		var listing strings.Builder

		for _, file := range files {
			for line := range strings.SplitSeq(file.content, "\n") {
				if c2FuzzHeaderLike(strings.TrimSuffix(line, "\r")) {
					t.Skip("a file line that looks like a header")
				}
			}

			if file.content != "" {
				listing.WriteString(file.path + ":\n" + file.content + "\n")
			}
		}

		for _, file := range files {
			client, typ, _ := strings.Cut(strings.TrimPrefix(file.path, "7/"), "/")

			got := typedC2Response(listing.String(), client, C2ResponseType(typ))
			if want := c2FuzzRead(file.content); got != want {
				t.Fatalf("typedC2Response(%q, %s, %s) = %q, want %q", listing.String(), client, typ, got, want)
			}
		}
	})
}

//nolint:gochecknoglobals // minicli's handlers are global; register them once
var registerTapPatterns = sync.OnceFunc(func() {
	// as minimega's `tap` and `shell` commands declare them
	minicli.MustRegister(&minicli.Handler{Patterns: []string{
		"tap <create,> <vlan> bridge <bridge> name [tap name]",
		"tap <create,> <vlan> bridge <bridge> ip <ip> [tap name]",
		"tap <delete,> <tap name or all>",
		"shell <command>...",
	}})
})

// tapFuzzSafe matches values every minimega command takes as one argument.
var tapFuzzSafe = regexp.MustCompile(`^[A-Za-z0-9_./-]+$`)

// tapNetnsScript is the sh script TapVLAN runs to move tap name, with IP ip ("" for none), into its network namespace.
func tapNetnsScript(name, ip string) string {
	steps := []string{
		"ip netns add " + name + " || exit 100",
		"ip link set dev " + name + " netns " + name + " || exit 101",
		"ip netns exec " + name + " ip link set dev " + name + " up || exit 102",
	}

	if ip != "" {
		steps = append(steps, "ip netns exec "+name+" ip addr add "+ip+" dev "+name+" || exit 103")
	}

	return strings.Join(steps, "; ")
}

// FuzzTapVLAN checks that every command TapVLAN sends reaches minimega's
// handlers with exactly the values it was given, that it refuses a value
// otherwise without sending anything, and that it takes plain values.
func FuzzTapVLAN(f *testing.F) {
	registerTapPatterns()

	for _, seed := range []struct {
		name, vlan, bridge, ip string
		netns, del             bool
	}{
		{"t1", "EXP_1", "phenix", "", false, false},
		{"t1", "EXP_1", "br0", "10.0.0.1/24", true, false},
		{"t1", "other//EXP_1", "br0", "10.0.0.1/24", false, false},
		{"t1", "EXP_1", "br0", "", true, true},
		{"t 1", "EXP_1", "br0", "", false, false},
		{"t1", "EXP_1 #", "br0", "", false, false},
		{"t1", "EXP_1", "$BR", "10.0.0.1", false, false},
		{"t1", "EXP_1", "br0", "10.0.0.1 x", true, false},
		{"", "EXP_1", "br0", "", false, true},
	} {
		f.Add(seed.name, seed.vlan, seed.bridge, seed.ip, seed.netns, seed.del)
	}

	f.Fuzz(func(t *testing.T, name, vlan, bridge, ip string, netns, del bool) {
		received := useFakeMinimega(t, answering(nil))

		opts := []TapOption{TapNS("exp"), TapHost("head"), TapName(name), TapVLANAlias(vlan), TapBridge(bridge), TapIP(ip)}
		if netns {
			opts = append(opts, TapNetNS(name))
		}

		if del {
			opts = append(opts, TapDelete())
		}

		err := TapVLAN(opts...)

		sent := slices.DeleteFunc(received(), func(cmd mmtest.Command) bool { return cmd.Base == "host" })

		if err != nil {
			if !errors.Is(err, ErrUnsafeArg) {
				t.Fatalf("TapVLAN(%q, %q, %q, %q) error = %v, want only %v", name, vlan, bridge, ip, err, ErrUnsafeArg)
			}

			if len(sent) > 0 {
				t.Fatalf("TapVLAN(%q, %q, %q, %q) refused a value but sent %q", name, vlan, bridge, ip, sent)
			}

			plain := tapFuzzSafe.MatchString(name) && (ip == "" || tapFuzzSafe.MatchString(ip)) &&
				(del || tapFuzzSafe.MatchString(vlan) && tapFuzzSafe.MatchString(bridge))
			if plain {
				t.Fatalf("TapVLAN(%q, %q, %q, %q) error = %v, want plain values taken", name, vlan, bridge, ip, err)
			}

			return
		}

		if !strings.Contains(vlan, "//") {
			vlan = "exp//" + vlan
		}

		if bridge == "" {
			bridge = "phenix"
		}

		// a tap in a network namespace gets its IP there
		tapIP := ip
		if netns {
			tapIP = ""
		}

		if len(sent) == 0 || !strings.HasPrefix(sent[0].Base, "tap ") {
			t.Fatalf("TapVLAN(%q, %q, %q, %q) sent %q, want a tap command first", name, vlan, bridge, ip, sent)
		}

		shell := [][]string{
			{"sh", "-c", tapNetnsScript(name, ip)},
			{"ip", "netns", "delete", name},
		}

		for _, cmd := range sent {
			compiled, err := minicli.Compile(cmd.Base)
			if err != nil || compiled == nil {
				t.Fatalf("TapVLAN(%q, %q, %q, %q) sent %q, which minimega cannot read (error %v)", name, vlan, bridge, ip, cmd.Raw, err)
			}

			args := compiled.StringArgs

			switch {
			case compiled.BoolArgs["create"]:
				if args["vlan"] != vlan || args["bridge"] != bridge || args["tap"] != name || args["ip"] != tapIP {
					t.Fatalf("TapVLAN(%q, %q, %q, %q) sent %q, which minimega reads as %q", name, vlan, bridge, ip, cmd.Raw, args)
				}
			case compiled.BoolArgs["delete"]:
				if args["tap"] != name {
					t.Fatalf("TapVLAN(%q) sent %q, which minimega reads as %q", name, cmd.Raw, args)
				}
			default:
				if !slices.ContainsFunc(shell, func(want []string) bool { return slices.Equal(want, compiled.ListArgs["command"]) }) {
					t.Fatalf("TapVLAN(%q, %q) sent %q, which minimega reads as %q", name, ip, cmd.Raw, compiled.ListArgs["command"])
				}
			}
		}
	})
}
