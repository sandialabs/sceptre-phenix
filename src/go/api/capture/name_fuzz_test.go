package capture

import "testing"

func FuzzFileName(f *testing.F) {
	for _, test := range fileNameTests {
		f.Add(test.name, test.format == "pcap")
	}

	f.Fuzz(func(t *testing.T, name string, pcap bool) {
		format := "pcapng"
		if pcap {
			format = "pcap"
		}

		got := fileName(name, format)
		if problem := checkFileName(got, format); problem != "" {
			t.Fatalf("fileName(%q, %s) = %q: %s", name, format, got, problem)
		}
	})
}
