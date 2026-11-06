package printer

import (
	"bytes"
	"io"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"phenix/api/capture"
	"phenix/api/job"
	"phenix/store"
	"phenix/types"
	v1 "phenix/types/version/v1"
	v2 "phenix/types/version/v2"
	"phenix/util/mm"
	"phenix/util/termcolor"
)

// Each table printer writes its rows in a fixed order, sorted where its input
// is a map, with the columns and values it names.
func TestTablePrinters(t *testing.T) {
	t.Parallel()

	plain := termcolor.New(false)
	finished := time.Date(2026, 10, 30, 13, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		render func(w io.Writer)
		want   string
	}{
		{
			name: "configs",
			render: func(w io.Writer) {
				PrintTableOfConfigs(w, store.Configs{
					{
						Kind:     "Topology",
						Version:  "phenix.sandia.gov/v1",
						Metadata: store.ConfigMetadata{Name: "topo", Created: "2026-01-01"},
					},
					{Kind: "Scenario", Version: "phenix.sandia.gov/v2", Metadata: store.ConfigMetadata{Name: "scn"}},
				})
			},
			want: `+----------+----------------------+------+------------+
|   KIND   |       VERSION        | NAME |  CREATED   |
+----------+----------------------+------+------------+
| Topology | phenix.sandia.gov/v1 | topo | 2026-01-01 |
| Scenario | phenix.sandia.gov/v2 | scn  |            |
+----------+----------------------+------+------------+
`,
		},
		{
			name: "image configs",
			render: func(w io.Writer) {
				PrintTableOfImageConfigs(w, []string{"Format", "Compressed", "Mirror"}, types.Image{
					Metadata: store.ConfigMetadata{Name: "jammy"},
					Spec: &v1.Image{
						Size: "10G", Variant: "minbase", Release: "jammy", Format: v1.FormatQcow2, Compress: true,
						Mirror: "http://mirror", Overlays: []string{"o1", "o2"}, Packages: []string{"vim"},
						Scripts: map[string]string{"zz": "", "aa": "", "mm": ""},
					},
				})
			},
			want: `+-------+------+---------+---------+----------+----------+---------+--------+------------+---------------+
| NAME  | SIZE | VARIANT | RELEASE | OVERLAYS | PACKAGES | SCRIPTS | FORMAT | COMPRESSED |    MIRROR     |
+-------+------+---------+---------+----------+----------+---------+--------+------------+---------------+
| jammy | 10G  | minbase | jammy   | o1       | vim      | aa      | qcow2  | true       | http://mirror |
|       |      |         |         | o2       |          | mm      |        |            |               |
|       |      |         |         |          |          | zz      |        |            |               |
+-------+------+---------+---------+----------+----------+---------+--------+------------+---------------+
`,
		},
		{
			name: "VLAN aliases",
			render: func(w io.Writer) {
				PrintTableOfVLANAliases(w, map[string]map[string]int{
					"exp2": {"MGMT": 200}, "exp1": {"WAN": 102, "LAN": 101},
				})
			},
			want: `+------------+------------+---------+
| EXPERIMENT | VLAN ALIAS | VLAN ID |
+------------+------------+---------+
| exp1       | LAN        |     101 |
| exp1       | WAN        |     102 |
| exp2       | MGMT       |     200 |
+------------+------------+---------+
`,
		},
		{
			name: "VLAN ranges",
			render: func(w io.Writer) {
				PrintTableOfVLANRanges(w, map[string][2]int{"exp2": {300, 399}, "exp1": {100, 199}})
			},
			want: `+------------+------------+
| EXPERIMENT | VLAN RANGE |
+------------+------------+
| exp1       | 100 - 199  |
| exp2       | 300 - 399  |
+------------+------------+
`,
		},
		{
			name: "subnet captures",
			render: func(w io.Writer) {
				PrintTableOfSubnetCaptures(w, []mm.Capture{{VM: "router", Interface: 1, Filepath: "/exp/files/router.pcap"}})
			},
			want: `+--------+-----------------+------------------------+
|  NAME  | INTERFACE INDEX |       FILE PATH        |
+--------+-----------------+------------------------+
| router |               1 | /exp/files/router.pcap |
+--------+-----------------+------------------------+
`,
		},
		{
			name: "experiment captures",
			render: func(w io.Writer) {
				PrintTableOfExperimentCaptures(w, []mm.NamespaceCapture{
					{
						CaptureCounters: mm.CaptureCounters{Packets: 12, Bytes: 900, KDrops: 1, PDrops: 2, QDrops: 3, Lost: 4},
						ID:              "3", State: "running", Path: "/exp/files/a.pcapng", Filter: "tcp", Size: 1024,
						Streams: []mm.CaptureStream{
							{
								CaptureCounters: mm.CaptureCounters{Packets: 12, Bytes: 900},
								Host:            "compute1", Bridge: "phenix", State: "running",
							},
						},
					},
					{ID: "4", State: "failed", Path: "/exp/files/b.pcap", VLANs: []string{"EXP_1", "EXP_2"}, Error: "boom"},
				})
			},
			want: `+----+----------+--------+---------+---------+-------+-------+------+--------------+--------+---------------------+-------+
| ID |   HOST   | BRIDGE |  STATE  | PACKETS | BYTES | DROPS | SIZE |    VLANS     | FILTER |      FILE PATH      | ERROR |
+----+----------+--------+---------+---------+-------+-------+------+--------------+--------+---------------------+-------+
|  3 |          |        | running |      12 |   900 |     6 | 1024 | all          | tcp    | /exp/files/a.pcapng |       |
|    | compute1 | phenix | running |      12 |   900 |     0 |      |              |        |                     |       |
|  4 |          |        | failed  |       0 |     0 |     0 |    0 | EXP_1, EXP_2 |        | /exp/files/b.pcap   | boom  |
+----+----------+--------+---------+---------+-------+-------+------+--------------+--------+---------------------+-------+
`,
		},
		{
			name: "mirror budgets",
			render: func(w io.Writer) {
				PrintTableOfMirrorBudgets(w, []capture.Budget{
					{Host: "compute1", Bridge: "phenix", Total: 29, Experiment: 18, Limit: 32, Level: "near"},
				})
			},
			want: `+----------+--------+---------+------------+-------+-------+
|   HOST   | BRIDGE | MIRRORS | EXPERIMENT | LIMIT | LEVEL |
+----------+--------+---------+------------+-------+-------+
| compute1 | phenix |      29 |         18 |    32 | near  |
+----------+--------+---------+------------+-------+-------+
`,
		},
		{
			name: "settings",
			render: func(w io.Writer) {
				PrintTableOfSettings(w, []types.Setting{{Spec: &v2.Setting{Name: "PasswordMinLength", Category: "Password", Value: "8"}}})
			},
			want: `+-------------------+----------+-------+
|       NAME        | CATEGORY | VALUE |
+-------------------+----------+-------+
| PasswordMinLength | Password |     8 |
+-------------------+----------+-------+
`,
		},
		{
			name: "runtime settings",
			render: func(w io.Writer) {
				PrintTableOfRuntimeSettings(w, map[string]any{
					"ui": map[string]any{"port": 3000, "tls": map[string]any{"enabled": false}}, "base-dir": "/phenix",
				})
			},
			want: `+----------------+---------+
|      KEY       |  VALUE  |
+----------------+---------+
| base-dir       | /phenix |
| ui.port        | 3000    |
| ui.tls.enabled | false   |
+----------------+---------+
`,
		},
		{
			name: "one VM",
			render: func(w io.Writer) {
				PrintTableOfVMs(w, plain, true, mm.VM{
					Host: "compute1", Name: "router", Running: true, Disk: "/images/r.qc2", Uptime: 90, CPUs: 2, RAM: 1024,
					OSType: "linux", Networks: []string{"EXP_1 (101)"}, IPv4: []string{"10.0.0.1"}, Taps: []string{"mega_tap1"},
					Labels:   map[string]string{"zone": "dmz", "role": "edge", "app": "fw"},
					Metadata: map[string]any{"k": "v"},
				})
			},
			want: `+------------+----------------------------------------+
|  SETTING   |                 VALUE                  |
+------------+----------------------------------------+
| Host       | compute1                               |
| Name       | router                                 |
| Running    | true                                   |
| Disk       | /images/r.qc2                          |
| Interfaces | ID: 0, IP: 10.0.0.1, VLAN: EXP_1 (101) |
| Uptime     | 1m30s                                  |
| VCPUs      | 2                                      |
| Memory     | 1024                                   |
| OS Type    | linux                                  |
| Taps       | mega_tap1                              |
| Labels     | app: fw, role: edge, zone: dmz         |
| Metadata   | {                                      |
|            |   "k": "v"                             |
|            | }                                      |
+------------+----------------------------------------+
`,
		},
		{
			name: "job",
			render: func(w io.Writer) {
				PrintJob(w, plain, job.Job{
					ID: "j1", Kind: "image build", Title: "Build jammy", Status: job.StatusFailed, Experiment: "exp",
					Owner: "admin", Created: finished.Add(-time.Hour), Finished: &finished, Error: "out of space",
					Progress: job.Progress{Done: 1, Total: 2, Bytes: 512, BytesTotal: 2048},
					Artifact: &job.Artifact{Name: "jammy.qc2", Size: 512, Incomplete: true},
					Items:    []job.Item{{Name: "a", Status: job.ItemFailed, Retryable: true, Error: "full"}},
				})
			},
			want: `ID:         j1
Kind:       image build
Title:      Build jammy
Status:     failed
Progress:   1/2 (25% of 2048 bytes)
Experiment: exp
Owner:      admin
Created:    2026-10-30T12:00:00Z
Finished:   2026-10-30T13:00:00Z
Error:      out of space
Artifact:   jammy.qc2 (512 bytes), incomplete

+------+--------+-----------+-------+
| ITEM | STATUS | RETRYABLE | ERROR |
+------+--------+-----------+-------+
| a    | failed | yes       | full  |
+------+--------+-----------+-------+
`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer

			tc.render(&out)

			if diff := cmp.Diff(tc.want, out.String()); diff != "" {
				t.Errorf("%s table mismatch (-want +got):\n%s", tc.name, diff)
			}
		})
	}
}
