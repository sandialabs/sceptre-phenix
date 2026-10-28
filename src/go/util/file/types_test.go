package file

import (
	"fmt"
	"io/fs"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/go-cmp/cmp"
)

func sortTestFiles() Files {
	day := func(d int) time.Time { return time.Date(2024, 1, d, 0, 0, 0, 0, time.UTC) }

	return Files{
		{Name: "b.pcap", Path: "host2/b.pcap", Size: 30, dateTime: day(2)},
		{Name: "A.pcap", Path: "Host3/A.pcap", Size: 10, dateTime: day(3)},
		{Name: "c.pcap", Path: "host1/c.pcap", Size: 20, dateTime: day(1)},
	}
}

func TestFilesSortBy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		col  string
		asc  bool
		want []string
	}{
		{"name", true, []string{"A.pcap", "b.pcap", "c.pcap"}},
		{"name", false, []string{"c.pcap", "b.pcap", "A.pcap"}},
		{"date", true, []string{"c.pcap", "b.pcap", "A.pcap"}},
		{"date", false, []string{"A.pcap", "b.pcap", "c.pcap"}},
		{"path", true, []string{"c.pcap", "b.pcap", "A.pcap"}},
		{"path", false, []string{"A.pcap", "b.pcap", "c.pcap"}},
		{"size", true, []string{"A.pcap", "c.pcap", "b.pcap"}},
		{"size", false, []string{"b.pcap", "c.pcap", "A.pcap"}},
		{"category", true, []string{"b.pcap", "A.pcap", "c.pcap"}},
		{"", false, []string{"b.pcap", "A.pcap", "c.pcap"}},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s asc=%t", tt.col, tt.asc), func(t *testing.T) {
			t.Parallel()

			files := sortTestFiles()
			files.SortBy(tt.col, tt.asc)

			if diff := cmp.Diff(tt.want, names(files)); diff != "" {
				t.Errorf("SortBy(%q, %t) mismatch (-want +got):\n%s", tt.col, tt.asc, diff)
			}
		})
	}
}

func TestFilesPaginate(t *testing.T) {
	t.Parallel()

	five := Files{{Name: "a"}, {Name: "b"}, {Name: "c"}, {Name: "d"}, {Name: "e"}}

	tests := []struct {
		name  string
		files Files
		page  int
		size  int
		want  []string
	}{
		{"first page", five, 1, 2, []string{"a", "b"}},
		{"middle page", five, 2, 2, []string{"c", "d"}},
		{"last partial page", five, 3, 2, []string{"e"}},
		{"page past the end", five, 4, 2, []string{}},
		{"page just past a full last page", five, 2, 5, []string{}},
		{"size larger than the list", five, 1, 10, []string{"a", "b", "c", "d", "e"}},
		{"size equal to the list", five, 1, 5, []string{"a", "b", "c", "d", "e"}},
		{"size zero", five, 1, 0, []string{}},
		{"empty list", Files{}, 1, 10, []string{}},
		{"nil list", nil, 1, 10, []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.files.Paginate(tt.page, tt.size)
			if diff := cmp.Diff(tt.want, names(got)); diff != "" {
				t.Errorf("Paginate(%d, %d) mismatch (-want +got):\n%s", tt.page, tt.size, diff)
			}
		})
	}
}

func TestMakeFile(t *testing.T) {
	t.Parallel()

	modified := time.Date(2024, 1, 15, 10, 30, 45, 0, time.UTC)
	fsys := fstest.MapFS{
		"exp/vm_eth0.pcap": {Data: []byte("12345"), ModTime: modified},
		"exp/logs":         {Mode: fs.ModeDir, ModTime: modified},
	}

	tests := []struct {
		name string
		want File
	}{
		{"exp/vm_eth0.pcap", File{
			Name: "vm_eth0.pcap", Path: "/base/vm_eth0.pcap", Date: "2024-01-15T10:30:45Z", Size: 5,
			Categories: []string{}, dateTime: modified,
		}},
		{"exp/logs", File{
			Name: "logs", Path: "/base/logs", Date: "2024-01-15T10:30:45Z", Categories: []string{}, IsDir: true,
			dateTime: modified,
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			info, err := fs.Stat(fsys, tt.name)
			if err != nil {
				t.Fatalf("fs.Stat(%q): %v", tt.name, err)
			}

			got := MakeFile(info, "/base")
			if diff := cmp.Diff(tt.want, got, cmp.AllowUnexported(File{})); diff != "" {
				t.Errorf("MakeFile(%q, %q) mismatch (-want +got):\n%s", tt.name, "/base", diff)
			}
		})
	}
}
