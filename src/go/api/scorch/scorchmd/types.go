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

func (m ScorchMetadata) ComponentSpecs() ComponentSpecMap {
	return m.components
}

func (m ScorchMetadata) RunName(id int) string {
	if len(m.Runs) > id {
		return m.Runs[id].Name
	}

	return ""
}

func (m ScorchMetadata) FilebeatEnabled(id int) bool {
	run := m.Runs[id]

	return (run.Filebeat == nil && m.Filebeat.Enabled) ||
		(run.Filebeat != nil && run.Filebeat.Enabled)
}

func (m ScorchMetadata) FilebeatConfig(id int) map[string]any {
	run := m.Runs[id]

	if run.Filebeat == nil {
		return m.Filebeat.Config
	}

	return run.Filebeat.Config
}

func (m ScorchMetadata) UseExpNameAsIndexName(id int) bool {
	run := m.Runs[id]

	if run.Filebeat == nil && m.Filebeat.Enabled {
		return m.Filebeat.ExpAsIndex
	}

	if run.Filebeat != nil && run.Filebeat.Enabled {
		return run.Filebeat.ExpAsIndex
	}

	return false
}

type Loop struct {
	Filebeat  *FilebeatSpec  `mapstructure:"filebeat"`
	Count     int            `mapstructure:"count"`
	Name      string         `mapstructure:"name"`
	Replace   map[string]any `mapstructure:"replace"`
	Configure []string       `mapstructure:"configure"`
	Start     []string       `mapstructure:"start"`
	Stop      []string       `mapstructure:"stop"`
	Cleanup   []string       `mapstructure:"cleanup"`
	Loop      *Loop          `mapstructure:"loop"` // using a pointer here to avoid cyclical references
}

func (l Loop) ContainsComponent(name string) bool {
	if util.StringSliceContains(l.Configure, name) {
		return true
	}

	if util.StringSliceContains(l.Start, name) {
		return true
	}

	if util.StringSliceContains(l.Stop, name) {
		return true
	}

	if util.StringSliceContains(l.Cleanup, name) {
		return true
	}

	if l.Loop != nil {
		return l.Loop.ContainsComponent(name)
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
	Enabled    bool           `mapstructure:"enabled"`
	ExpAsIndex bool           `mapstructure:"expNameAsIndexName" structs:"expNameAsIndexName"`
	Config     map[string]any `mapstructure:"config"`
}

type (
	ComponentMetadata map[string]any
	ComponentSpecMap  map[string]ComponentSpec
)

type ScorchStatus struct {
	RunID int                 `mapstructure:"runID" structs:"runID"`
	Taps  map[string]*tap.Tap `mapstructure:"taps"  structs:"taps"`
}
