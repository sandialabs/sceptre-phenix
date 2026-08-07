package app

import (
	"context"
	"strings"
	"testing"

	"phenix/store"
	"phenix/types"
	v1 "phenix/types/version/v1"
	v2 "phenix/types/version/v2"
)

func TestSerialPreStartSchedulesVMToExternalHost(t *testing.T) {
	vm := serialTestVM("vm1")
	external := serialTestExternal("plc-serial0", "compute1", "/dev/ttyUSB0", 0)
	exp := serialTestExperiment(t, []*v1.Node{vm, external}, nil, nil, []map[string]any{
		{"src": "vm1", "dst": "plc-serial0"},
	})

	if err := (Serial{}).PreStart(context.Background(), exp); err != nil {
		t.Fatalf("unexpected PreStart error: %v", err)
	}

	if got := exp.Spec.Schedules()["vm1"]; got != "compute1" {
		t.Fatalf("expected vm1 to be scheduled on compute1, got %q", got)
	}

	qemuAppend := vm.Advanced()["qemu-append"]
	if !strings.Contains(qemuAppend, "serial-exp_serial_vm1_0") {
		t.Fatalf("expected qemu-append to include VM serial socket, got %q", qemuAppend)
	}

	if external.Advanced()["qemu-append"] != "" {
		t.Fatalf("expected external node not to get qemu-append, got %q", external.Advanced()["qemu-append"])
	}
}

func TestSerialPreStartConflictingScheduleFails(t *testing.T) {
	exp := serialTestExperiment(t, []*v1.Node{
		serialTestVM("vm1"),
		serialTestExternal("plc-serial0", "compute1", "/dev/ttyUSB0", 0),
	}, map[string]string{"vm1": "compute2"}, nil, []map[string]any{
		{"src": "vm1", "dst": "plc-serial0"},
	})

	err := (Serial{}).PreStart(context.Background(), exp)
	if err == nil {
		t.Fatal("expected PreStart to fail")
	}

	if !strings.Contains(err.Error(), "scheduled to host \"compute2\"") {
		t.Fatalf("expected schedule conflict error, got %v", err)
	}
}

func TestSerialPreStartVMExternalDifferentHostsFails(t *testing.T) {
	exp := serialTestExperiment(t, []*v1.Node{
		serialTestVM("vm1"),
		serialTestExternal("plc-serial0", "compute1", "/dev/ttyUSB0", 0),
		serialTestExternal("plc-serial1", "compute2", "/dev/ttyUSB1", 0),
	}, nil, nil, []map[string]any{
		{"src": "vm1", "dst": "plc-serial0"},
		{"src": "vm1", "dst": "plc-serial1"},
	})

	err := (Serial{}).PreStart(context.Background(), exp)
	if err == nil {
		t.Fatal("expected PreStart to fail")
	}

	if !strings.Contains(err.Error(), "different hosts") {
		t.Fatalf("expected multiple external host error, got %v", err)
	}
}

func TestSerialPreStartSerialDeviceAnnotationOnVMFails(t *testing.T) {
	vm := serialTestVM("vm1")
	vm.AnnotationsF = map[string]any{
		serialDeviceAnnotation: map[string]any{
			"host":   "compute1",
			"device": "/dev/ttyUSB0",
		},
	}

	exp := serialTestExperiment(t, []*v1.Node{vm}, nil, nil, nil)

	err := (Serial{}).PreStart(context.Background(), exp)
	if err == nil {
		t.Fatal("expected PreStart to fail")
	}

	if !strings.Contains(err.Error(), "only valid on external nodes") {
		t.Fatalf("expected external-only annotation error, got %v", err)
	}
}

func TestSerialPostStartVMToExternalCommandIsLocalOnly(t *testing.T) {
	exp := serialTestExperiment(t, []*v1.Node{
		serialTestVM("vm1"),
		serialTestExternal("plc-serial0", "compute1", "/dev/ttyUSB0", 115200),
	}, nil, map[string]string{"vm1": "compute1"}, []map[string]any{
		{"src": "vm1", "dst": "plc-serial0"},
	})

	commands, err := serialPostStartCommands(exp, SerialConnectionConfig{Src: "vm1", Dst: "plc-serial0"}, 0)
	if err != nil {
		t.Fatalf("unexpected command build error: %v", err)
	}

	if len(commands) != 1 {
		t.Fatalf("expected one same-host socat command, got %d", len(commands))
	}

	want := "socat -lf/tmp/serial-exp_serial_vm1_plc-serial0_0.log -d -d -d -d " +
		"UNIX-CONNECT:/tmp/serial-exp_serial_vm1_0 OPEN:/dev/ttyUSB0,raw,echo=0,b115200"
	if commands[0].host != "compute1" || commands[0].command != want {
		t.Fatalf("unexpected command:\nhost: %s\ncommand: %s", commands[0].host, commands[0].command)
	}

	if strings.Contains(commands[0].command, "TCP-") {
		t.Fatalf("expected local-only command without TCP bridge, got %q", commands[0].command)
	}
}

