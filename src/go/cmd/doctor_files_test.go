package cmd

import (
	"encoding/binary"
	"hash/fnv"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/spf13/viper"
	"go.etcd.io/bbolt"
	"pgregory.net/rapid"
)

const doctorFilesPageSize = 4096

// doctorFilesBolt is a new BoltDB file of four pageSize pages, laid out as
// bbolt writes one: two meta pages, a free list and an empty leaf.
func doctorFilesBolt(pageSize int) []byte {
	order := binary.NativeEndian
	data := make([]byte, 4*pageSize)

	for i := range 2 {
		page := data[i*pageSize:]
		order.PutUint64(page[0:], uint64(i))
		order.PutUint16(page[8:], 0x04) // a meta page

		meta := page[boltPageHeader:]
		order.PutUint32(meta[0:], boltMagic)
		order.PutUint32(meta[4:], boltVersion)
		order.PutUint32(meta[8:], uint32(pageSize))
		order.PutUint64(meta[16:], 3) // the root bucket's page
		order.PutUint64(meta[32:], 2) // the free list's page
		order.PutUint64(meta[40:], 4) // the high water mark
		order.PutUint64(meta[48:], uint64(i))
		doctorFilesResum(page)
	}

	return data
}

// doctorFilesResum writes the checksum of the meta page at the start of page.
func doctorFilesResum(page []byte) {
	hash := fnv.New64a()
	_, _ = hash.Write(page[boltPageHeader : boltPageHeader+boltMetaSummed])
	binary.NativeEndian.PutUint64(page[boltPageHeader+boltMetaSummed:], hash.Sum64())
}

// doctorFilesReadAt reads from data as osReadAt reads from a file.
func doctorFilesReadAt(data []byte) func(string, int64, int) ([]byte, error) {
	return func(_ string, off int64, n int) ([]byte, error) {
		if off >= int64(len(data)) {
			return nil, nil
		}

		return data[off:min(off+int64(n), int64(len(data)))], nil
	}
}

// doctorFilesOwned is file information whose owner is uid.
type doctorFilesOwned struct {
	os.FileInfo

	uid uint32
}

func (o doctorFilesOwned) Sys() any {
	return &syscall.Stat_t{Uid: o.uid}
}

// The meta pages doctorFilesBolt writes are the ones bbolt writes, so the
// crafted files below stand for files of the bbolt phenix uses.
func TestDoctorFilesBoltMatchesBbolt(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "store.bdb")

	db, err := bbolt.Open(path, 0o600, &bbolt.Options{Timeout: time.Second, PageSize: doctorFilesPageSize})
	if err != nil {
		t.Fatalf("bbolt.Open() error = %v", err)
	}

	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}

	want := doctorFilesBolt(doctorFilesPageSize)
	metaEnd := boltPageHeader + boltMetaSize

	for _, off := range []int{0, doctorFilesPageSize} {
		if diff := cmp.Diff(want[off:off+metaEnd], got[off:off+metaEnd]); diff != "" {
			t.Errorf("meta page at %d mismatch (-crafted +bbolt):\n%s", off, diff)
		}
	}

	env := fakeDoctorEnv(nil, false)
	if check := env.storeFileCheck(path, int64(len(got))); check.Status != doctorPass {
		t.Errorf("storeFileCheck(bbolt file) = %+v, want pass", check)
	}
}

// With its first page zeroed, as a torn write leaves it, a store file opens
// in bbolt exactly when doctor finds the second meta page valid: only when the
// file's pages are the size of the host's.
func TestStoreFileCheckMatchesBbolt(t *testing.T) {
	t.Parallel()

	host := os.Getpagesize()

	for _, pageSize := range []int{host, 2 * host} {
		path := filepath.Join(t.TempDir(), "store.bdb")

		db, err := bbolt.Open(path, 0o600, &bbolt.Options{Timeout: time.Second, PageSize: pageSize})
		if err != nil {
			t.Fatalf("bbolt.Open() error = %v", err)
		}

		if err := db.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("os.ReadFile() error = %v", err)
		}

		clear(data[:pageSize])

		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatalf("os.WriteFile() error = %v", err)
		}

		env := fakeDoctorEnv(nil, false)
		env.pageSize = host
		check := env.storeFileCheck(path, int64(len(data)))

		db, err = bbolt.Open(path, 0o600, &bbolt.Options{Timeout: time.Second, ReadOnly: true})
		if err == nil {
			_ = db.Close()
		}

		if opens := err == nil; opens != (check.Status != doctorFail) || opens != (pageSize == host) {
			t.Errorf("%d-byte pages: bbolt.Open() error = %v, and storeFileCheck() = %+v", pageSize, err, check)
		}
	}
}

