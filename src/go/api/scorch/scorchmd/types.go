package scorchmd

import (
	"phenix/util"
	"phenix/util/tap"
)

/*
spec:
  apps:
  - name: scorch
    metadata:
      filebeat:
        enabled: false
        expNameAsIndexName: true
        config:
          output.elasticsearch:
            hosts: ["localhost:9200"]
            index: "scorch-%{+yyyy.MM.dd}"
          setup:
            ilm.enabled: false
            template.name: "filebeat"
            template.pattern: "scorch-*"
            template.overwrite: "false"
          filebeat.shutdown_timeout: 60s
      runs:
      - count: 1
        configure: []
        start: [mooncake_topo, break]
        stop: [mooncake_topo]
        cleanup: []
        loop:
          count: 2
          configure: []
          start: [mooncake_apps]
          stop: [mooncake_apps]
          cleanup: []
          loop:
            count: 3
            configure: []
            start: [mooncake_apps]
            stop: [mooncake_apps]
            cleanup: []
      components:
      - name: mooncake_topo
        metadata:
          test-one:
            disk    : miniccc.qc2
            memory  : 512
            snapshot: true
            networks: onenet
          test-two:
            disk    : miniccc.qc2
            memory  : 512
            snapshot: true
            networks: twonet
          test-router:
            disk    : minirouter.qc2
            memory  : 512
            snapshot: true
            networks: onenet twonet
      - name: break
        metadata: {}
      - name: mooncake_apps
        metadata:
          inject:
          - test-one: [test.yml]
          run_start: "bash foo /test.yml"
          extract:
          - test-one: [test.yml]
          run_stop: "bash cleanup"
          filebeat.inputs:
          - type: log
            enabled: true
            paths:
            - "test.yml_test-one"
*/

type ScorchMetadata struct {
	Filebeat   FilebeatSpec    `mapstructure:"filebeat"`
	Runs       []*Loop         `mapstructure:"runs"`
	Components []ComponentSpec `mapstructure:"components"`

	components ComponentSpecMap
}

func (this ScorchMetadata) ComponentSpecs() ComponentSpecMap {
	return this.components
}

func (this ScorchMetadata) FilebeatEnabled(id int) bool {
	run := this.Runs[id]
	return (run.Filebeat == nil && this.Filebeat.Enabled) || (run.Filebeat != nil && run.Filebeat.Enabled)
}

func (this ScorchMetadata) FilebeatConfig(id int) map[string]interface{} {
	run := this.Runs[id]

	if run.Filebeat == nil {
		return this.Filebeat.Config
	}

	return run.Filebeat.Config
}

func (this ScorchMetadata) UseExpNameAsIndexName(id int) bool {
	run := this.Runs[id]

	if run.Filebeat == nil && this.Filebeat.Enabled {
		return this.Filebeat.ExpAsIndex
	}

	if run.Filebeat != nil && run.Filebeat.Enabled {
		return run.Filebeat.ExpAsIndex
	}

	return false
}

type Loop struct {
	Filebeat  *FilebeatSpec `mapstructure:"filebeat"`
	Count     int           `mapstructure:"count"`
	Configure []string      `mapstructure:"configure"`
	Start     []string      `mapstructure:"start"`
	Stop      []string      `mapstructure:"stop"`
	Cleanup   []string      `mapstructure:"cleanup"`
	Loop      *Loop         `mapstructure:"loop"` // using a pointer here to avoid cyclical references
}

func (this Loop) ContainsComponent(name string) bool {
	if util.StringSliceContains(this.Configure, name) {
		return true
	}

	if util.StringSliceContains(this.Start, name) {
		return true
	}

	if util.StringSliceContains(this.Stop, name) {
		return true
	}

	if util.StringSliceContains(this.Cleanup, name) {
		return true
	}

	if this.Loop != nil {
		return this.Loop.ContainsComponent(name)
	}

	return false
}

type ComponentSpec struct {
	Name       string            `mapstructure:"name"`
	Type       string            `mapstructure:"type"`
	Background bool              `mapstructure:"background"`
	Metadata   ComponentMetadata `mapstructure:"metadata"`
}

type FilebeatSpec struct {
	Enabled    bool                   `mapstructure:"enabled"`
	ExpAsIndex bool                   `mapstructure:"expNameAsIndexName" structs:"expNameAsIndexName"`
	Config     map[string]interface{} `mapstructure:"config"`
}

type ComponentMetadata map[string]interface{}
type ComponentSpecMap map[string]ComponentSpec

type ScorchStatus struct {
	RunID int                 `structs:"runID" mapstructure:"runID"`
	Taps  map[string]*tap.Tap `structs:"taps" mapstructure:"taps"`
}
