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

func TestStartupApp(t *testing.T) {
	baseDir, err := ioutil.TempDir("", "startup-app-test")
	if err != nil {
		t.Log(err)
		t.FailNow()
	}

	defer os.RemoveAll(baseDir)

	nodes := []*v1.Node{
		{
			TypeF: "Router",
			HardwareF: &v1.Hardware{
				OSTypeF: "linux",
				DrivesF: []*v1.Drive{
					{
						ImageF: "foobar",
					},
				},
			},
		},
		{
			TypeF: "VirtualMachine",
			GeneralF: &v1.General{
				HostnameF: "centos-linux",
			},
			HardwareF: &v1.Hardware{
				OSTypeF: "centos",
				DrivesF: []*v1.Drive{
					{
						ImageF: "foobar",
					},
				},
			},
			NetworkF: &v1.Network{
				InterfacesF: []*v1.Interface{
					{}, // empty interface for testing
					{}, // empty interface for testing
				},
			},
		},
		{
			TypeF: "VirtualMachine",
			GeneralF: &v1.General{
				HostnameF: "rhel-linux",
			},
			HardwareF: &v1.Hardware{
				OSTypeF: "rhel",
				DrivesF: []*v1.Drive{
					{
						ImageF: "foobar",
					},
				},
			},
			NetworkF: &v1.Network{
				InterfacesF: []*v1.Interface{
					{}, // empty interface for testing
					{}, // empty interface for testing
					{}, // empty interface for testing
				},
			},
		},
		{
			TypeF: "VirtualMachine",
			GeneralF: &v1.General{
				HostnameF: "linux",
			},
			HardwareF: &v1.Hardware{
				OSTypeF: "linux",
				DrivesF: []*v1.Drive{
					{
						ImageF: "foobar",
					},
				},
			},
		},
		{
			TypeF: "VirtualMachine",
			GeneralF: &v1.General{
				HostnameF: "windows",
			},
			HardwareF: &v1.Hardware{
				OSTypeF: "windows",
				DrivesF: []*v1.Drive{
					{
						ImageF: "foobar",
					},
				},
			},
			InjectionsF: []*v1.Injection{
				{
					DstF: "startup.ps1",
				},
			},
		},
	}

	expected := [][]ifaces.NodeInjection{
		nil, // router
		{ // centos-linux
			&v1.Injection{
				SrcF: fmt.Sprintf("%s/startup/centos-linux-hostname.sh", baseDir),
				DstF: "/etc/phenix/startup/1_hostname-start.sh",
			},
			&v1.Injection{
				SrcF: fmt.Sprintf("%s/startup/centos-linux-timezone.sh", baseDir),
				DstF: "/etc/phenix/startup/2_timezone-start.sh",
			},
			&v1.Injection{
				SrcF: fmt.Sprintf("%s/startup/interfaces-centos-linux-eth0", baseDir),
				DstF: "/etc/sysconfig/network-scripts/ifcfg-eth0",
			},
			&v1.Injection{
				SrcF: fmt.Sprintf("%s/startup/interfaces-centos-linux-eth1", baseDir),
				DstF: "/etc/sysconfig/network-scripts/ifcfg-eth1",
			},
		},
		{ // rhel-linux
			&v1.Injection{
				SrcF: fmt.Sprintf("%s/startup/rhel-linux-hostname.sh", baseDir),
				DstF: "/etc/phenix/startup/1_hostname-start.sh",
			},
			&v1.Injection{
				SrcF: fmt.Sprintf("%s/startup/rhel-linux-timezone.sh", baseDir),
				DstF: "/etc/phenix/startup/2_timezone-start.sh",
			},
			&v1.Injection{
				SrcF: fmt.Sprintf("%s/startup/interfaces-rhel-linux-eth0", baseDir),
				DstF: "/etc/sysconfig/network-scripts/ifcfg-eth0",
			},
			&v1.Injection{
				SrcF: fmt.Sprintf("%s/startup/interfaces-rhel-linux-eth1", baseDir),
				DstF: "/etc/sysconfig/network-scripts/ifcfg-eth1",
			},
			&v1.Injection{
				SrcF: fmt.Sprintf("%s/startup/interfaces-rhel-linux-eth2", baseDir),
				DstF: "/etc/sysconfig/network-scripts/ifcfg-eth2",
			},
		},
		{ // linux
			&v1.Injection{
				SrcF: fmt.Sprintf("%s/startup/linux-hostname.sh", baseDir),
				DstF: "/etc/phenix/startup/1_hostname-start.sh",
			},
			&v1.Injection{
				SrcF: fmt.Sprintf("%s/startup/linux-timezone.sh", baseDir),
				DstF: "/etc/phenix/startup/2_timezone-start.sh",
			},
			&v1.Injection{
				SrcF: fmt.Sprintf("%s/startup/linux-interfaces", baseDir),
				DstF: "/etc/network/interfaces",
			},
		},
		{ // windows
			&v1.Injection{
				SrcF: fmt.Sprintf("%s/startup/windows-startup.ps1", baseDir),
				DstF: "startup.ps1",
			},
			&v1.Injection{
				SrcF: fmt.Sprintf("%s/startup/startup-scheduler.cmd", baseDir),
				DstF: "ProgramData/Microsoft/Windows/Start Menu/Programs/StartUp/startup_scheduler.cmd",
			},
		},
	}

	spec := &v1.ExperimentSpec{
		BaseDirF: baseDir,
		TopologyF: &v1.TopologySpec{
			NodesF: nodes,
		},
	}

	exp := &types.Experiment{Spec: spec}

	app := GetApp("startup")

	if err := app.PreStart(exp); err != nil {
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
