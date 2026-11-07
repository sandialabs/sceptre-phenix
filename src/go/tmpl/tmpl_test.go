package tmpl_test

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/tmpl"
	v1 "phenix/types/version/v1"
)

const hostnameScript = "#!/bin/bash\n\nsed -i -e 's/phenix/host-01/g' /etc/hosts\nhostnamectl set-hostname host-01"

func TestGenerateFromTemplate(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	if err := tmpl.GenerateFromTemplate("linux_hostname.tmpl", "host-01", &out); err != nil {
		t.Fatalf("GenerateFromTemplate(linux_hostname.tmpl) = %v, want nil", err)
	}

	if got := out.String(); got != hostnameScript {
		t.Errorf("GenerateFromTemplate(linux_hostname.tmpl, host-01) = %q, want %q", got, hostnameScript)
	}
}

func TestGenerateFromTemplateFails(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		template string
		data     any
	}{
		{name: "missing template", template: "missing.tmpl", data: nil},
		{name: "data the template cannot use", template: "linux_interfaces.tmpl", data: 42},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if err := tmpl.GenerateFromTemplate(tt.template, tt.data, new(bytes.Buffer)); err == nil {
				t.Errorf("GenerateFromTemplate(%q, %v) = nil, want an error", tt.template, tt.data)
			}
		})
	}
}

// CreateFileFromTemplate makes the missing folders and replaces a file that is there.
func TestCreateFileFromTemplate(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "a", "b", "hostname.sh")

	for _, host := range []string{"long-host-name-01", "host-01"} {
		if err := tmpl.CreateFileFromTemplate("linux_hostname.tmpl", host, path); err != nil {
			t.Fatalf("CreateFileFromTemplate(linux_hostname.tmpl, %s, %q) = %v, want nil", host, path, err)
		}
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the created file: %v", err)
	}

	if string(got) != hostnameScript {
		t.Errorf("CreateFileFromTemplate() wrote %q, want %q", got, hostnameScript)
	}
}

func TestCreateFileFromTemplateFails(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatalf("writing a file: %v", err)
	}

	tests := []struct {
		name     string
		template string
		filename string
	}{
		{name: "a folder in the path is a file", template: "linux_hostname.tmpl", filename: filepath.Join(file, "out.sh")},
		{name: "the path is a folder", template: "linux_hostname.tmpl", filename: dir},
		{name: "missing template", template: "missing.tmpl", filename: filepath.Join(dir, "out.sh")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if err := tmpl.CreateFileFromTemplate(tt.template, "host-01", tt.filename); err == nil {
				t.Errorf("CreateFileFromTemplate(%q, host-01, %q) = nil, want an error", tt.template, tt.filename)
			}
		})
	}
}

// CreateFileFromTemplate keeps the old content of the file when the template fails, and leaves no temporary file.
func TestCreateFileFromTemplateKeepsOldFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		template string
		data     any
	}{
		{name: "missing template", template: "missing.tmpl", data: "host-01"},
		{name: "data the template cannot use", template: "linux_interfaces.tmpl", data: 42},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			path := filepath.Join(dir, "interfaces")

			if err := os.WriteFile(path, []byte("old content"), 0o600); err != nil {
				t.Fatalf("writing the old file: %v", err)
			}

			if err := tmpl.CreateFileFromTemplate(tt.template, tt.data, path); err == nil {
				t.Fatalf("CreateFileFromTemplate(%q, %v, %q) = nil, want an error", tt.template, tt.data, path)
			}

			if got, err := os.ReadFile(path); err != nil || string(got) != "old content" {
				t.Errorf("file after a failed CreateFileFromTemplate = %q, %v, want %q", got, err, "old content")
			}

			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatalf("os.ReadDir(%q) error = %v", dir, err)
			}

			names := make([]string, 0, len(entries))
			for _, e := range entries {
				names = append(names, e.Name())
			}

			if diff := cmp.Diff([]string{"interfaces"}, names); diff != "" {
				t.Errorf("folder after a failed CreateFileFromTemplate mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// CreateFileFromTemplate gives a new file mode 0600.
func TestCreateFileFromTemplateMode(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "hostname.sh")

	if err := tmpl.CreateFileFromTemplate("linux_hostname.tmpl", "host-01", path); err != nil {
		t.Fatalf("CreateFileFromTemplate(linux_hostname.tmpl, host-01, %q) = %v, want nil", path, err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("os.Stat(%q) error = %v", path, err)
	}

	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("CreateFileFromTemplate() file mode = %v, want %v", got, os.FileMode(0o600))
	}
}

// CreateFileFromTemplate writes a file with a name as long as the file system allows.
func TestCreateFileFromTemplateLongName(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), strings.Repeat("a", 250))

	if err := tmpl.CreateFileFromTemplate("linux_hostname.tmpl", "host-01", path); err != nil {
		t.Errorf("CreateFileFromTemplate(linux_hostname.tmpl, host-01, <250 characters>) = %v, want nil", err)
	}
}