// A store file passes only when it holds two meta pages bbolt accepts: a
// file that is not BoltDB or is truncated fails, and one bad meta page warns.
func TestStoreFileCheck(t *testing.T) {
	t.Parallel()

	order := binary.NativeEndian
	valid := doctorFilesBolt(doctorFilesPageSize)

	// edit returns valid changed by fn, with the checksums left as they are
	edit := func(fn func([]byte)) []byte {
		data := slices.Clone(valid)
		fn(data)

		return data
	}
	// resummed returns valid changed by fn in the first meta page, with its checksum
	resummed := func(fn func(meta []byte)) []byte {
		return edit(func(data []byte) {
			fn(data[boltPageHeader:])
			doctorFilesResum(data)
		})
	}
	// resummedBoth returns valid changed by fn in both meta pages, with their checksums
	resummedBoth := func(fn func(meta []byte)) []byte {
		return edit(func(data []byte) {
			for _, page := range [][]byte{data, data[doctorFilesPageSize:]} {
				fn(page[boltPageHeader:])
				doctorFilesResum(page)
			}
		})
	}

	tests := map[string]struct {
		data       []byte
		pageSize   int // of the host; 0 is doctorFilesPageSize
		want       doctorStatus
		wantDetail string
	}{
		"new file":   {data: valid, want: doctorPass, wantDetail: "BoltDB version 2, 4096-byte pages, 16384 bytes"},
		"empty file": {data: []byte{}, want: doctorPass, wantDetail: "is empty"},
		"no checksums": {
			data: edit(func(data []byte) {
				clear(data[boltPageHeader+boltMetaSummed : boltPageHeader+boltMetaSize])
				clear(data[doctorFilesPageSize+boltPageHeader+boltMetaSummed : doctorFilesPageSize+boltPageHeader+boltMetaSize])
			}),
			want: doctorPass,
		},
		"text file": {data: []byte("hello\n"), want: doctorFail, wantDetail: "is 6 bytes, too short for a BoltDB file"},
		"wrong magic": {
			data:       resummedBoth(func(meta []byte) { order.PutUint32(meta, 0xCAFEBABE) }),
			want:       doctorFail,
			wantDetail: "is not a BoltDB file: it starts with magic number 0xcafebabe, not 0xed0cdaed",
		},
		"wrong magic in the first meta page": {
			data:       resummed(func(meta []byte) { order.PutUint32(meta, 0xCAFEBABE) }),
			want:       doctorWarn,
			wantDetail: "the first meta page of /phenix/store.bdb is corrupt (magic number 0xcafebabe), so BoltDB reads the second",
		},
		"first page zeroed": {
			data:       edit(func(data []byte) { clear(data[:doctorFilesPageSize]) }),
			want:       doctorWarn,
			wantDetail: "is corrupt (magic number 0x0), so BoltDB reads the second",
		},
		"first page zeroed, on a host of larger pages": {
			data:       append(edit(func(data []byte) { clear(data[:doctorFilesPageSize]) }), make([]byte, 4*doctorFilesPageSize)...),
			pageSize:   4 * doctorFilesPageSize,
			want:       doctorFail,
			wantDetail: "is not a BoltDB file: it starts with magic number 0x0",
		},
		"short file that is not BoltDB": {
			data: make([]byte, 5000), want: doctorFail, wantDetail: "is not a BoltDB file: it starts with magic number 0x0",
		},
		"other byte order": {
			data:       resummed(func(meta []byte) { order.PutUint32(meta, 0xEDDA0CED) }),
			want:       doctorFail,
			wantDetail: "other byte order",
		},
		"wrong version": {
			data:       resummedBoth(func(meta []byte) { order.PutUint32(meta[4:], 1) }),
			want:       doctorFail,
			wantDetail: "is BoltDB format version 1, but phenix reads version 2",
		},
		"wrong version in the first meta page": {
			data:       resummed(func(meta []byte) { order.PutUint32(meta[4:], 1) }),
			want:       doctorWarn,
			wantDetail: "is corrupt (format version 1), so BoltDB reads the second",
		},
		"wrong page size": {
			data:       resummed(func(meta []byte) { order.PutUint32(meta[8:], 3000) }),
			want:       doctorFail,
			wantDetail: "page size of 3000",
		},
		"truncated to one page": {
			data:       valid[:doctorFilesPageSize],
			want:       doctorFail,
			wantDetail: "is 4096 bytes, shorter than its two 4096-byte meta pages, so it is truncated",
		},
		"truncated in the second page": {
			data: valid[:doctorFilesPageSize+100], want: doctorFail, wantDetail: "truncated",
		},
		"shorter than bbolt reads": {
			data: doctorFilesBolt(1024)[:3000], want: doctorFail,
			wantDetail: "is 3000 bytes, shorter than the 4096 bytes BoltDB reads first, so BoltDB refuses it",
		},
		"first meta page corrupt": {
			data:       edit(func(data []byte) { data[boltPageHeader+48]++ }),
			want:       doctorWarn,
			wantDetail: "the first meta page of /phenix/store.bdb is corrupt (bad checksum)",
		},
		"second meta page corrupt": {
			data:       edit(func(data []byte) { data[doctorFilesPageSize+boltPageHeader+48]++ }),
			want:       doctorWarn,
			wantDetail: "the second meta page of",
		},
		"both meta pages corrupt": {
			data: edit(func(data []byte) {
				data[boltPageHeader+48]++
				data[doctorFilesPageSize+boltPageHeader+48]++
			}),
			want:       doctorFail,
			wantDetail: "both meta pages of",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			env := fakeDoctorEnv(nil, false)
			env.readAt = doctorFilesReadAt(tc.data)

			if tc.pageSize != 0 {
				env.pageSize = tc.pageSize
			}

			got := env.storeFileCheck("/phenix/store.bdb", int64(len(tc.data)))
			if got.Status != tc.want || !strings.Contains(got.Detail, tc.wantDetail) {
				t.Errorf("storeFileCheck() = %+v, want %s with %q", got, tc.want, tc.wantDetail)
			}
		})
	}
}

