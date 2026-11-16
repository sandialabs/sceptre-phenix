package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// experiment-files passes when <base-dir.phenix>/images is minimega's -filepath, by path or as one directory, warns
// with both paths when it is not, and skips when minimega or its -filepath is not known.
func TestExperimentFilesCheck(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		base     func(dir string) string // base-dir.phenix; fakeDoctorInput's when nil
		args     func(dir string) map[string]string
		up       bool
		noFound  bool
		cwd      func(dir string) string
		want     func(dir string) doctorCheck
		wantHint bool
	}{
		"same path": {
			args: func(dir string) map[string]string {
				return map[string]string{"filepath": filepath.Join(dir, "phenix") + "/./images/"}
			},
			up: true,
			want: func(dir string) doctorCheck {
				return passCheck(filepath.Join(dir, "phenix", "images") + " is minimega's -filepath")
			},
		},
		"same directory through a symlink": {
			args: doctorMinimegaArgs,
			up:   true,
			want: func(dir string) doctorCheck {
				return passCheck(filepath.Join(dir, "phenix", "images") + " is the directory of minimega's -filepath " +
					filepath.Join(dir, "files"))
			},
		},
		"relative base-dir.phenix": {
			base: func(string) string { return "phenix" },
			args: doctorMinimegaArgs,
			up:   true,
			cwd:  func(dir string) string { return dir },
			want: func(dir string) doctorCheck {
				return passCheck(filepath.Join(dir, "phenix", "images") + " is the directory of minimega's -filepath " +
					filepath.Join(dir, "files"))
			},
		},
		"other directory": {
			base: func(dir string) string { return filepath.Join(dir, "other") },
			args: doctorMinimegaArgs,
			up:   true,
			want: func(dir string) doctorCheck {
				images, files := filepath.Join(dir, "other", "images"), filepath.Join(dir, "files")

				return warnCheck("phenix keeps experiment files in " + images + " (<base-dir.phenix>/images), " +
					"but minimega's -filepath is " + files + ". The Files tab lists files from " + files +
					", so it does not show the files that phenix writes, such as VM screenshots")
			},
			wantHint: true,
		},
		"other directory that exists": {
			base: func(dir string) string { return filepath.Join(dir, "minimega") },
			args: doctorMinimegaArgs,
			up:   true,
			want: func(dir string) doctorCheck {
				images, files := filepath.Join(dir, "minimega", "images"), filepath.Join(dir, "files")

				return warnCheck("phenix keeps experiment files in " + images + " (<base-dir.phenix>/images), " +
					"but minimega's -filepath is " + files + ". The Files tab lists files from " + files +
					", so it does not show the files that phenix writes, such as VM screenshots")
			},
			wantHint: true,
		},
		"minimega down": {
			args: doctorMinimegaArgs,
			want: func(string) doctorCheck { return hostSkip("minimega gave no version") },
		},
		"no run findings": {
			noFound: true,
			want:    func(string) doctorCheck { return hostSkip("minimega gave no version") },
		},
		"no args": {
			up:   true,
			want: func(string) doctorCheck { return hostSkip("minimega-env did not read minimega's flags") },
		},
		"args without a filepath": {
			args: func(dir string) map[string]string { return map[string]string{"base": filepath.Join(dir, "minimega")} },
			up:   true,
			want: func(string) doctorCheck {
				return hostSkip("minimega gives no absolute -filepath (see minimega-env)")
			},
		},
		"relative filepath": {
			args: func(string) map[string]string { return map[string]string{"filepath": "files"} },
			up:   true,
			want: func(string) doctorCheck {
				return hostSkip("minimega gives no absolute -filepath (see minimega-env)")
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := newDoctorTestDir(t)

			if err := os.MkdirAll(filepath.Join(dir, "minimega", "images"), 0o750); err != nil {
				t.Fatalf("os.MkdirAll(minimega/images) error = %v", err)
			}

			var settings map[string]string
			if tc.base != nil {
				settings = map[string]string{"base-dir.phenix": tc.base(dir)}
			}

			in := fakeDoctorInput(dir, settings)
			if tc.cwd != nil {
				in.cwd = tc.cwd(dir)
			}

			if !tc.noFound {
				in.found = &doctorFound{minimegaUp: tc.up}
				if tc.args != nil {
					in.found.minimegaArgs = tc.args(dir)
				}
			}

			got := experimentFilesCheck(in)
			if diff := cmp.Diff(tc.want(dir), got); diff != "" {
				t.Errorf("experimentFilesCheck() mismatch (-want +got):\n%s", diff)
			}

			const hint = "set minimega's -filepath to <base-dir.phenix>/images, " +
				"or set base-dir.phenix so that <base-dir.phenix>/images is minimega's -filepath"

			wantHint := ""
			if tc.wantHint {
				wantHint = hint
			}

			if got := experimentFilesHint(got); got != wantHint {
				t.Errorf("experimentFilesHint() = %q, want %q", got, wantHint)
			}
		})
	}
}
