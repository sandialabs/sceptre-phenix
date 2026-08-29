package image_test

import (
	"slices"
	"testing"

	"phenix/api/image"
	v1 "phenix/types/version/v1"
)

const defaultUbuntuMirror = "http://us.archive.ubuntu.com/ubuntu"

func TestSetupImage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		img            v1.Image
		wantErr        bool
		wantMirror     string
		wantComponents []string
		wantHasPkgs    []string
		wantNoPkgs     []string
		wantScripts    []string
		wantNoScripts  []string
	}{
		{
			name: "ubuntu noble keeps default mirror and uses ubuntu components",
			img: v1.Image{
				Variant: "minbase",
				Release: "noble",
				Mirror:  defaultUbuntuMirror,
			},
			wantMirror:     defaultUbuntuMirror,
			wantComponents: image.UbuntuComponents,
			wantHasPkgs:    []string{"linux-image-generic", "curl"},
			wantNoPkgs:     []string{"linux-image-amd64"},
		},
		{
			name: "ubuntu resolute falls through to ubuntu defaults",
			img: v1.Image{
				Variant: "minbase",
				Release: "resolute",
				Mirror:  defaultUbuntuMirror,
			},
			wantMirror:     defaultUbuntuMirror,
			wantComponents: image.UbuntuComponents,
			wantHasPkgs:    []string{"linux-image-generic"},
		},
		{
			name: "debian trixie swaps mirror and uses debian components",
			img: v1.Image{
				Variant: "minbase",
				Release: "trixie",
				Mirror:  defaultUbuntuMirror,
			},
			wantMirror:     "http://ftp.us.debian.org/debian",
			wantComponents: image.DebianComponents,
			wantHasPkgs:    []string{"linux-image-amd64"},
			wantNoPkgs:     []string{"linux-image-generic"},
		},
		{
			name: "kali rolling swaps mirror and uses kali components",
			img: v1.Image{
				Variant: "minbase",
				Release: "kali-rolling",
				Mirror:  defaultUbuntuMirror,
			},
			wantMirror:     "http://http.kali.org/kali",
			wantComponents: image.KaliComponents,
			wantHasPkgs:    []string{"linux-image-amd64", "default-jdk"},
		},
		{
			name: "user-provided mirror is preserved for debian release",
			img: v1.Image{
				Variant: "minbase",
				Release: "trixie",
				Mirror:  "http://my.local.mirror/debian",
			},
			wantMirror:     "http://my.local.mirror/debian",
			wantComponents: image.DebianComponents,
		},
		{
			name: "user-provided components are preserved",
			img: v1.Image{
				Variant:    "minbase",
				Release:    "noble",
				Mirror:     defaultUbuntuMirror,
				Components: []string{"main", "universe"},
			},
			wantComponents: []string{"main", "universe"},
		},
		{
			name: "mingui ubuntu adds gui packages and postbuild gui script",
			img: v1.Image{
				Variant: "mingui",
				Release: "noble",
				Mirror:  defaultUbuntuMirror,
			},
			wantHasPkgs: []string{"xubuntu-desktop", "xdotool", "linux-image-generic"},
			wantScripts: []string{"POSTBUILD_GUI"},
		},
		{
			name: "mingui kali uses kali gui script",
			img: v1.Image{
				Variant: "mingui",
				Release: "kali-rolling",
				Mirror:  defaultUbuntuMirror,
			},
			wantHasPkgs:   []string{"kali-desktop-xfce"},
			wantScripts:   []string{"POSTBUILD_KALI_GUI"},
			wantNoScripts: []string{"POSTBUILD_GUI"},
		},
		{
			name: "skip-default-packages omits default package set",
			img: v1.Image{
				Variant:             "minbase",
				Release:             "noble",
				Mirror:              defaultUbuntuMirror,
				SkipDefaultPackages: true,
			},
			wantHasPkgs: []string{"linux-image-generic"},
			wantNoPkgs:  []string{"curl", "vim", "openssh-server"},
		},
		{
			name: "invalid variant returns error",
			img: v1.Image{
				Variant: "bogus",
				Release: "noble",
				Mirror:  defaultUbuntuMirror,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			img := tt.img
			err := image.SetupImage(&img)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantMirror != "" && img.Mirror != tt.wantMirror {
				t.Errorf("mirror: got %q, want %q", img.Mirror, tt.wantMirror)
			}

			if tt.wantComponents != nil && !slices.Equal(img.Components, tt.wantComponents) {
				t.Errorf("components: got %v, want %v", img.Components, tt.wantComponents)
			}

			for _, p := range tt.wantHasPkgs {
				if !slices.Contains(img.Packages, p) {
					t.Errorf("expected package %q in %v", p, img.Packages)
				}
			}

			for _, p := range tt.wantNoPkgs {
				if slices.Contains(img.Packages, p) {
					t.Errorf("did not expect package %q in %v", p, img.Packages)
				}
			}

			for _, s := range tt.wantScripts {
				if _, ok := img.Scripts[s]; !ok {
					t.Errorf("expected script %q in %v", s, keys(img.Scripts))
				}
			}

			for _, s := range tt.wantNoScripts {
				if _, ok := img.Scripts[s]; ok {
					t.Errorf("did not expect script %q in %v", s, keys(img.Scripts))
				}
			}
		})
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