// A file of any bytes is checked without a panic, and a change to any byte a
// meta page's checksum covers keeps the file from passing.
func TestStoreFileCheckProperty(t *testing.T) {
	t.Parallel()

	valid := doctorFilesBolt(doctorFilesPageSize)

	rapid.Check(t, func(t *rapid.T) {
		var data []byte

		if rapid.Bool().Draw(t, "random") {
			data = rapid.SliceOfN(rapid.Byte(), 0, 3*doctorFilesPageSize).Draw(t, "data")
		} else {
			data = slices.Clone(valid)
			page := rapid.SampledFrom([]int{0, doctorFilesPageSize}).Draw(t, "page")
			at := page + boltPageHeader + rapid.IntRange(0, boltMetaSummed-1).Draw(t, "offset")
			data[at] ^= rapid.ByteRange(1, 255).Draw(t, "flip")
		}

		env := fakeDoctorEnv(nil, false)
		env.readAt = doctorFilesReadAt(data)

		if got := env.storeFileCheck("store.bdb", int64(len(data))); got.Status == doctorPass && len(data) > 0 {
			t.Fatalf("storeFileCheck() = %+v, want warn or fail", got)
		}
	})
}

// The store's owner warns when it is another user and this one is not root,
// and as root under sudo phenix when it is not the user sudo phenix resolves.
func TestStoreOwnerCheck(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		owner     uint32
		uid       int
		phenixUID string
		want      []doctorCheck
	}{
		"own store": {
			owner: 1000, uid: 1000, phenixUID: "1000",
			want: []doctorCheck{row("", doctorPass, "/s.bdb belongs to alice (uid 1000)")},
		},
		"another user's store": {
			owner: 0, uid: 1000, phenixUID: "1000",
			want: []doctorCheck{row("", doctorWarn,
				"/s.bdb belongs to root (uid 0), not to this user, alice (uid 1000), and "+
					"phenix creates the store readable by its owner only")},
		},
		"unknown owner": {
			owner: 4242, uid: 1000, phenixUID: "1000",
			want: []doctorCheck{row("", doctorWarn,
				"/s.bdb belongs to uid 4242, not to this user, alice (uid 1000), and phenix creates the store readable by its owner only")},
		},
		"root reads any store": {
			owner: 1000, uid: 0, phenixUID: "0",
			want: []doctorCheck{row("", doctorPass, "/s.bdb belongs to alice (uid 1000)")},
		},
		"sudo phenix on a store root created": {
			owner: 0, uid: 0, phenixUID: "1000",
			want: []doctorCheck{row("", doctorWarn,
				"/s.bdb belongs to root (uid 0), but sudo phenix uses the store of alice (uid 1000), who cannot open it without sudo")},
		},
		"sudo phenix on the user's store": {
			owner: 1000, uid: 0, phenixUID: "1000",
			want: []doctorCheck{row("", doctorPass, "/s.bdb belongs to alice (uid 1000)")},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			env := fakeDoctorEnv(nil, tc.uid == 0)
			got := env.storeOwnerCheck("/s.bdb", doctorFilesOwned{FileInfo: nil, uid: tc.owner}, tc.uid, tc.phenixUID)

			if diff := cmp.Diff(tc.want, got, cmp.AllowUnexported(doctorCheck{})); diff != "" {
				t.Errorf("storeOwnerCheck() mismatch (-want +got):\n%s", diff)
			}
		})
	}

	info, err := os.Stat(".")
	if err != nil {
		t.Fatalf("os.Stat() error = %v", err)
	}

	if got := fakeDoctorEnv(nil, false).storeOwnerCheck("/s.bdb", doctorFilesNoOwner{info}, 1000, "1000"); got != nil {
		t.Errorf("storeOwnerCheck(no owner) = %+v, want no rows", got)
	}
}

