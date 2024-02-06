package version

import (
	"context"
	"testing"

	v2 "phenix/types/version/v2"

	"github.com/getkin/kin-openapi/openapi3"
	"gopkg.in/yaml.v3"
)

var topo = `
nodes:
- general:
    hostname: host-00
  hardware:
    drives:
    - image: miniccc.qc2
    memory: 512
    vcpus: 1
    os_type: linux
  network:
    interfaces:
    - address: 10.0.0.1
      mask: 24
      gateway: 10.0.0.254
      name: IF0
      proto: static
      type: ethernet
      vlan: EXP
  type: VirtualMachine
- general:
    hostname: hil
# network:
#   interfaces:
#   - address: 192.168.86.177
#     name: IF0
  type: HIL
  external: true
`

func TestSchema(t *testing.T) {
	s, err := openapi3.NewLoader().LoadFromData(v2.OpenAPI)
	if err != nil {
		t.Log(err)
		t.FailNow()
	}

	if err := s.Validate(context.Background()); err != nil {
		t.Log(err)
		t.FailNow()
	}

	ref, ok := s.Components.Schemas["Topology"]
	if !ok {
		t.Log("missing Topology schema")
		t.FailNow()
	}

	var spec interface{}
	if err := yaml.Unmarshal([]byte(topo), &spec); err != nil {
		t.Log(err)
		t.FailNow()
	}

	/*
		body, _ := json.Marshal(spec)
		json.Unmarshal(body, &spec)
	*/

	if err := ref.Value.VisitJSON(spec); err != nil {
		t.Log(err)
		t.FailNow()
	}
}
