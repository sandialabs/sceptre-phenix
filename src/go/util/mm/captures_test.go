package mm

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"
	"pgregory.net/rapid"

	"phenix/util/mm/mmtest"
)

const (
	supportedAnswer   = "minimega 3.4.0 abc 2026-10-01"
	unsupportedAnswer = "minimega 3.3.0 abc 2026-01-01"
)

// namespaceCaptureHeader is the header of the capture pcap namespace listing of minimega 3.4.0.
//
//nolint:gochecknoglobals // read-only test table
var namespaceCaptureHeader = []string{
	"id", "host", "bridge", "state", "ports", "packets", "bytes", "kdrops", "pdrops", "qdrops", "lost", "late", "lag",
	"skew", "size", "path", "vlans", "filter", "start", "error",
}

// namespaceRow is a row of the listing with namespaceCaptureHeader, from the column values given by name.
func namespaceRow(values map[string]string) []string {
	row := make([]string, len(namespaceCaptureHeader))
	for i, column := range namespaceCaptureHeader {
		row[i] = values[column]
	}

	return row
}

func TestParseNamespaceCaptures(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		rows []map[string]string
		want []NamespaceCapture
	}{
		"summary and streams, sorted by numeric ID, then host and bridge": {
			rows: []map[string]string{
				{"id": "10", "host": "", "bridge": "", "state": "stopped", "path": "/f/exp/files/b.pcap"},
				{"id": "3", "host": "compute2", "bridge": "mega_bridge_exp", "state": "running", "packets": "5", "lag": "95ms"},
				{
					"id": "3", "host": "", "bridge": "", "state": "running", "ports": "4", "packets": "12", "bytes": "1200",
					"kdrops": "1", "pdrops": "2", "qdrops": "3", "lost": "4", "late": "5", "lag": "300µs", "skew": "-600µs",
					"size": "4096", "path": "/f/exp/files/a.pcapng", "vlans": "EXP_1, EXP_2,", "filter": "tcp port 80",
					"start": "2026-11-05T10:00:00Z", "error": "",
				},
				{"id": "3", "host": "compute1", "bridge": "phenix", "state": "running", "packets": "3"},
				{"id": "3", "host": "compute1", "bridge": "mega_bridge_exp", "state": "stalled", "packets": "4"},
				{"id": "2", "host": "", "bridge": "", "state": "failed", "error": "node compute1 runs 3.3.0"},
			},
			want: []NamespaceCapture{
				{ID: "2", State: "failed", Error: "node compute1 runs 3.3.0"},
				{
					ID: "3", State: "running", Path: "/f/exp/files/a.pcapng", Format: "pcapng", Filter: "tcp port 80",
					Start: "2026-11-05T10:00:00Z", VLANs: []string{"EXP_1", "EXP_2"}, Size: 4096,
					CaptureCounters: CaptureCounters{
						Ports: 4, Packets: 12, Bytes: 1200, KDrops: 1, PDrops: 2, QDrops: 3, Lost: 4, Late: 5,
						Lag: 300 * time.Microsecond, Skew: -600 * time.Microsecond,
					},
					Streams: []CaptureStream{
						{Host: "compute1", Bridge: "mega_bridge_exp", State: "stalled", CaptureCounters: CaptureCounters{Packets: 4}},
						{Host: "compute1", Bridge: "phenix", State: "running", CaptureCounters: CaptureCounters{Packets: 3}},
						{
							Host: "compute2", Bridge: "mega_bridge_exp", State: "running",
							CaptureCounters: CaptureCounters{Packets: 5, Lag: 95 * time.Millisecond},
						},
					},
				},
				{ID: "10", State: "stopped", Path: "/f/exp/files/b.pcap", Format: "pcap"},
			},
		},
		"missing and unknown columns": {
			rows: []map[string]string{{"id": "1", "colour": "blue"}},
			want: []NamespaceCapture{{ID: "1"}},
		},
		"numbers and durations that do not parse are 0": {
			rows: []map[string]string{{
				"id": "1", "packets": "-1", "bytes": "1e3", "ports": "x", "size": "big", "lag": "95", "skew": "soon",
				"lost": "18446744073709551616",
			}},
			want: []NamespaceCapture{{ID: "1"}},
		},
		"rows without an ID are dropped, other IDs sort after numbers": {
			rows: []map[string]string{
				{"id": "", "host": "compute1", "bridge": "phenix"},
				{"id": "b", "state": "running"},
				{"id": "a", "state": "running"},
				{"id": "7", "state": "running"},
			},
			want: []NamespaceCapture{{ID: "7", State: "running"}, {ID: "a", State: "running"}, {ID: "b", State: "running"}},
		},
		"the first summary row wins": {
			rows: []map[string]string{
				{"id": "1", "state": "running", "path": "a.pcap"},
				{"id": "1", "state": "stopped", "path": "b.pcap"},
			},
			want: []NamespaceCapture{{ID: "1", State: "running", Path: "a.pcap", Format: "pcap"}},
		},
		"a stream with no summary row": {
			rows: []map[string]string{{"id": "4", "host": "compute1", "bridge": "phenix", "state": "orphaned"}},
			want: []NamespaceCapture{{ID: "4", Streams: []CaptureStream{{Host: "compute1", Bridge: "phenix", State: "orphaned"}}}},
		},
		"no rows": {},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tc.want, parseNamespaceCaptures(tc.rows)); diff != "" {
				t.Errorf("parseNamespaceCaptures() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Whatever rows minimega gives, the parser does not panic, lists each ID once in order, and puts every stream row
// in the capture of its ID, sorted by host and bridge.
func TestParseNamespaceCapturesProperty(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		pick := func(label string, values ...string) string { return rapid.SampledFrom(values).Draw(t, label) }

		rows := rapid.SliceOfN(rapid.Custom(func(t *rapid.T) map[string]string {
			row := map[string]string{}
			for _, column := range namespaceCaptureHeader {
				if rapid.Bool().Draw(t, "has "+column) {
					row[column] = rapid.StringMatching(`[0-9a-zµ., -]{0,6}`).Draw(t, column)
				}
			}

			row["id"] = pick("id", "", "1", "2", "10", "x")
			row["host"] = pick("host", "", "compute1", "compute2")
			row["bridge"] = pick("bridge", "", "phenix", "mega_bridge_exp")

			return row
		}), 0, 20).Draw(t, "rows")

		captures := parseNamespaceCaptures(rows)

		ids := make([]string, 0, len(captures))
		for _, c := range captures {
			ids = append(ids, c.ID)

			if !slices.IsSortedFunc(c.Streams, func(a, b CaptureStream) int {
				return strings.Compare(a.Host+"\x00"+a.Bridge, b.Host+"\x00"+b.Bridge)
			}) {
				t.Fatalf("streams of capture %s are not sorted: %+v", c.ID, c.Streams)
			}
		}

		if !slices.IsSortedFunc(ids, CompareCaptureIDs) || len(slices.Compact(slices.Clone(ids))) != len(ids) {
			t.Fatalf("capture IDs = %q, want each once, sorted", ids)
		}

		for _, row := range rows {
			if row["id"] == "" || (row["host"] == "" && row["bridge"] == "") {
				continue
			}

			i := slices.Index(ids, row["id"])
			if i < 0 {
				t.Fatalf("stream row %v belongs to no listed capture %q", row, ids)
			}

			if !slices.ContainsFunc(captures[i].Streams, func(s CaptureStream) bool {
				return s.Host == row["host"] && s.Bridge == row["bridge"]
			}) {
				t.Fatalf("capture %s has no stream for row %v", row["id"], row)
			}
		}
	})
}

