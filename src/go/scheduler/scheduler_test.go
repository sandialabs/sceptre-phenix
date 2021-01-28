package scheduler

import v1 "phenix/types/version/v1"

var nodes = []*v1.Node{
	{
		GeneralF: &v1.General{
			HostnameF: "foo",
		},
		HardwareF: &v1.Hardware{
			VCPUF:   2,
			MemoryF: 2048,
		},
		NetworkF: &v1.Network{
			InterfacesF: []*v1.Interface{
				{
					VLANF: "hello",
				},
			},
		},
	},
	{
		GeneralF: &v1.General{
			HostnameF: "bar",
		},
		HardwareF: &v1.Hardware{
			VCPUF:   1,
			MemoryF: 2048,
		},
		NetworkF: &v1.Network{
			InterfacesF: []*v1.Interface{
				{
					VLANF: "world",
				},
			},
		},
	},
	{
		GeneralF: &v1.General{
			HostnameF: "sucka",
		},
		HardwareF: &v1.Hardware{
			VCPUF:   4,
			MemoryF: 8192,
		},
		NetworkF: &v1.Network{
			InterfacesF: []*v1.Interface{
				{
					VLANF: "hello",
				},
			},
		},
	},
	{
		GeneralF: &v1.General{
			HostnameF: "fish",
		},
		HardwareF: &v1.Hardware{
			VCPUF:   1,
			MemoryF: 512,
		},
		NetworkF: &v1.Network{
			InterfacesF: []*v1.Interface{
				{
					VLANF: "world",
				},
			},
		},
	},
}