// doctorFilesNoOwner is file information without an owner.
type doctorFilesNoOwner struct{ os.FileInfo }

func (doctorFilesNoOwner) Sys() any { return nil }

// A per-user default store warns while the store of a root phenix ui
// exists, or cannot be seen; root, a set endpoint and that store give no row.
func TestStoreRootCheck(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	system := writeTestFile(t, dir, "store.bdb", "")
	user := "bolt://" + filepath.Join(dir, "home", ".phenix.bdb")

	tests := map[string]struct {
		endpoint, source, phenixUID, system string
		statErr                             error
		want                                []doctorStatus
	}{
		"root store exists": {endpoint: user, source: sourceDefault, phenixUID: "1000", system: system, want: []doctorStatus{doctorWarn}},
		"no root store": {
			endpoint: user, source: sourceDefault, phenixUID: "1000", system: filepath.Join(dir, "none.bdb"),
			want: []doctorStatus{doctorPass},
		},
		"root store unseen": {
			endpoint: user, source: sourceDefault, phenixUID: "1000", system: system, statErr: os.ErrPermission,
			want: []doctorStatus{doctorWarn},
		},
		"root":             {endpoint: "bolt://" + system, source: sourceDefault, phenixUID: "0", system: system},
		"endpoint set":     {endpoint: user, source: sourceEnv, phenixUID: "1000", system: system},
		"the root store":   {endpoint: "bolt://" + system, source: sourceDefault, phenixUID: "1000", system: system},
		"invalid endpoint": {endpoint: "etcd://x", source: sourceDefault, phenixUID: "1000", system: system},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			env := fakeDoctorEnv(nil, false)
			if tc.statErr != nil {
				env.stat = func(string) (os.FileInfo, error) { return nil, tc.statErr }
			}

			got := env.storeRootCheck(tc.endpoint, tc.source, tc.phenixUID, tc.system)
			if diff := cmp.Diff(tc.want, checkStatuses(got), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("storeRootCheck() mismatch (-want +got):\n%s\n%+v", diff, got)
			}
		})
	}
}

// checkStatuses is the status of each check.
func checkStatuses(checks []doctorCheck) []doctorStatus {
	got := make([]doctorStatus, 0, len(checks))
	for _, c := range checks {
		got = append(got, c.Status)
	}

	return got
}

