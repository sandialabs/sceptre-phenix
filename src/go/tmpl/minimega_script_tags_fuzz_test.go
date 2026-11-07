package tmpl_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	v1 "phenix/types/version/v1"
	"phenix/util/mm"
	"phenix/util/mm/mmtest"
)

// noPreprocess is the prefix of a script line whose values minimega must not change.
const noPreprocess = ".preprocess false "

// checkUnexpanded fails t for a script line that minimega would change before it runs it.
func checkUnexpanded(t *testing.T, lines []string) {
	t.Helper()

	for _, line := range lines {
		if mmtest.Expands(line) {
			t.Errorf("minimega would expand the $ in script line %q", line)
		}
	}
}

// compileLine compiles the line of lines that starts with prefix, without its noPreprocess prefix, which every line
// with free text must have.
func compileLine(t *testing.T, lines []string, prefix string) *minicli.Command {
	t.Helper()

	i := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(l, noPreprocess+prefix) })
	if i < 0 {
		t.Fatalf("no %q line:\n%s", noPreprocess+prefix, strings.Join(lines, "\n"))
	}

	if i+1 >= len(lines) {
		t.Fatalf("%q line not followed by another line:\n%s", prefix, strings.Join(lines, "\n"))
	}

	cmd, err := minicli.Compile(strings.TrimPrefix(lines[i], noPreprocess))
	if err != nil {
		t.Fatalf("minicli.Compile(%q) error = %v", lines[i], err)
	}

	return cmd
}

// FuzzMinimegaScriptTagRoundTrip checks that minicli reads every label and
// QEMU override key and value back from the minimega script exactly, on one
// line that turns off minimega's preprocessing, so a "$" stays as it is.
func FuzzMinimegaScriptTagRoundTrip(f *testing.F) {
	minicli.Reset()
	f.Cleanup(minicli.Reset)
	minicli.MustRegister(&minicli.Handler{
		Patterns: []string{"vm config tags <key> <value>", "vm config qemu-override <match> <replacement>"},
	})

	// Fuzzing seeds testing a variety of potential breakage areas
	f.Add(`a "quoted" \ key`, `C:\temp\`)
	f.Add("label", "First line\r\nSecond line\t#")
	f.Add("key\nshell id", "x")
	f.Add("empty", "")
	f.Add("région", "naïve café ✓")
	f.Add("emoji 🚀", "🧪 lab 👩‍💻")
	f.Add("  padded  ", "   leading and trailing   ")
	f.Add("it's", `don't 'quote' me`)
	f.Add("json", `{"role":"web","ports":[80,443]}`)
	f.Add("shell", "$(id); `whoami` | tee /tmp/x && echo $HOME")
	f.Add("${KEY}", "$1 $@ ${VALUE}")
	f.Add("file:x", "http://example.com/a")
	f.Add("<key>", "[optional] {a,b} <value>")
	f.Add("control", "nul\x00 esc\x1b[31m cr\r bell\a")
	f.Add("unicode breaks", "line para next\u0085end")

	f.Fuzz(func(t *testing.T, key, value string) {
		if !utf8.ValidString(key) || !utf8.ValidString(value) {
			t.Skip("labels and overrides come from YAML or JSON, which are UTF-8")
		}

		lines := renderScript(t, scriptSpec(func(n *v1.Node) {
			n.LabelsF = map[string]string{key: value}
			n.OverridesF = map[string]string{key: value}
		}))

		checkUnexpanded(t, lines)

		tags := compileLine(t, lines, "vm config tags ")
		if got, want := [2]string{tags.StringArgs["key"], tags.StringArgs["value"]}, [2]string{key, value}; got != want {
			t.Errorf("label round trip = %q, want %q", got, want)
		}

		override := compileLine(t, lines, "vm config qemu-override ")
		got := [2]string{override.StringArgs["match"], override.StringArgs["replacement"]}
		if want := [2]string{key, value}; got != want {
			t.Errorf("QEMU override round trip = %q, want %q", got, want)
		}
	})
}

// FuzzMinimegaScriptValidatedValues checks that an experiment refuses an
// interface, CPU, default bridge, VLAN alias or schedule value that minimega
// would change, and that no line of the script of an experiment it takes is
// one minimega would change.
func FuzzMinimegaScriptValidatedValues(f *testing.F) {
	f.Add("br0", "EXP_1", "e1000", "Broadwell", "10.0.0.2", "phenix", "compute1")
	f.Add("$BRIDGE", "EXP_1", "e1000", "Broadwell", "10.0.0.2", "phenix", "compute1")
	f.Add("br0", "${VLAN}", "e1000", "Broadwell", "10.0.0.2", "phenix", "compute1")
	f.Add("br0", "EXP_1", "$1", "Broadwell", "10.0.0.2", "phenix", "compute1")
	f.Add("br0", "EXP_1", "e1000", "host,$X", "10.0.0.2", "phenix", "compute1")
	f.Add("br0", "EXP_1", "e1000", "Broadwell", "$IP", "phenix", "compute1")
	f.Add("br0", "EXP_1", "e1000", "Broadwell", "10.0.0.2", "file:br", "compute1")
	f.Add("br0", "EXP_1", "e1000", "Broadwell", "10.0.0.2", "phenix", "$HOST")
	f.Add("br0", "a$(b)", "e1000", "Broadwell", "10.0.0.2", "phenix", "compute$")

	f.Fuzz(func(t *testing.T, bridge, vlan, driver, cpu, address, defaultBridge, host string) {
		spec := scriptSpec(func(n *v1.Node) {
			n.HardwareF.CPUF = cpu
			n.NetworkF = &v1.Network{InterfacesF: []*v1.Interface{
				{NameF: "eth0", BridgeF: bridge, VLANF: vlan, DriverF: driver, AddressF: address},
			}}
		})
		spec.DefaultBridgeF = defaultBridge
		spec.SchedulesF = map[string]string{"host-01": host}

		if err := spec.Init(); err != nil {
			for _, value := range []string{bridge, vlan, driver, cpu, address, defaultBridge, host} {
				if mm.ValidateUnexpanded(value) != nil {
					return // refused a value minimega would change
				}
			}

			if errors.Is(err, mm.ErrUnsafeArg) {
				t.Fatalf("Init() error = %v, want none of these values refused", err)
			}

			t.Skip("refused for another reason")
		}

		checkUnexpanded(t, renderScript(t, spec))
	})
}