// CreateFileFromTemplate fails in a folder that it cannot write, and keeps the file there.
func TestCreateFileFromTemplateReadOnlyFolder(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		t.Skip("root can write a read-only folder")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "hostname.sh")

	if err := os.WriteFile(path, []byte("old content"), 0o600); err != nil {
		t.Fatalf("writing the old file: %v", err)
	}

	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("os.Chmod(%q) error = %v", dir, err)
	}

	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if err := tmpl.CreateFileFromTemplate("linux_hostname.tmpl", "host-01", path); err == nil {
		t.Errorf("CreateFileFromTemplate(linux_hostname.tmpl, host-01, %q) = nil, want an error", path)
	}

	if got, err := os.ReadFile(path); err != nil || string(got) != "old content" {
		t.Errorf("file after a failed CreateFileFromTemplate = %q, %v, want %q", got, err, "old content")
	}
}

// lossyOverride is the minimega script line that turns on lossy VNC updates for a KVM VM.
const lossyOverride = `.preprocess false vm config qemu-override "-vnc unix:" "-vnc lossy=on,vnc=unix:"`

// scriptNode is a VM of the given type and OS for the script tests, with one interface.
func scriptNode(hostname, vmType, osType string) *v1.Node {
	snapshot := false

	return &v1.Node{
		TypeF:     "VirtualMachine",
		GeneralF:  &v1.General{HostnameF: hostname, VMTypeF: vmType, SnapshotF: &snapshot},
		HardwareF: &v1.Hardware{OSTypeF: osType, DrivesF: []*v1.Drive{{ImageF: "image.qc2"}}},
		NetworkF:  &v1.Network{InterfacesF: []*v1.Interface{{NameF: "eth0", VLANF: "EXP_1", BridgeF: "phenix"}}},
	}
}

// scriptBlocks splits a minimega script into the lines of each VM, from the VM's "## VM: <name> ##" header to the
// next "## " header.
func scriptBlocks(script string) map[string][]string {
	blocks := map[string][]string{}

	var vm string

	for line := range strings.SplitSeq(script, "\n") {
		line = strings.TrimSpace(line)

		if strings.HasPrefix(line, "## ") {
			name, ok := strings.CutPrefix(line, "## VM: ")
			vm = ""

			if ok {
				vm = strings.TrimSuffix(name, " ##")
			}

			continue
		}

		if vm != "" && line != "" {
			blocks[vm] = append(blocks[vm], line)
		}
	}

	return blocks
}