// The config paths row lists each config and users file in search order,
// marks the first of each name read, and warns when phenix ignores one.
func TestConfigPathsCheck(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		files      []string // under the directories a, b and c
		paths      []string // the directories searched, besides "." under dir; default ., b, c
		linkC      bool     // c is a symbolic link to b
		want       doctorStatus
		wantDetail string
	}{
		"one config file": {
			files: []string{"b/config.yaml"}, want: doctorPass,
			wantDetail: "searched ., B, C, and found B/config.yaml (read)",
		},
		"config and users files": {
			files: []string{"b/config.yaml", "c/users.yaml"}, want: doctorPass,
			wantDetail: "found B/config.yaml (read), C/users.yaml (read)",
		},
		"none": {want: doctorPass, wantDetail: "found no config or users file"},
		"two config files": {
			files: []string{"a/config.yaml", "c/config.yaml"}, want: doctorWarn,
			wantDetail: "phenix reads only the first file of each name, so it ignores C/config.yaml " +
				"(searched ., B, C, and found A/config.yaml (read), C/config.yaml (ignored))",
		},
		"two users files": {
			files: []string{"b/users.yaml", "c/users.yml"}, want: doctorWarn, wantDetail: "so it ignores C/users.yml (",
		},
		"two extensions in one directory": {
			files: []string{"b/config.yaml", "b/config.json"}, want: doctorWarn,
			wantDetail: "found B/config.json (read), B/config.yaml (ignored)",
		},
		"a directory named like a config file": {
			files: []string{"b/config.yaml/"}, want: doctorPass, wantDetail: "found no config or users file",
		},
		"the working directory on the path again": {
			files: []string{"a/config.yaml", "a/users.yaml"}, paths: []string{".", "a", "c"}, want: doctorPass,
			wantDetail: "searched ., A, C, and found A/config.yaml (read), A/users.yaml (read)",
		},
		"a link to a directory on the path": {
			files: []string{"b/config.yaml"}, linkC: true, want: doctorPass,
			wantDetail: "searched ., B, C, and found B/config.yaml (read)",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			for _, file := range tc.files {
				doctorFilesCreate(t, filepath.Join(dir, file), strings.HasSuffix(file, "/"))
			}

			abs := func(sub string) string { return filepath.Join(dir, sub) }
			replacer := strings.NewReplacer(abs("a"), "A", abs("b"), "B", abs("c"), "C")

			if tc.linkC {
				if err := os.Symlink(abs("b"), abs("c")); err != nil {
					t.Fatalf("os.Symlink() error = %v", err)
				}
			}

			paths := []string{".", abs("b"), abs("c")}
			if tc.paths != nil {
				paths = make([]string, len(tc.paths))
				for i, p := range tc.paths {
					paths[i] = p
					if p != "." {
						paths[i] = abs(p)
					}
				}
			}

			got := fakeDoctorEnv(nil, false).configPathsCheck(paths, abs("a"))
			got.Detail = replacer.Replace(got.Detail)

			if got.Status != tc.want || !strings.Contains(got.Detail, tc.wantDetail) {
				t.Errorf("configPathsCheck() = %+v, want %s with %q", got, tc.want, tc.wantDetail)
			}
		})
	}

	env := fakeDoctorEnv(nil, false)
	env.stat = func(string) (os.FileInfo, error) { return nil, os.ErrPermission }

	got := env.configPathsCheck([]string{"/etc/phenix"}, "/")
	if want := "searched /etc/phenix, and found no config or users file, but this user cannot search /etc/phenix"; got.Detail != want {
		t.Errorf("configPathsCheck(unsearchable) detail = %q, want %q", got.Detail, want)
	}
}

// doctorFilesCreate creates the config file path, or with dir a directory.
func doctorFilesCreate(t interface {
	Helper()
	Fatalf(format string, args ...any)
}, path string, dir bool,
) {
	t.Helper()

	if dir {
		if err := os.MkdirAll(path, 0o750); err != nil {
			t.Fatalf("os.MkdirAll(%s) error = %v", path, err)
		}

		return
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("os.MkdirAll(%s) error = %v", filepath.Dir(path), err)
	}

	if err := os.WriteFile(path, []byte("log:\n  level: info\n"), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%s) error = %v", path, err)
	}
}