func TestSerialVMToVMSocatCommandsUnchanged(t *testing.T) {
	sameHost := vmToVMSerialSocatCommands(
		"serial-exp",
		SerialConnectionConfig{Src: "vm1", Dst: "vm2"},
		0,
		map[string]string{"vm1": "compute1", "vm2": "compute1"},
	)

	if len(sameHost) != 1 {
		t.Fatalf("expected one same-host command, got %d", len(sameHost))
	}

	wantSameHost := "socat -lf/tmp/serial-exp_serial_vm1_vm2_0.log -d -d -d -d " +
		"UNIX-CONNECT:/tmp/serial-exp_serial_vm1_0 UNIX-CONNECT:/tmp/serial-exp_serial_vm2_0"
	if sameHost[0].host != "compute1" || sameHost[0].command != wantSameHost {
		t.Fatalf("unexpected same-host command:\nhost: %s\ncommand: %s", sameHost[0].host, sameHost[0].command)
	}

	crossHost := vmToVMSerialSocatCommands(
		"serial-exp",
		SerialConnectionConfig{Src: "vm1", Dst: "vm2"},
		1,
		map[string]string{"vm1": "compute1", "vm2": "compute2"},
	)

	if len(crossHost) != 2 {
		t.Fatalf("expected two cross-host commands, got %d", len(crossHost))
	}

	wantListen := "socat -lf/tmp/serial-exp_serial_vm1_vm2_1.log -d -d -d -d " +
		"UNIX-CONNECT:/tmp/serial-exp_serial_vm1_1 TCP-LISTEN:40501"
	wantConnect := "socat -lf/tmp/serial-exp_serial_vm1_vm2_1.log -d -d -d -d " +
		"UNIX-CONNECT:/tmp/serial-exp_serial_vm2_1 TCP-CONNECT:compute1:40501"

	if crossHost[0].host != "compute1" || crossHost[0].command != wantListen {
		t.Fatalf("unexpected listen command:\nhost: %s\ncommand: %s", crossHost[0].host, crossHost[0].command)
	}

	if crossHost[1].host != "compute2" || crossHost[1].command != wantConnect {
		t.Fatalf("unexpected connect command:\nhost: %s\ncommand: %s", crossHost[1].host, crossHost[1].command)
	}
}

func TestSerialExternalAnnotationValidation(t *testing.T) {
	tests := []struct {
		name string
		node *v1.Node
		want string
	}{
		{
			name: "missing annotation",
			node: serialTestExternalWithoutAnnotation("plc-serial0"),
			want: "missing \"phenix/serial-device\" annotation",
		},
		{
			name: "invalid annotation shape",
			node: serialTestExternalWithAnnotation("plc-serial0", "not-a-map"),
			want: "decoding",
		},
		{
			name: "missing host",
			node: serialTestExternal("plc-serial0", "", "/dev/ttyUSB0", 0),
			want: "host is required",
		},
		{
			name: "invalid device path",
			node: serialTestExternal("plc-serial0", "compute1", "ttyUSB0", 0),
			want: "absolute /dev path",
		},
		{
			name: "invalid device characters",
			node: serialTestExternal("plc-serial0", "compute1", "/dev/ttyUSB0,b9600", 0),
			want: "unsupported characters",
		},
		{
			name: "invalid baud rate",
			node: serialTestExternal("plc-serial0", "compute1", "/dev/ttyUSB0", 12345),
			want: "not supported",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseExternalSerialConfig(tt.node)
			if err == nil {
				t.Fatal("expected validation error")
			}

			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestSerialPreStartExternalToExternalFails(t *testing.T) {
	exp := serialTestExperiment(t, []*v1.Node{
		serialTestExternal("plc-serial0", "compute1", "/dev/ttyUSB0", 0),
		serialTestExternal("plc-serial1", "compute1", "/dev/ttyUSB1", 0),
	}, nil, nil, []map[string]any{
		{"src": "plc-serial0", "dst": "plc-serial1"},
	})

	err := (Serial{}).PreStart(context.Background(), exp)
	if err == nil {
		t.Fatal("expected PreStart to fail")
	}

	if !strings.Contains(err.Error(), "external-to-external serial links are not supported") {
		t.Fatalf("expected external-to-external error, got %v", err)
	}
}

func serialTestExperiment(t *testing.T, nodes []*v1.Node, specSchedules map[string]string, statusSchedules map[string]string, connections []map[string]any) *types.Experiment {
	t.Helper()

	return &types.Experiment{
		Metadata: store.ConfigMetadata{Name: "serial-exp"},
		Spec: &v1.ExperimentSpec{
			ExperimentNameF: "serial-exp",
			BaseDirF:        t.TempDir(),
			TopologyF:       &v1.TopologySpec{NodesF: nodes},
			ScenarioF: &v2.ScenarioSpec{AppsF: []*v2.ScenarioApp{
				{
					NameF: appNameSerial,
					MetadataF: map[string]any{
						"connections": connections,
					},
				},
			}},
			SchedulesF: specSchedules,
		},
		Status: &v1.ExperimentStatus{
			SchedulesF: statusSchedules,
		},
	}
}

func serialTestVM(name string) *v1.Node {
	return &v1.Node{
		TypeF: "VirtualMachine",
		GeneralF: &v1.General{
			HostnameF: name,
		},
		HardwareF: &v1.Hardware{
			OSTypeF: osLinux,
		},
		NetworkF: &v1.Network{},
	}
}

func serialTestExternal(name, host, device string, baudRate int) *v1.Node {
	annotation := map[string]any{
		"host":   host,
		"device": device,
	}
	if baudRate != 0 {
		annotation["baud_rate"] = baudRate
	}

	return serialTestExternalWithAnnotation(name, annotation)
}

func serialTestExternalWithoutAnnotation(name string) *v1.Node {
	external := true

	return &v1.Node{
		TypeF:     "External",
		GeneralF:  &v1.General{HostnameF: name},
		ExternalF: &external,
	}
}

func serialTestExternalWithAnnotation(name string, annotation any) *v1.Node {
	node := serialTestExternalWithoutAnnotation(name)
	node.AnnotationsF = map[string]any{serialDeviceAnnotation: annotation}

	return node
}
