package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"phenix/web/health"
)

// disk-space measures the filesystems of the images directory, minimega's -base and its log once each, warns when one
// has less free space than the larger of 10% and 5 GiB, and then names the minimega log and what leftovers found.
func TestDoctorDiskSpace(t *testing.T) {
	t.Parallel()

	const gib = 1 << 30

	tests := map[string]struct {
		devices   map[string]health.DiskStat // by the directory name under the test directory
		logfile   bool
		leftovers *doctorLeftovers
		statfsErr error
		want      func(dir, log string) []doctorCheck
	}{
		"enough space on one filesystem": {
			devices: map[string]health.DiskStat{
				"files": {Device: 1, Total: 100 * gib, Avail: 50 * gib}, "minimega": {Device: 1, Total: 100 * gib, Avail: 50 * gib},
			},
			leftovers: &doctorLeftovers{},
			want: func(dir, _ string) []doctorCheck {
				return []doctorCheck{
					row("disk-space files", doctorPass, filepath.Join(dir, "files")+" (the images directory) and "+
						filepath.Join(dir, "minimega")+" (minimega's -base): 50.0 GiB free of 100.0 GiB (50.0%)"),
					row(diskSpaceInjectRow, doctorPass, "no dstImg* directories in "+filepath.Join(dir, "minimega")),
				}
			},
		},
		"low space with the log and leftovers": {
			devices: map[string]health.DiskStat{
				"files": {Device: 1, Total: 100 * gib, Avail: 6 * gib}, "minimega": {Device: 2, Total: 20 * gib, Avail: 10 * gib},
				"log": {Device: 1, Total: 100 * gib, Avail: 6 * gib},
			},
			logfile:   true,
			leftovers: &doctorLeftovers{bytes: 3 * gib, items: 2},
			want: func(dir, log string) []doctorCheck {
				return []doctorCheck{
					row("disk-space files", doctorWarn, filepath.Join(dir, "files")+" (the images directory) and "+log+
						" (the minimega log): 6.0 GiB free of 100.0 GiB (6.0%), which is less than 10.0 GiB. "+
						"The minimega log "+log+" is 2.0 KiB. The leftovers rows name 2 snapshot files and directories of deleted "+
						"experiments, 3.0 GiB in all, that are safe to delete"),
					row("disk-space base", doctorPass, filepath.Join(dir, "minimega")+" (minimega's -base): "+
						"10.0 GiB free of 20.0 GiB (50.0%)"),
					row(diskSpaceInjectRow, doctorPass, "no dstImg* directories in "+filepath.Join(dir, "minimega")),
				}
			},
		},
		"low space, minimega logs to stdout, leftovers skipped": {
			devices: map[string]health.DiskStat{
				"files": {Device: 1, Total: 20 * gib, Avail: 4 * gib}, "minimega": {Device: 1, Total: 20 * gib, Avail: 4 * gib},
			},
			want: func(dir, _ string) []doctorCheck {
				return []doctorCheck{
					row("disk-space files", doctorWarn, filepath.Join(dir, "files")+" (the images directory) and "+
						filepath.Join(dir, "minimega")+" (minimega's -base): 4.0 GiB free of 20.0 GiB (20.0%), which is "+
						"less than 5.0 GiB. The leftovers check did not run, so it found no files that are safe to delete. "+
						"minimega gives no -logfile, so it logs to its standard output"),
					row(diskSpaceInjectRow, doctorPass, "no dstImg* directories in "+filepath.Join(dir, "minimega")),
				}
			},
		},
		"cannot measure": {
			statfsErr: errors.New("statfs: permission denied"),
			want: func(dir, _ string) []doctorCheck {
				return []doctorCheck{
					row("disk-space files", doctorSkip, "not checked, because the images directory "+filepath.Join(dir, "files")+
						" is not measured: statfs: permission denied. minimega's -base "+filepath.Join(dir, "minimega")+
						" is not measured: statfs: permission denied"),
					row(diskSpaceInjectRow, doctorPass, "no dstImg* directories in "+filepath.Join(dir, "minimega")),
				}
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := newDoctorTestDir(t)
			in := fakeDoctorInput(dir, nil)
			log := filepath.Join(dir, "log", "minimega.log")
			writeSized(t, log, 2048)

			args := map[string]string{"filepath": filepath.Join(dir, "files"), "base": filepath.Join(dir, "minimega"), "logfile": ""}
			if tc.logfile {
				args["logfile"] = log
			}

			in.found = &doctorFound{minimegaUp: true, minimegaArgs: args, leftovers: tc.leftovers}
			in.statfs = func(path string) (health.DiskStat, error) {
				if tc.statfsErr != nil {
					return health.DiskStat{}, tc.statfsErr
				}

				return tc.devices[filepath.Base(path)], nil
			}

			got := diskSpaceChecks(t.Context(), in)
			if diff := cmp.Diff(tc.want(dir, log), got); diff != "" {
				t.Errorf("diskSpaceChecks() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// disk-space gives up on a stat or statfs that does not return within the doctor timeout, as on a hung network mount.
func TestDoctorDiskSpaceHung(t *testing.T) {
	t.Parallel()

	for _, call := range []string{"stat", "statfs"} {
		t.Run(call, func(t *testing.T) {
			t.Parallel()

			dir := newDoctorTestDir(t)
			in := fakeDoctorInput(dir, nil)
			in.timeout = 10 * time.Millisecond
			in.found = &doctorFound{minimegaUp: true, minimegaArgs: map[string]string{"filepath": filepath.Join(dir, "files")}}

			hung := make(chan struct{})
			t.Cleanup(func() { close(hung) })

			if call == "stat" {
				stat := in.env.stat
				in.env.stat = func(path string) (os.FileInfo, error) {
					if strings.HasPrefix(path, dir) {
						<-hung
					}

					return stat(path)
				}
			} else {
				in.statfs = func(string) (health.DiskStat, error) {
					<-hung

					return health.DiskStat{}, nil
				}
			}

			got := diskSpaceChecks(t.Context(), in)[0]
			if got.Status != doctorSkip || !strings.Contains(got.Detail, "the filesystem did not answer within 10ms") {
				t.Errorf("diskSpaceChecks() first row = %+v, want a skip for the hung %s", got, call)
			}
		})
	}
}

// The inject dirs row warns about the dstImg* directories in minimega's -base, and names those still mounted.
func TestDoctorInjectDirs(t *testing.T) {
	t.Parallel()

	host := doctorGoodHost()
	in := hostInput(t, host, doctorHostTools)
	base := t.TempDir()

	for _, sub := range []string{"dstImg123", "dstImg456", "files", "dstImg.txt"} {
		if err := os.Mkdir(filepath.Join(base, sub), 0o750); err != nil {
			t.Fatalf("os.Mkdir(%s) error = %v", sub, err)
		}
	}

	host["/proc/mounts"] = "/dev/nbd0p1 " + filepath.Join(base, "dstImg123") + " ext4 rw 0 0\n"
	host.apply(&in.env)

	want := row(diskSpaceInjectRow, doctorWarn, "minimega disk inject directories in "+base+", which an inject that fails "+
		"can leave, if no inject runs now: "+filepath.Join(base, "dstImg.txt")+", "+filepath.Join(base, "dstImg123")+
		" (mounted from /dev/nbd0p1), "+filepath.Join(base, "dstImg456"))

	if diff := cmp.Diff(want, injectDirsCheck(in, base)); diff != "" {
		t.Errorf("injectDirsCheck() mismatch (-want +got):\n%s", diff)
	}

	missing := filepath.Join(base, "missing")
	if diff := cmp.Diff(row(diskSpaceInjectRow, doctorSkip, "not checked, because "+missing+" does not exist"),
		injectDirsCheck(in, missing)); diff != "" {
		t.Errorf("injectDirsCheck() of a missing directory mismatch (-want +got):\n%s", diff)
	}
}

// The disk-space hint frees space, and also rotates the minimega log or limits its Docker log when the row names them.
func TestDoctorDiskSpaceHint(t *testing.T) {
	t.Parallel()

	free := "increase the free space: delete the images and files that you do not use, and the snapshot files and " +
		"directories that the leftovers rows name"

	tests := map[string]struct {
		check doctorCheck
		want  string
	}{
		"low": {check: row("disk-space base", doctorWarn, "x, "+lowSpacePhrase+" 5.0 GiB"), want: free},
		"log": {
			check: row("disk-space files", doctorWarn, "x. The minimega log /var/log/mm.log is 9.0 GiB, "+lowSpacePhrase),
			want:  free + ". Delete or rotate the minimega log, such as with logrotate and its copytruncate option",
		},
		"stdout": {
			check: row("disk-space files", doctorWarn, "x. minimega gives no -logfile, "+stdoutLogPhrase),
			want:  free + ". When minimega runs in Docker, limit its log with the json-file options max-size and max-file",
		},
		"inject dirs": {
			check: row(diskSpaceInjectRow, doctorWarn, "x"),
			want: "when no inject runs, unmount each mounted directory with umount <directory>, disconnect its nbd " +
				"device (see the nbd row), then remove the directory with rmdir <directory>",
		},
		"skip": {check: row("disk-space files", doctorSkip, "not checked"), want: ""},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := diskSpaceHint(tc.check); got != tc.want {
				t.Errorf("diskSpaceHint() = %q, want %q", got, tc.want)
			}
		})
	}
}
