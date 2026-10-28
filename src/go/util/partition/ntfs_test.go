package partition_test

import (
	"encoding/binary"
	"errors"
	"io"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

	"phenix/util/partition"
	pt "phenix/util/partition/partitiontest"
)

// countingReader counts the bytes read through it.
type countingReader struct {
	r    io.ReaderAt
	read int
}

func (c *countingReader) ReadAt(p []byte, off int64) (int, error) {
	c.read += len(p)

	return c.r.ReadAt(p, off)
}

// failingReader fails every read.
type failingReader struct{}

var errTestRead = errors.New("test read error")

func (failingReader) ReadAt([]byte, int64) (int, error) { return 0, errTestRead }

// ntfsVolume returns the bytes of v after change, which edits them.
func ntfsVolume(v pt.NTFS, change func(b []byte)) []byte {
	b := v.Bytes()
	if change != nil {
		change(b)
	}

	return b
}

// tester is the part of [testing.T] and [rapid.T] that the checks use.
type tester interface {
	Helper()
	Errorf(format string, args ...any)
}

// checkNTFS runs CheckNTFS on b, and fails t when it reads outside b or more than NTFSReadMax bytes.
func checkNTFS(t tester, b []byte) (partition.VolumeState, error) {
	t.Helper()

	disk := newDisk(int64(len(b))).write(0, b)
	r := &countingReader{r: disk}

	state, err := partition.CheckNTFS(r, disk.size)

	if len(disk.outside) > 0 {
		t.Errorf("CheckNTFS() read outside the volume: %v", disk.outside)
	}

	if r.read > partition.NTFSReadMax {
		t.Errorf("CheckNTFS() read %d bytes, more than %d", r.read, partition.NTFSReadMax)
	}

	return state, err
}