// For any config files in the search path, the file the row marks read is
// the one viper reads, as the root command does.
func TestConfigPathsCheckProperty(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	run := 0

	rapid.Check(t, func(t *rapid.T) {
		run++
		dir := filepath.Join(base, strconv.Itoa(run))
		paths := []string{filepath.Join(dir, "a"), filepath.Join(dir, "b"), filepath.Join(dir, "c")}

		candidates := make([]string, 0, len(paths)*4)
		for _, p := range paths {
			for _, ext := range []string{"yaml", "yml", "json", "toml"} {
				candidates = append(candidates, filepath.Join(p, "config."+ext))
			}
		}

		files := rapid.SliceOfNDistinct(rapid.SampledFrom(candidates), 0, 4, rapid.ID).Draw(t, "files")
		for _, file := range files {
			doctorFilesCreate(t, file, false)
		}

		v := viper.New()
		v.SetConfigName("config")

		for _, p := range paths {
			v.AddConfigPath(p)
		}

		_ = v.ReadInConfig()

		got := fakeDoctorEnv(nil, false).configPathsCheck(paths, dir)

		var read string
		if _, after, ok := strings.Cut(got.Detail, "found "); ok {
			if file, _, ok := strings.Cut(after, " (read)"); ok {
				read = file
			}
		}

		if read != v.ConfigFileUsed() {
			t.Fatalf("configPathsCheck() marks %q read, viper reads %q; detail: %s", read, v.ConfigFileUsed(), got.Detail)
		}

		if want := len(files) > 1; (got.Status == doctorWarn) != want {
			t.Fatalf("configPathsCheck() = %s for %d files", got.Status, len(files))
		}
	})
}

// phenix settings set must write the config file phenix reads, and be able
// to: a different file, a file that is not YAML or one it cannot write warns.
func TestSettingsFileCheck(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	file := writeTestFile(t, dir, "config.yaml", "")
	other := writeTestFile(t, dir, "config.json", "{}")

	tests := map[string]struct {
		target, read string
		readOnly     bool
		want         doctorCheck
	}{
		"the file read": {
			target: file, read: file,
			want: row("", doctorPass, "phenix settings set writes "+file),
		},
		"none read": {
			target: filepath.Join(dir, "phenix", "config.yaml"), read: "",
			want: row("", doctorPass, "phenix settings set writes "+filepath.Join(dir, "phenix", "config.yaml")+" (created on first use)"),
		},
		"another file": {
			target: filepath.Join(dir, "home", "config.yaml"), read: file,
			want: row("", doctorWarn, "phenix settings set writes "+filepath.Join(dir, "home", "config.yaml")+", but phenix reads "+file),
		},
		"not YAML": {
			target: other, read: other,
			want: row("", doctorWarn, "phenix settings set and unset refuse "+other+", because they rewrite only YAML files"),
		},
		"not writable": {
			target: file, read: file, readOnly: true,
			want: row("", doctorWarn, "phenix settings set fails: "+file+" is not writable: permission denied"),
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			env := fakeDoctorEnv(nil, false)
			if tc.readOnly {
				env.access = func(string, uint32) error { return syscall.EACCES }
			}

			if diff := cmp.Diff(tc.want, env.settingsFileCheck(tc.target, tc.read), cmp.AllowUnexported(doctorCheck{})); diff != "" {
				t.Errorf("settingsFileCheck() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// osReadAt reads at most n bytes from an offset, and fewer at the end of the file.
func TestOSReadAt(t *testing.T) {
	t.Parallel()

	path := writeTestFile(t, t.TempDir(), "f", "0123456789")

	tests := map[string]struct {
		off  int64
		n    int
		want string
	}{
		"start":       {off: 0, n: 4, want: "0123"},
		"middle":      {off: 3, n: 4, want: "3456"},
		"past end":    {off: 8, n: 4, want: "89"},
		"after end":   {off: 20, n: 4, want: ""},
		"whole file":  {off: 0, n: 10, want: "0123456789"},
		"more than n": {off: 0, n: 64, want: "0123456789"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := osReadAt(path, tc.off, tc.n)
			if err != nil || string(got) != tc.want {
				t.Errorf("osReadAt(%d, %d) = %q, %v, want %q", tc.off, tc.n, got, err, tc.want)
			}
		})
	}

	if _, err := osReadAt(filepath.Join(t.TempDir(), "missing"), 0, 4); err == nil {
		t.Error("osReadAt(missing) error = nil, want one")
	}
}