// The minimega script turns on lossy VNC updates once for each KVM VM that is not external, before the
// topology's overrides. A Linux VM gets the qxl video card through vm config vga, unless the topology sets vga.
func TestMinimegaScriptVNCSettings(t *testing.T) {
	t.Parallel()

	external := true

	linux := scriptNode("linux-vm", "kvm", "linux")
	linux.OverridesF = map[string]string{"-vnc unix:": "-vnc lossy=on,share=force-shared,vnc=unix:"}

	virtio := scriptNode("virtio-vm", "kvm", "linux")
	virtio.AdvancedF = map[string]string{"vga": "virtio"}

	container := scriptNode("container-vm", "container", "linux")

	externalVM := scriptNode("external-vm", "kvm", "linux")
	externalVM.ExternalF = &external

	spec := &v1.ExperimentSpec{
		ExperimentNameF: "vnc-exp",
		TopologyF: &v1.TopologySpec{NodesF: []*v1.Node{
			linux, virtio, scriptNode("windows-vm", "kvm", "windows"), container, externalVM,
		}},
	}

	var script bytes.Buffer

	if err := tmpl.GenerateFromTemplate("minimega_script.tmpl", spec, &script); err != nil {
		t.Fatalf("GenerateFromTemplate(minimega_script.tmpl) error = %v", err)
	}

	blocks := scriptBlocks(script.String())

	want := []string{
		"clear vm config",
		"vm config vcpus 0",
		"vm config cpu",
		"vm config memory 0",
		"vm config snapshot false",
		"vm config disk image.qc2",
		"vm config vga qxl",
		"vm config net phenix,EXP_1",
		lossyOverride,
		`.preprocess false vm config qemu-override "-vnc unix:" "-vnc lossy=on,share=force-shared,vnc=unix:"`,
		"vm launch kvm linux-vm",
	}

	if diff := cmp.Diff(want, blocks["linux-vm"]); diff != "" {
		t.Errorf("minimega script for linux-vm mismatch (-want +got):\n%s", diff)
	}

	tests := map[string]struct {
		lossy   bool
		vga     []string
		missing bool
	}{
		"virtio-vm":    {lossy: true, vga: []string{"vm config vga virtio"}},
		"windows-vm":   {lossy: true},
		"container-vm": {lossy: false, vga: []string{"vm config vga qxl"}},
		"external-vm":  {missing: true},
	}

	for vm, tc := range tests {
		lines, ok := blocks[vm]
		if ok == tc.missing {
			t.Errorf("minimega script has %s = %t, want %t:\n%s", vm, ok, !tc.missing, script.String())

			continue
		}

		if got := slices.Contains(lines, lossyOverride); got != tc.lossy {
			t.Errorf("minimega script for %s has the lossy override = %t, want %t", vm, got, tc.lossy)
		}

		var vga []string

		for _, line := range lines {
			if strings.HasPrefix(line, "vm config vga ") {
				vga = append(vga, line)
			}

			if strings.Contains(line, "-vga") {
				t.Errorf("minimega script for %s has %q, want no -vga", vm, line)
			}
		}

		if diff := cmp.Diff(tc.vga, vga); diff != "" {
			t.Errorf("minimega script for %s vga lines mismatch (-want +got):\n%s", vm, diff)
		}
	}
}

// The packet capture script gives each capture VM the qxl video card through vm config vga, and turns on lossy
// VNC updates for it.
func TestPacketCaptureScriptVNCSettings(t *testing.T) {
	t.Parallel()

	elastic := scriptNode("elastic", "kvm", "linux")
	elastic.LabelsF = map[string]string{"soh-elastic-server": "true"}

	monitor := scriptNode("monitor", "kvm", "linux")
	monitor.LabelsF = map[string]string{"soh-monitor-node": "true"}

	data := struct {
		Exp *v1.ExperimentSpec
		Mon map[string][]string
	}{
		Exp: &v1.ExperimentSpec{
			ExperimentNameF: "soh-exp",
			TopologyF:       &v1.TopologySpec{NodesF: []*v1.Node{elastic, monitor}},
		},
		Mon: map[string][]string{},
	}

	var script bytes.Buffer

	if err := tmpl.GenerateFromTemplate("packet_capture_script.tmpl", data, &script); err != nil {
		t.Fatalf("GenerateFromTemplate(packet_capture_script.tmpl) error = %v", err)
	}

	blocks := scriptBlocks(script.String())

	for _, vm := range []string{"elastic", "monitor"} {
		lines := blocks[vm]

		vga := slices.Index(lines, "vm config vga qxl")
		lossy := slices.Index(lines, lossyOverride)
		launch := slices.Index(lines, "vm launch kvm "+vm)

		if vga < 0 || lossy < 0 || launch < 0 || lossy > launch || vga > launch {
			t.Errorf("packet capture script for %s = %q, want vm config vga qxl and the lossy override before the launch",
				vm, lines)
		}

		for _, line := range lines {
			if strings.Contains(line, "-vga") {
				t.Errorf("packet capture script for %s has %q, want no -vga", vm, line)
			}
		}
	}
}