// GetNamespaceCaptures reads the listing in the namespace, keeps the empty host of a summary row though mmcli
// names the answering host, and reports a failed read as an error.
func TestGetNamespaceCaptures(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	listing := []*minicli.Response{mmtest.Tabular("head", namespaceCaptureHeader,
		namespaceRow(map[string]string{"id": "1", "state": "running", "path": "exp/files/a.pcapng"}),
		namespaceRow(map[string]string{"id": "1", "host": "compute1", "bridge": "phenix", "state": "running"}),
	)}

	tests := map[string]struct {
		answer  string
		resps   []*minicli.Response
		want    []NamespaceCapture
		wantErr error
		sent    []string
	}{
		"lists the captures": {
			answer: supportedAnswer,
			resps:  listing,
			want: []NamespaceCapture{{
				ID: "1", State: "running", Path: "exp/files/a.pcapng", Format: "pcapng",
				Streams: []CaptureStream{{Host: "compute1", Bridge: "phenix", State: "running"}},
			}},
			sent: []string{inExp("capture pcap namespace")},
		},
		"a listing without a host column has only streams": {
			answer: supportedAnswer,
			resps: []*minicli.Response{
				mmtest.Tabular("head", []string{"id", "bridge", "state"}, []string{"1", "phenix", "running"}),
			},
			want: []NamespaceCapture{{ID: "1", Streams: []CaptureStream{{Host: "head", Bridge: "phenix", State: "running"}}}},
			sent: []string{inExp("capture pcap namespace")},
		},
		"a failed read is an error": {
			answer:  supportedAnswer,
			resps:   failed("boom"),
			wantErr: errAny,
			sent:    []string{inExp("capture pcap namespace")},
		},
		"older minimega": {answer: unsupportedAnswer, wantErr: ErrCaptureUnsupported},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			received := useFakeMinimega(t, answering(map[string][]*minicli.Response{"capture pcap namespace": tc.resps}))
			useMinimegaVersion(t, answered(tc.answer), false, true)

			got, err := GetNamespaceCaptures(NS("exp"), Context(t.Context()))
			checkErr(t, "GetNamespaceCaptures()", err, tc.wantErr)

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("GetNamespaceCaptures() mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(tc.sent, sentRaw(received())); diff != "" {
				t.Errorf("GetNamespaceCaptures() sent commands mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// errAny stands for any error in a test table.
var errAny = errors.New("any error")

// checkErr fails the test unless err is want: nil, errAny for any error, or an error that err wraps.
func checkErr(t *testing.T, call string, err, want error) {
	t.Helper()

	switch {
	case want == nil && err != nil:
		t.Errorf("%s error = %v, want nil", call, err)
	case want == errAny && err == nil: //nolint:errorlint // a marker, never wrapped
		t.Errorf("%s error = nil, want an error", call)
	case want != nil && want != errAny && !errors.Is(err, want): //nolint:errorlint // a marker, never wrapped
		t.Errorf("%s error = %v, want %v", call, err, want)
	}
}

func TestStartNamespaceCapture(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	const file = "exp/files/exp-20261105-100000.pcapng"

	tests := map[string]struct {
		answer  string
		opts    []Option
		resps   []*minicli.Response
		want    string
		wantErr error
		sent    []string
	}{
		"only a file": {
			answer: supportedAnswer,
			resps:  []*minicli.Response{mmtest.Text("head", "3\n")},
			want:   "3",
			sent:   []string{inExp("capture pcap namespace " + file)},
		},
		"every option": {
			answer: supportedAnswer,
			opts: []Option{
				CaptureVLANs([]string{"EXP_1", "EXP_2"}), CaptureFilter("tcp port 80 and not (host 10.0.0.1)"),
				CaptureSnaplen(1600), CaptureFormat("pcapng"), CaptureMaxSize(10240),
			},
			resps: []*minicli.Response{mmtest.Text("head", "4")},
			want:  "4",
			sent: []string{inExp("capture pcap namespace " + file +
				` vlans=EXP_1,EXP_2 filter="tcp port 80 and not (host 10.0.0.1)" snaplen=1600 format=pcapng maxsize=10240M`)},
		},
		"the smallest snaplen and pcap": {
			answer: supportedAnswer,
			opts:   []Option{CaptureSnaplen(14), CaptureFormat("pcap"), CaptureMaxSize(0)},
			resps:  []*minicli.Response{mmtest.Text("head", "5")},
			want:   "5",
			sent:   []string{inExp("capture pcap namespace " + file + " snaplen=14 format=pcap")},
		},
		"a failed start": {
			answer: supportedAnswer, resps: failed("node compute1 runs 3.3.0"), wantErr: errAny,
			sent: []string{inExp("capture pcap namespace " + file)},
		},
		"no ID": {
			answer: supportedAnswer, resps: []*minicli.Response{mmtest.Text("head", " \n")}, wantErr: errAny,
			sent: []string{inExp("capture pcap namespace " + file)},
		},
		"older minimega":        {answer: unsupportedAnswer, wantErr: ErrCaptureUnsupported},
		"absolute file":         {answer: supportedAnswer, opts: []Option{CaptureFile("/" + file)}, wantErr: ErrInvalidCaptureOption},
		"file with a space":     {answer: supportedAnswer, opts: []Option{CaptureFile("exp/files/a b.pcap")}, wantErr: ErrUnsafeArg},
		"no file":               {answer: supportedAnswer, opts: []Option{CaptureFile("")}, wantErr: ErrInvalidCaptureOption},
		"VLAN with a comma":     {answer: supportedAnswer, opts: []Option{CaptureVLANs([]string{"A,B"})}, wantErr: ErrUnsafeArg},
		"VLAN with a quote":     {answer: supportedAnswer, opts: []Option{CaptureVLANs([]string{`A"`})}, wantErr: ErrUnsafeArg},
		"filter with a quote":   {answer: supportedAnswer, opts: []Option{CaptureFilter(`host "a"`)}, wantErr: ErrUnsafeArg},
		"filter with a hash":    {answer: supportedAnswer, opts: []Option{CaptureFilter("port 80 # x")}, wantErr: ErrUnsafeArg},
		"filter too long":       {answer: supportedAnswer, opts: []Option{CaptureFilter(strings.Repeat("a", 1025))}, wantErr: ErrUnsafeArg},
		"snaplen too small":     {answer: supportedAnswer, opts: []Option{CaptureSnaplen(13)}, wantErr: ErrInvalidCaptureOption},
		"snaplen too large":     {answer: supportedAnswer, opts: []Option{CaptureSnaplen(65536)}, wantErr: ErrInvalidCaptureOption},
		"format minimega lacks": {answer: supportedAnswer, opts: []Option{CaptureFormat("erf")}, wantErr: ErrInvalidCaptureOption},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			received := useFakeMinimega(t, answeringAll(tc.resps))
			useMinimegaVersion(t, answered(tc.answer), false, true)

			opts := append([]Option{NS("exp"), CaptureFile(file), Context(t.Context())}, tc.opts...)

			got, err := StartNamespaceCapture(opts...)
			checkErr(t, "StartNamespaceCapture()", err, tc.wantErr)

			if got != tc.want {
				t.Errorf("StartNamespaceCapture() = %q, want %q", got, tc.want)
			}

			if diff := cmp.Diff(tc.sent, sentRaw(received())); diff != "" {
				t.Errorf("StartNamespaceCapture() sent commands mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestStopNamespaceCapture(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	final := []*minicli.Response{mmtest.Tabular("head", namespaceCaptureHeader,
		namespaceRow(map[string]string{"id": "3", "state": "stopped", "packets": "9", "path": "exp/files/a.pcap"}),
	)}

	tests := map[string]struct {
		id      string
		resps   []*minicli.Response
		want    []NamespaceCapture
		wantErr error
		sent    []string
	}{
		"stops the capture": {
			id: "3", resps: final,
			want: []NamespaceCapture{{
				ID: "3", State: "stopped", Path: "exp/files/a.pcap", Format: "pcap", CaptureCounters: CaptureCounters{Packets: 9},
			}},
			sent: []string{inExp("capture pcap delete namespace 3")},
		},
		"a failed stop": {
			id: "3", resps: failed("no capture 3"), wantErr: errAny, sent: []string{inExp("capture pcap delete namespace 3")},
		},
		"no ID":     {wantErr: ErrUnsafeArg},
		"unsafe ID": {id: "3 x", wantErr: ErrUnsafeArg},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			received := useFakeMinimega(t, answeringAll(tc.resps))
			useMinimegaVersion(t, answered(supportedAnswer), false, true)

			got, err := StopNamespaceCapture(NS("exp"), CaptureID(tc.id), Context(t.Context()))
			checkErr(t, "StopNamespaceCapture()", err, tc.wantErr)

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("StopNamespaceCapture() mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(tc.sent, sentRaw(received())); diff != "" {
				t.Errorf("StopNamespaceCapture() sent commands mismatch (-want +got):\n%s", diff)
			}
		})
	}

	t.Run("older minimega", func(t *testing.T) {
		received := useFakeMinimega(t, nil)
		useMinimegaVersion(t, answered(unsupportedAnswer), false, true)

		_, err := StopNamespaceCapture(NS("exp"), CaptureID("3"))
		checkErr(t, "StopNamespaceCapture()", err, ErrCaptureUnsupported)

		if got := sentRaw(received()); len(got) != 0 {
			t.Errorf("StopNamespaceCapture() sent %q, want nothing", got)
		}
	})
}

// answeringAll answers the headnode lookup as answering does, and every other command with resps.
func answeringAll(resps []*minicli.Response) mmtest.Handler {
	return func(cmd mmtest.Command) []*minicli.Response {
		if cmd.Base == "host" && cmd.Namespace == "minimega" {
			return answering(nil)(cmd)
		}

		return resps
	}
}

// GetBridgeMirrors lists the head node's mirrors locally and the other hosts' with one mesh send, naming the host
// that answered each.
func TestGetBridgeMirrors(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	header := []string{"bridge", "mirror", "output", "namespace", "kind"}
	local := mmtest.Tabular("head", header, []string{"mega_bridge_exp", "mirror-1", "mega_tap1", "exp", "capture"})
	remote := []*minicli.Response{
		mmtest.Tabular("compute1", header, []string{"mega_bridge_exp", "mirror-2", "mega_tap2", "exp", "capture"}),
		mmtest.Tabular("compute2", header, []string{"phenix", "m", "eth9", "", "external"}),
	}

	headMirror := BridgeMirror{
		Host: "head", Bridge: "mega_bridge_exp", Mirror: "mirror-1", Output: "mega_tap1", Namespace: "exp", Kind: "capture",
	}
	remoteMirrors := []BridgeMirror{
		{Host: "compute1", Bridge: "mega_bridge_exp", Mirror: "mirror-2", Output: "mega_tap2", Namespace: "exp", Kind: "capture"},
		{Host: "compute2", Bridge: "phenix", Mirror: "m", Output: "eth9", Namespace: "", Kind: "external"},
	}

	tests := map[string]struct {
		hosts   []string
		failing string
		want    []BridgeMirror
		wantErr error
		sent    []string
	}{
		"head node and others": {
			hosts: []string{"compute1", "head", "compute2", "compute1"},
			want:  append([]BridgeMirror{headMirror}, remoteMirrors...),
			sent:  []string{bare("bridge mirrors"), bare("mesh send compute1,compute2 bridge mirrors")},
		},
		"head node only": {
			hosts: []string{"head"}, want: []BridgeMirror{headMirror}, sent: []string{bare("bridge mirrors")},
		},
		"other hosts only": {
			hosts: []string{"compute1", "compute2"}, want: remoteMirrors,
			sent: []string{bare("mesh send compute1,compute2 bridge mirrors")},
		},
		"no hosts": {},
		"a failed host": {
			hosts: []string{"head", "compute1"}, failing: "mesh send compute1 bridge mirrors", wantErr: errAny,
			sent: []string{bare("bridge mirrors"), bare("mesh send compute1 bridge mirrors")},
		},
		"a failed head node": {
			hosts: []string{"head", "compute1"}, failing: "bridge mirrors", wantErr: errAny,
			sent: []string{bare("bridge mirrors")},
		},
		"a host list in one name":  {hosts: []string{"compute1,compute2"}, wantErr: ErrInvalidCaptureOption},
		"a host name with a space": {hosts: []string{"compute1 shell"}, wantErr: ErrInvalidCaptureOption},
		"an empty host name":       {hosts: []string{""}, wantErr: ErrInvalidCaptureOption},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
				switch cmd.Base {
				case tc.failing:
					return failed("boom")
				case "bridge mirrors":
					return []*minicli.Response{local}
				case "mesh send compute1,compute2 bridge mirrors":
					return remote
				}

				return answering(nil)(cmd)
			})
			useMinimegaVersion(t, answered(supportedAnswer), false, true)

			got, err := GetBridgeMirrors(MirrorHosts(tc.hosts), Context(t.Context()))
			checkErr(t, "GetBridgeMirrors()", err, tc.wantErr)

			if tc.wantErr == nil {
				if diff := cmp.Diff(tc.want, got); diff != "" {
					t.Errorf("GetBridgeMirrors(%q) mismatch (-want +got):\n%s", tc.hosts, diff)
				}
			}

			if diff := cmp.Diff(tc.sent, sentRaw(received())); diff != "" {
				t.Errorf("GetBridgeMirrors(%q) sent commands mismatch (-want +got):\n%s", tc.hosts, diff)
			}
		})
	}

	t.Run("older minimega", func(t *testing.T) {
		received := useFakeMinimega(t, nil)
		useMinimegaVersion(t, answered(unsupportedAnswer), false, true)

		_, err := GetBridgeMirrors(MirrorHosts([]string{"head"}))
		checkErr(t, "GetBridgeMirrors()", err, ErrCaptureUnsupported)

		if got := sentRaw(received()); len(got) != 0 {
			t.Errorf("GetBridgeMirrors() sent %q, want nothing", got)
		}
	})
}

