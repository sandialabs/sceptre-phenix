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

func TestVrouterApp(t *testing.T) {
	baseDir, err := ioutil.TempDir("", "vrouter-app-test")
	if err != nil {
		t.Log(err)
		t.FailNow()
	}

	defer os.RemoveAll(baseDir)

	nodes := []*v1.Node{
		{
			TypeF: "Router",
			GeneralF: &v1.General{
				HostnameF: "router",
			},
		},
		{
			TypeF: "VirtualMachine",
			LabelsF: map[string]string{
				"ntp-server": "true",
			},
			GeneralF: &v1.General{
				HostnameF: "linux",
			},
			HardwareF: &v1.Hardware{
				OSTypeF: "linux",
			},
		},
		{
			TypeF: "VirtualMachine",
			GeneralF: &v1.General{
				HostnameF: "win",
			},
			HardwareF: &v1.Hardware{
				OSTypeF: "windows",
			},
		},
	}

	expected := [][]ifaces.NodeInjection{
		{
			&v1.Injection{
				SrcF: fmt.Sprintf("%s/vrouter/router.boot", baseDir),
				DstF: "/opt/vyatta/etc/config/config.boot",
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

	app := GetApp("vrouter")

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
