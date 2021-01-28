package app

import (
	"fmt"
	"io/ioutil"
	"os"
	"testing"

	"phenix/types"
	ifaces "phenix/types/interfaces"
	v1 "phenix/types/version/v1"
)

func TestSerialApp(t *testing.T) {
	baseDir, err := ioutil.TempDir("", "serial-app-test")
	if err != nil {
		t.Log(err)
		t.FailNow()
	}

	defer os.RemoveAll(baseDir)

	// minimal spec for testing serial app
	nodes := []*v1.Node{
		{
			GeneralF: &v1.General{
				HostnameF: "linux-serial-node",
			},
			HardwareF: &v1.Hardware{
				OSTypeF: "linux",
			},
			NetworkF: &v1.Network{
				InterfacesF: []*v1.Interface{
					{
						TypeF: "serial",
					},
				},
			},
		},
		{
			GeneralF: &v1.General{
				HostnameF: "linux-node",
			},
			HardwareF: &v1.Hardware{
				OSTypeF: "linux",
			},
			NetworkF: &v1.Network{
				InterfacesF: []*v1.Interface{
					{
						TypeF: "ethernet",
					},
				},
			},
		},
		{
			GeneralF: &v1.General{
				HostnameF: "windows-serial-node",
			},
			HardwareF: &v1.Hardware{
				OSTypeF: "windows",
			},
			NetworkF: &v1.Network{
				InterfacesF: []*v1.Interface{
					{
						TypeF: "serial",
					},
				},
			},
		},
	}

	// first slice of 2D slice represents topology node
	expected := [][]ifaces.NodeInjection{
		{
			&v1.Injection{
				SrcF: fmt.Sprintf("%s/startup/linux-serial-node-serial.bash", baseDir),
				DstF: "/etc/phenix/serial-startup.bash",
			},
			&v1.Injection{
				SrcF: baseDir + "/startup/serial-startup.service",
				DstF: "/etc/systemd/system/serial-startup.service",
			},
			&v1.Injection{
				SrcF: baseDir + "/startup/symlinks/serial-startup.service",
				DstF: "/etc/systemd/system/multi-user.target.wants/serial-startup.service",
			},
		},
		nil,
		nil,
	}

	spec := &v1.ExperimentSpec{
		BaseDirF: baseDir,
		TopologyF: &v1.TopologySpec{
			NodesF: nodes,
		},
	}

	exp := &types.Experiment{Spec: spec}

	app := GetApp("serial")

	if err := app.Configure(exp); err != nil {
		t.Log(err)
		t.FailNow()
	}

	checkConfigureExpected(t, spec.Topology().Nodes(), expected)

	if err := app.PreStart(exp); err != nil {
		t.Log(err)
		t.FailNow()
	}

	checkStartExpected(t, spec.Topology().Nodes(), expected)
}
