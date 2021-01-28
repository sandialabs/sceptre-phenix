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

func TestNTPAppRouter(t *testing.T) {
	baseDir, err := ioutil.TempDir("", "ntp-app-test")
	if err != nil {
		t.Log(err)
		t.FailNow()
	}

	defer os.RemoveAll(baseDir)

	nodes := []*v1.Node{
		{
			TypeF: "Router",
			LabelsF: map[string]string{
				"ntp-server": "true",
			},
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
			LabelsF: map[string]string{
				"ntp-server": "true",
			},
			GeneralF: &v1.General{
				HostnameF: "win",
			},
			HardwareF: &v1.Hardware{
				OSTypeF: "windows",
			},
		},
	}

	// only first node w/ ntp-server tag should be configured
	expected := [][]ifaces.NodeInjection{
		{
			&v1.Injection{
				SrcF: fmt.Sprintf("%s/ntp/router_ntp", baseDir),
				DstF: "/opt/vyatta/etc/ntp.conf",
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

	app := GetApp("ntp")

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

func TestNTPAppLinux(t *testing.T) {
	baseDir, err := ioutil.TempDir("", "ntp-app-test")
	if err != nil {
		t.Log(err)
		t.FailNow()
	}

	defer os.RemoveAll(baseDir)

	nodes := []*v1.Node{
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
			LabelsF: map[string]string{
				"ntp-server": "true",
			},
			GeneralF: &v1.General{
				HostnameF: "win",
			},
			HardwareF: &v1.Hardware{
				OSTypeF: "windows",
			},
		},
		{
			TypeF: "Router",
			LabelsF: map[string]string{
				"ntp-server": "true",
			},
			GeneralF: &v1.General{
				HostnameF: "router",
			},
		},
	}

	// only first node w/ ntp-server tag should be configured
	expected := [][]ifaces.NodeInjection{
		{
			&v1.Injection{
				SrcF: fmt.Sprintf("%s/ntp/linux_ntp", baseDir),
				DstF: "/etc/ntp.conf",
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

	app := GetApp("ntp")

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

func TestNTPAppWindows(t *testing.T) {
	baseDir, err := ioutil.TempDir("", "ntp-app-test")
	if err != nil {
		t.Log(err)
		t.FailNow()
	}

	defer os.RemoveAll(baseDir)

	nodes := []*v1.Node{
		{
			TypeF: "VirtualMachine",
			LabelsF: map[string]string{
				"ntp-server": "true",
			},
			GeneralF: &v1.General{
				HostnameF: "win",
			},
			HardwareF: &v1.Hardware{
				OSTypeF: "windows",
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
			TypeF: "Router",
			LabelsF: map[string]string{
				"ntp-server": "true",
			},
			GeneralF: &v1.General{
				HostnameF: "router",
			},
		},
	}

	// only first node w/ ntp-server tag should be configured
	expected := [][]ifaces.NodeInjection{
		{
			&v1.Injection{
				SrcF: fmt.Sprintf("%s/ntp/win_ntp", baseDir),
				DstF: "ntp.ps1",
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

	app := GetApp("ntp")

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

func TestNTPAppNone(t *testing.T) {
	baseDir, err := ioutil.TempDir("", "ntp-app-test")
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

	// no ntp-server labels present
	expected := [][]ifaces.NodeInjection{nil, nil, nil}

	spec := &v1.ExperimentSpec{
		BaseDirF: baseDir,
		TopologyF: &v1.TopologySpec{
			NodesF: nodes,
		},
	}

	exp := &types.Experiment{Spec: spec}

	app := GetApp("ntp")

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
