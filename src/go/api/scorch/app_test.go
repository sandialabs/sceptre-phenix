package scorch

import (
	"context"
	"fmt"

	"phenix/api/scorch/scorchmd"

	"github.com/mitchellh/mapstructure"
	"gopkg.in/yaml.v3"
)

var data = `
execute:
  configure: [break]
  start: [break]
  stop: [break]
  cleanup: [break]
  loop:
    execute:
      configure: []
      start: [break]
      stop: [break]
      cleanup: []
      loop:
        execute:
          configure: [break]
          start: []
          stop: []
          cleanup: [break]
        count: 3
    count: 2
components:
- name: break
  metadata: {}
`

func ExampleExecuteLoop() {
	ms := make(map[string]interface{})

	if err := yaml.Unmarshal([]byte(data), &ms); err != nil {
		fmt.Println(err)
		return
	}

	var md scorchmd.ScorchMetadata

	if err := mapstructure.Decode(ms, &md); err != nil {
		fmt.Println(err)
		return
	}

	components := make(map[string]scorchmd.ComponentSpec)

	for _, c := range md.Components {
		components[c.Name] = c
	}

	if err := executor(context.Background(), components, md.Execute); err != nil {
		fmt.Println(err)
		return
	}

	// Output:
	// configure break
	// start break
	// start break
	// configure break
	// cleanup break
	// configure break
	// cleanup break
	// configure break
	// cleanup break
	// stop break
	// start break
	// configure break
	// cleanup break
	// configure break
	// cleanup break
	// configure break
	// cleanup break
	// stop break
	// stop break
	// cleanup break
}