// CheckNTFS reads VOLUME_IS_DIRTY from $Volume, and from the newest restart page of $LogFile whether the log is
// clean: no client in use, or the clean flag. An emptied log and a chkdsk restart page are clean.
func TestCheckNTFS(t *testing.T) {
	t.Parallel()

	notClean := pt.Restart{LSN: 9, InUse: true}

	tests := map[string]struct {
		volume []byte
		want   partition.VolumeState
	}{
		"clean": {volume: pt.NTFS{}.Bytes()},
		"client in use with the clean flag": {
			volume: pt.NTFS{First: pt.Restart{LSN: 1, InUse: true, Clean: true}}.Bytes(),
		},
		"dirty": {
			volume: pt.NTFS{Dirty: true}.Bytes(),
			want:   partition.VolumeState{Dirty: true, Reason: partition.ReasonNTFSDirty},
		},
		"log not clean": {
			volume: pt.NTFS{First: notClean}.Bytes(),
			want:   partition.VolumeState{LogNotClean: true, Reason: partition.ReasonNTFSLog},
		},
		"dirty and log not clean": {
			volume: pt.NTFS{Dirty: true, First: notClean, Second: notClean}.Bytes(),
			want: partition.VolumeState{
				Dirty: true, LogNotClean: true, Reason: partition.ReasonNTFSDirty + ", and " + partition.ReasonNTFSLog,
			},
		},
		"newer second page not clean": {
			volume: pt.NTFS{First: pt.Restart{LSN: 1}, Second: notClean}.Bytes(),
			want:   partition.VolumeState{LogNotClean: true, Reason: partition.ReasonNTFSLog},
		},
		"older second page not clean": {
			volume: pt.NTFS{First: pt.Restart{LSN: 10}, Second: notClean}.Bytes(),
		},
		"bad first page, second not clean": {
			volume: ntfsVolume(pt.NTFS{Second: notClean}, func(b []byte) { copy(b[pt.NTFSLog:], "XXXX") }),
			want:   partition.VolumeState{LogNotClean: true, Reason: partition.ReasonNTFSLog},
		},
		"first page torn, second clean": {
			volume: ntfsVolume(pt.NTFS{First: notClean}, func(b []byte) { b[pt.NTFSLog+1022]++ }),
		},
		"emptied log": {volume: pt.NTFS{EmptyLog: true}.Bytes()},
		"chkdsk restart page": {
			volume: ntfsVolume(pt.NTFS{First: notClean, Second: pt.Restart{LSN: 0}}, func(b []byte) {
				copy(b[pt.NTFSLog:], "CHKD")
			}),
		},
		"4 KiB clusters": {
			volume: func() []byte {
				// the same volume with 8 sectors per cluster: move the MFT and the log to clusters 1 and 2
				b := make([]byte, 64<<10)
				v := pt.NTFS{Dirty: true}.Bytes()
				copy(b, v[:512])
				b[0x0D] = 8
				binary.LittleEndian.PutUint64(b[0x30:], 1)
				copy(b[4096:], v[pt.NTFSMFT:pt.NTFSMFT+4*pt.NTFSRecordSize])

				logFile := pt.FileRecord(pt.NonResident(0x80, pt.Run(2, 2)))
				copy(b[4096+2*pt.NTFSRecordSize:], logFile)
				copy(b[8192:], pt.RestartPage(notClean))
				copy(b[12288:], pt.RestartPage(pt.Restart{}))

				return b
			}(),
			want: partition.VolumeState{
				Dirty: true, LogNotClean: true, Reason: partition.ReasonNTFSDirty + ", and " + partition.ReasonNTFSLog,
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := checkNTFS(t, tc.volume)
			if err != nil {
				t.Fatalf("CheckNTFS() error = %v", err)
			}

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("CheckNTFS() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// ntfsBadVolumes are NTFS volumes that CheckNTFS rejects with an error, by what is wrong with them.
func ntfsBadVolumes() map[string][]byte {
	le := binary.LittleEndian
	clean := pt.NTFS{}
	logFile := func(attr []byte) []byte {
		return ntfsVolume(clean, func(b []byte) { copy(b[pt.NTFSLogFile:], pt.FileRecord(attr)) })
	}

	return map[string][]byte{
		"empty":                {},
		"cut short":            clean.Bytes()[:pt.NTFSLog+100],
		"no NTFS boot sector":  ntfsVolume(clean, func(b []byte) { copy(b[3:], "MSDOS5.0") }),
		"bad sector size":      ntfsVolume(clean, func(b []byte) { le.PutUint16(b[0x0B:], 100) }),
		"zero sector size":     ntfsVolume(clean, func(b []byte) { le.PutUint16(b[0x0B:], 0) }),
		"bad cluster size":     ntfsVolume(clean, func(b []byte) { b[0x0D] = 3 }),
		"zero cluster size":    ntfsVolume(clean, func(b []byte) { b[0x0D] = 0 }),
		"huge cluster":         ntfsVolume(clean, func(b []byte) { b[0x0D] = 0x81 }),
		"zero record size":     ntfsVolume(clean, func(b []byte) { b[0x40] = 0 }),
		"small record size":    ntfsVolume(clean, func(b []byte) { b[0x40] = 0xF8 }),
		"huge record size":     ntfsVolume(clean, func(b []byte) { b[0x40] = 0x80 }),
		"MFT past the end":     ntfsVolume(clean, func(b []byte) { le.PutUint64(b[0x30:], 1<<62) }),
		"MFT at cluster 0":     ntfsVolume(clean, func(b []byte) { le.PutUint64(b[0x30:], 0) }),
		"no FILE mark":         ntfsVolume(clean, func(b []byte) { copy(b[pt.NTFSVolume:], "BAAD") }),
		"fixup mismatch":       ntfsVolume(clean, func(b []byte) { b[pt.NTFSVolume+510]++ }),
		"bad fixup count":      ntfsVolume(clean, func(b []byte) { le.PutUint16(b[pt.NTFSVolume+6:], 2) }),
		"odd fixup offset":     ntfsVolume(clean, func(b []byte) { le.PutUint16(b[pt.NTFSVolume+4:], 0x31) }),
		"fixups past block 0":  ntfsVolume(clean, func(b []byte) { le.PutUint16(b[pt.NTFSVolume+4:], 0x1FC) }),
		"record not in use":    ntfsVolume(clean, func(b []byte) { b[pt.NTFSLogFile+0x16] = 0 }),
		"first attribute zero": ntfsVolume(clean, func(b []byte) { le.PutUint16(b[pt.NTFSVolume+0x14:], 0) }),
		"attribute too long": ntfsVolume(clean, func(b []byte) {
			le.PutUint32(b[pt.NTFSVolume+0x38+4:], 0x10000)
		}),
		"attribute too short": ntfsVolume(clean, func(b []byte) { le.PutUint32(b[pt.NTFSVolume+0x38+4:], 8) }),
		"no volume information": ntfsVolume(clean, func(b []byte) {
			copy(b[pt.NTFSVolume:], pt.FileRecord(pt.Resident(0x30, make([]byte, 16))))
		}),
		"named volume information": ntfsVolume(clean, func(b []byte) { b[pt.NTFSVolume+0x38+9] = 1 }),
		"non-resident volume information": ntfsVolume(clean, func(b []byte) {
			copy(b[pt.NTFSVolume:], pt.FileRecord(pt.NonResident(0x70, pt.Run(1, 1))))
		}),
		"short volume information": ntfsVolume(clean, func(b []byte) {
			copy(b[pt.NTFSVolume:], pt.FileRecord(pt.Resident(0x70, make([]byte, 4))))
		}),
		"volume information past its attribute": ntfsVolume(clean, func(b []byte) {
			le.PutUint32(b[pt.NTFSVolume+0x38+0x10:], 0x1000)
		}),
		"resident log data":   logFile(pt.Resident(0x80, make([]byte, 16))),
		"no log data":         logFile(pt.Resident(0x10, make([]byte, 16))),
		"sparse first run":    logFile(pt.NonResident(0x80, []byte{0x02, 0x10, 0x00})),
		"no data runs":        logFile(pt.NonResident(0x80, nil)),
		"zero-length run":     logFile(pt.NonResident(0x80, pt.Run(0, 16))),
		"negative run":        logFile(pt.NonResident(0x80, []byte{0x12, 0x10, 0x00, 0xF0})),
		"run past the end":    logFile(pt.NonResident(0x80, pt.Run(16, 0x7000))),
		"run header too long": logFile(pt.NonResident(0x80, []byte{0x99})),
		"run cut short": ntfsVolume(clean, func(b []byte) {
			rec := pt.FileRecord(pt.NonResident(0x80, pt.Run(16, 16)))
			le.PutUint16(rec[0x38+0x20:], 0x47)
			rec[0x38+0x47] = 0x22
			copy(b[pt.NTFSLogFile:], rec)
		}),
		"data not from cluster 0": ntfsVolume(clean, func(b []byte) {
			rec := pt.FileRecord(pt.NonResident(0x80, pt.Run(16, 16)))
			rec[0x38+0x10] = 1
			copy(b[pt.NTFSLogFile:], rec)
		}),
		"no restart page": ntfsVolume(clean, func(b []byte) {
			copy(b[pt.NTFSLog:], "XXXX")
			copy(b[pt.NTFSLog+pt.NTFSPageSize:], "XXXX")
		}),
		"restart pages torn": ntfsVolume(clean, func(b []byte) {
			b[pt.NTFSLog+510]++
			b[pt.NTFSLog+pt.NTFSPageSize+510]++
		}),
		"bad page sizes": ntfsVolume(clean, func(b []byte) {
			le.PutUint32(b[pt.NTFSLog+0x10:], 3000)
			le.PutUint32(b[pt.NTFSLog+pt.NTFSPageSize+0x10:], 1<<20)
		}),
		"bad restart areas": ntfsVolume(clean, func(b []byte) {
			le.PutUint16(b[pt.NTFSLog+0x18:], 0x31)
			le.PutUint16(b[pt.NTFSLog+pt.NTFSPageSize+0x18:], 0xFFF8)
		}),
	}
}

// CheckNTFS returns an error, and never panics or reads outside the volume, for a volume that is cut short or has a
// bad boot sector, MFT record, attribute, data run, fixup or restart page.
func TestCheckNTFSBadVolumes(t *testing.T) {
	t.Parallel()

	for name, volume := range ntfsBadVolumes() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := checkNTFS(t, volume)
			if err == nil {
				t.Fatalf("CheckNTFS() = %+v, want an error", got)
			}

			t.Logf("CheckNTFS() error = %v", err)

			if diff := cmp.Diff(partition.VolumeState{}, got); diff != "" {
				t.Errorf("CheckNTFS() state with an error mismatch (-want +got):\n%s", diff)
			}
		})
	}

	t.Run("read error", func(t *testing.T) {
		t.Parallel()

		if _, err := partition.CheckNTFS(failingReader{}, pt.NTFSSize); !errors.Is(err, errTestRead) {
			t.Errorf("CheckNTFS() error = %v, want %v", err, errTestRead)
		}
	})
}

// CheckExt reports the error state of an ext file system, not other states, and returns an error for a partition
// with no ext superblock.
func TestCheckExt(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		data    []byte
		want    partition.VolumeState
		wantErr bool
	}{
		"clean":                 {data: pt.Ext(1)},
		"not cleanly unmounted": {data: pt.Ext(0)},
		"errors":                {data: pt.Ext(3), want: partition.VolumeState{HasErrors: true, Reason: partition.ReasonExtHasErrors}},
		"orphans":               {data: pt.Ext(4)},
		"no superblock":         {data: make([]byte, 2048), wantErr: true},
		"cut short":             {data: pt.Ext(3)[:1024+0x3A], wantErr: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			disk := newDisk(int64(len(tc.data))).write(0, tc.data)

			got, err := partition.CheckExt(disk, disk.size)
			if (err != nil) != tc.wantErr {
				t.Fatalf("CheckExt() error = %v, want an error %t", err, tc.wantErr)
			}

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("CheckExt() mismatch (-want +got):\n%s", diff)
			}

			if len(disk.outside) > 0 {
				t.Errorf("CheckExt() read outside the partition: %v", disk.outside)
			}
		})
	}
}

