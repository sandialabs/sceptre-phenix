package vm

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/api/disk"
	"phenix/util/mm"
)

// FuzzDumpGuestMemoryCommand checks the memory dump command for any path that
// validateDumpPath accepts: the QMP text is one single-quoted minimega
// argument, and it decodes to a dump of the VM's memory to that same path.
func FuzzDumpGuestMemoryCommand(f *testing.F) {
	for _, path := range []string{
		"/phenix/images/exp/files/mem.elf", "/dumps/a b.elf", `/dumps/a"b.elf`, `/dumps/a'b.elf`, `/dumps/a\b.elf`,
		"/dumps/$HOME.elf", "/dumps/ünïcode.elf", "/dumps/\x00.elf", "/dumps/\xff.elf", "",
	} {
		f.Add(path)
	}

	const prefix = "vm qmp vm1 '"

	f.Fuzz(func(t *testing.T, path string) {
		if validateDumpPath(path) != nil {
			return
		}

		got := dumpGuestMemoryCommand("vm1", path)

		qmp, ok := strings.CutPrefix(got, prefix)
		if !ok || !strings.HasSuffix(qmp, "'") {
			t.Fatalf("dumpGuestMemoryCommand(%q, %q) = %q, want %s<QMP>'", "vm1", path, got, prefix)
		}

		qmp = strings.TrimSuffix(qmp, "'")
		if strings.Contains(qmp, "'") {
			t.Fatalf("dumpGuestMemoryCommand(%q, %q) = %q, want no quote inside the QMP argument", "vm1", path, got)
		}

		var decoded struct {
			Execute   string         `json:"execute"`
			Arguments map[string]any `json:"arguments"`
		}

		if err := json.Unmarshal([]byte(qmp), &decoded); err != nil {
			t.Fatalf("dumpGuestMemoryCommand(%q, %q) QMP %q does not decode: %v", "vm1", path, qmp, err)
		}

		want := map[string]any{"protocol": "file:" + path, "paging": false, "format": "elf", "detach": true}
		if decoded.Execute != "dump-guest-memory" || !cmp.Equal(want, decoded.Arguments) {
			t.Fatalf("dumpGuestMemoryCommand(%q, %q) QMP = %s, want dump-guest-memory with %v", "vm1", path, qmp, want)
		}
	})
}

// FuzzValidateDiskSpec checks that ValidateDiskSpec accepts only a disk spec
// that minimega takes as one literal argument: a safe path and at most two
// more fields, which are an interface or a cache mode. A rejection wraps
// ErrInvalidDisk.
func FuzzValidateDiskSpec(f *testing.F) {
	for _, spec := range []string{
		"", "new.qc2", "/phenix/images/new.qc2,virtio", "new.qc2,writeback", "new.qc2,ide,none", "new.qc2,bogus",
		",virtio", "new.qc2,,", "a b.qc2,virtio", "$HOME.qc2", "new.qc2,virtio,none,x", "a\x00.qc2", "\xff.qc2",
	} {
		f.Add(spec)
	}

	f.Fuzz(func(t *testing.T, spec string) {
		err := ValidateDiskSpec(spec)
		if err != nil {
			if !errors.Is(err, ErrInvalidDisk) {
				t.Fatalf("ValidateDiskSpec(%q) error = %v, want one wrapping ErrInvalidDisk", spec, err)
			}

			return
		}

		if spec == "" {
			return
		}

		fields := strings.Split(spec, ",")
		if len(fields) > 3 {
			t.Fatalf("ValidateDiskSpec(%q) = nil for %d fields, want at most 3", spec, len(fields))
		}

		for _, field := range fields {
			if field == "" || disk.ValidateMinimegaPath(field) != nil {
				t.Fatalf("ValidateDiskSpec(%q) = nil with field %q, want each field one safe argument", spec, field)
			}
		}

		if path, err := mm.DiskSpecPath(spec); err != nil || path != fields[0] {
			t.Fatalf("mm.DiskSpecPath(%q) = %q, %v, want %q, nil", spec, path, err, fields[0])
		}
	})
}