// With minimega 3.4.0, GetExperimentCaptures reads the refreshed listing by column name, without .columns.
func TestGetExperimentCapturesRefreshed(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	header := []string{
		"host", "bridge", "type", "interface", "mode", "compress", "id", "state", "format", "snaplen", "filter",
		"packets", "bytes", "kdrops", "size", "start", "error", "path",
	}
	listing := []*minicli.Response{mmtest.Tabular("head", header,
		[]string{"head", "phenix", "pcap", "a:1", "", "", "7", "running", "pcapng", "1600", "", "12", "900", "2", "4096",
			"2026-11-05T10:00:00Z", "", "exp/files/a.pcapng"},
		[]string{"head", "phenix", "pcap", "b:0", "", "", "8", "full", "", "", "", "x", "", "", "", "", "file full",
			"exp/files/b.pcap"},
		[]string{"head", "phenix", "pcap", "", "", "", "9", "running", "pcap", "", "", "", "", "", "", "", "", "bridge.pcap"},
	)}

	tests := map[string]struct {
		answer string
		resps  []*minicli.Response
		want   []Capture
		sent   []string
	}{
		"minimega 3.4.0": {
			answer: supportedAnswer,
			resps:  listing,
			want: []Capture{
				{
					VM: "a", Interface: 1, Filepath: "exp/files/a.pcapng", ID: "7", State: "running", Format: "pcapng",
					Packets: 12, KDrops: 2, Size: 4096,
				},
				{VM: "b", Interface: 0, Filepath: "exp/files/b.pcap", ID: "8", State: "full", Format: "pcap", Error: "file full"},
			},
			sent: []string{inExp("capture")},
		},
		"older minimega": {
			answer: unsupportedAnswer,
			resps: []*minicli.Response{
				mmtest.Tabular("head", []string{"interface", "path"}, []string{"a:1", "exp/files/a.pcap"}),
			},
			want: []Capture{{VM: "a", Interface: 1, Filepath: "exp/files/a.pcap"}},
			sent: []string{inExp(`.columns "interface","path" capture`)},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			received := useFakeMinimega(t, answering(map[string][]*minicli.Response{captureCmd: tc.resps}))
			useMinimegaVersion(t, answered(tc.answer), false, true)

			if diff := cmp.Diff(tc.want, GetExperimentCaptures(NS("exp"))); diff != "" {
				t.Errorf("GetExperimentCaptures() mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(tc.sent, sentRaw(received())); diff != "" {
				t.Errorf("GetExperimentCaptures() sent commands mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// A failed read of the VM capture listing is an error from GetExperimentCapturesChecked, and the captures of the
// responses that answered are kept; GetExperimentCaptures logs it and returns those captures.
func TestGetExperimentCapturesChecked(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	listed := mmtest.Tabular("compute1", []string{"interface", "path"}, []string{"a:1", "exp/files/a.pcap"})
	want := []Capture{{VM: "a", Interface: 1, Filepath: "exp/files/a.pcap"}}

	tests := map[string]struct {
		resps   []*minicli.Response
		want    []Capture
		wantErr error
	}{
		"every node answers":      {resps: []*minicli.Response{listed}, want: want},
		"a node fails":            {resps: append(failed("boom"), listed), want: want, wantErr: errAny},
		"the only response fails": {resps: failed("boom"), wantErr: errAny},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			useFakeMinimega(t, answering(map[string][]*minicli.Response{captureCmd: tc.resps}))
			useMinimegaVersion(t, answered(unsupportedAnswer), false, true)

			got, err := GetExperimentCapturesChecked(NS("exp"), Context(t.Context()))
			checkErr(t, "GetExperimentCapturesChecked()", err, tc.wantErr)

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("GetExperimentCapturesChecked() mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(tc.want, GetExperimentCaptures(NS("exp"))); diff != "" {
				t.Errorf("GetExperimentCaptures() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// A capture runs while minimega starts, records, waits for a stalled stream or stops it.
func TestNamespaceCaptureRunning(t *testing.T) {
	t.Parallel()

	for state, want := range map[string]bool{
		"starting": true, "running": true, "stalled": true, "stopping": true,
		"stopped": false, "failed": false, "orphaned": false, "full": false, "": false,
	} {
		if got := (NamespaceCapture{State: state}).Running(); got != want {
			t.Errorf("NamespaceCapture{State: %q}.Running() = %t, want %t", state, got, want)
		}
	}
}

// CompareCaptureIDs orders numeric IDs by number, equal numbers as equal, and puts them before other IDs, which sort
// by text.
func TestCompareCaptureIDs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		a, b string
		want int
	}{
		{a: "4", b: "10", want: -1},
		{a: "10", b: "4", want: 1},
		{a: "4", b: "4", want: 0},
		{a: "007", b: "7", want: 0},
		{a: "9", b: "a", want: -1},
		{a: "a", b: "9", want: 1},
		{a: "a", b: "b", want: -1},
		{a: "b", b: "b", want: 0},
	}

	for _, tc := range tests {
		if got := CompareCaptureIDs(tc.a, tc.b); got != tc.want {
			t.Errorf("CompareCaptureIDs(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}