// genNTFSVolume draws an NTFS volume with any states, and often a few bytes changed at random.
func genNTFSVolume() *rapid.Generator[[]byte] {
	return rapid.Custom(func(t *rapid.T) []byte {
		restart := func(label string) pt.Restart {
			return pt.Restart{
				LSN:   rapid.Uint64Range(0, 4).Draw(t, label+" lsn"),
				InUse: rapid.Bool().Draw(t, label+" in use"),
				Clean: rapid.Bool().Draw(t, label+" clean"),
			}
		}

		b := pt.NTFS{
			Dirty: rapid.Bool().Draw(t, "dirty"), First: restart("first"), Second: restart("second"),
			EmptyLog: rapid.IntRange(0, 9).Draw(t, "empty") == 0,
		}.Bytes()

		for range rapid.IntRange(0, 4).Draw(t, "changes") {
			b[rapid.IntRange(0, len(b)-1).Draw(t, "at")] = rapid.Byte().Draw(t, "byte")
		}

		return b[:rapid.IntRange(0, len(b)).Draw(t, "size")]
	})
}

// For any volume, CheckNTFS stays inside the volume and NTFSReadMax bytes, returns a zero state with an error, and
// gives a reason exactly when the volume is dirty or its log is not clean. A volume that it reads twice gives the
// same result.
func TestCheckNTFSProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(rt *rapid.T) {
		volume := genNTFSVolume().Draw(rt, "volume")
		checkNTFSInvariants(rt, volume)
	})
}

// checkNTFSInvariants checks the invariants of TestCheckNTFSProperties for volume.
func checkNTFSInvariants(t tester, volume []byte) {
	t.Helper()

	state, err := checkNTFS(t, slices.Clone(volume))
	if err != nil && state != (partition.VolumeState{}) {
		t.Errorf("CheckNTFS() = %+v with the error %v, want a zero state", state, err)
	}

	if (state.Reason != "") != (state.Dirty || state.LogNotClean) || state.HasErrors {
		t.Errorf("CheckNTFS() = %+v, want a reason only for a dirty volume or a log that is not clean", state)
	}

	again, errAgain := checkNTFS(t, slices.Clone(volume))
	if again != state || (errAgain == nil) != (err == nil) {
		t.Errorf("CheckNTFS() again = %+v, %v; want %+v, %v", again, errAgain, state, err)
	}
}
