package scorch

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"

	"phenix/api/scorch/scorchmd"
)

const yamlIndent = 2

type filebeatMetrics struct {
	Started int `json:"filebeat.harvester.started"`
	Open    int `json:"filebeat.harvester.open_files"`
	Closed  int `json:"filebeat.harvester.closed"`
	Running int `json:"filebeat.harvester.running"`
}

func (m filebeatMetrics) Progress(prev filebeatMetrics) bool {
	if m.Started > prev.Started {
		return true
	}

	if m.Open > prev.Open {
		return true
	}

	if m.Closed > prev.Closed {
		return true
	}

	if m.Running > prev.Running {
		return true
	}

	return false
}

func (m filebeatMetrics) Done() bool {
	if m.Started == 0 {
		return false
	}

	if m.Closed != m.Started {
		return false
	}

	if m.Open != 0 {
		return false
	}

	if m.Running != 0 {
		return false
	}

	return true
}

func createFilebeatConfig(
	md scorchmd.ScorchMetadata,
	expName string,
	runID int,
	runDir string,
	start time.Time,
) (int, error) {
	inputs := mergeFilebeatConfig(md, expName, runID, runDir, start)

	if err := writeFilebeatConfig(md, runID, runDir); err != nil {
		return inputs, fmt.Errorf("writing Filebeat config: %w", err)
	}

	return inputs, nil
}

//nolint:funlen // complex logic
func mergeFilebeatConfig(
	md scorchmd.ScorchMetadata,
	expName string,
	runID int,
	runDir string,
	start time.Time,
) int {
	c := md.FilebeatConfig(runID)

	if md.UseExpNameAsIndexName(runID) { // force index name to be experiment name
		if v, ok := c["output.elasticsearch"]; ok {
			o, _ := v.(map[string]any)

			o["index"] = "experiment-" + expName
			c["output.elasticsearch"] = o
		} else {
			c["output.elasticsearch"] = map[string]any{"index": "experiment-" + expName}
		}

		if v, ok := c["setup"]; ok {
			s, _ := v.(map[string]any)

			s["ilm.enabled"] = false
			s["template.name"] = "filebeat"
			s["template.pattern"] = "experiment-*"
			s["template.overwrite"] = false

			c["setup"] = s
		} else {
			c["setup"] = map[string]any{
				"ilm.enabled":        false,
				"template.name":      "filebeat",
				"template.pattern":   "experiment-*",
				"template.overwrite": false,
			}
		}
	}

	c["fields_under_root"] = true

	fields := map[string]any{
		"s_time":            start.Format(time.RubyDate),
		"scorch.experiment": expName,
		"scorch.run_id":     runID,
		"scorch.runtime":    start.Format(time.RubyDate),
	}

	if name := md.RunName(runID); name != "" {
		fields["scorch.run_name"] = name
	}

	c["fields"] = fields

	c["processors"] = []map[string]any{
		{
			"timestamp": map[string]any{
				"field":        "s_time",
				"layouts":      []string{"Mon Jan 02 15:04:05 -0700 2006"},
				"target_field": "scorch.start_time",
			},
		},
		{
			"drop_fields": map[string]any{
				"fields": []string{"s_time"},
			},
		},
	}

	if _, ok := c["logging.level"]; !ok {
		// default log level to warning if not set in config
		c["logging.level"] = "warning"
	}

	c["logging.to_files"] = true

	c["logging.files"] = map[string]any{
		"path":       filepath.Join(runDir, "filebeat"),
		"name":       "filebeat.log",
		"keepfiles":  "7",
		"permission": "0644",
	}

	var inputs []any

	if v, ok := c["filebeat.inputs"]; ok {
		inputs, _ = v.([]any)
	}

	for _, cmp := range md.Components {
		run := md.Runs[runID]

		if !run.ContainsComponent(cmp.Name) {
			continue
		}

		baseDir := filepath.Join(runDir, cmp.Name)

		if v, ok := cmp.Metadata["filebeat.inputs"]; ok {
			ins, _ := v.([]any)

			for _, e := range ins {
				in, _ := e.(map[string]any)

				var processors []any

				if v2, ok2 := in["processors"]; ok2 {
					processors, _ = v2.([]any)
				}

				if v2, ok2 := in["paths"]; ok2 {
					paths, _ := v2.([]any)

					if len(paths) == 0 {
						continue
					}

					for idx, path := range paths {
						pathStr, ok3 := path.(string)
						if !ok3 {
							continue
						}

						fileName := filepath.Base(pathStr)

						// update path in Filebeat config to be full path used on disk
						paths[idx] = fmt.Sprintf("%s/loop-*-count-*/%s", baseDir, fileName)
					}

					in["paths"] = paths

					if _, ok3 := in["close_inactive"]; !ok3 {
						// Since we don't anticipate having to tail files from VMs for
						// processing, we can close processed files almost immediately.
						// We don't close them immediately just in case whatever tool might
						// be generating the file phenix-side (e.g., tshark for pcap -->
						// json) ends up streaming to the file instead of writing to it all
						// at once.
						in["close_inactive"] = "5s"
					}

					// If this is ever updated, we need to also update the amount of
					// time we wait between the end of a run and checking Filebeat
					// metrics for the number of open harvesters.
					in["scan_frequency"] = "5s"

					// add a default Filebeat dissector

					dissector := map[string]any{
						"tokenizer":     baseDir + "/%{scorch.itername}/%{scorch.output_file}",
						"field":         "log.file.path",
						"target_prefix": "",
					}

					processors = append(
						processors,
						map[string]any{"dissect": dissector},
					)

					in["processors"] = processors
				}
			}

			inputs = append(inputs, ins...)
		}
	}

	c["filebeat.inputs"] = inputs

	c["filebeat.registry.path"] = filepath.Join(runDir, "filebeat", "registry")

	return len(inputs)
}

func writeFilebeatConfig(md scorchmd.ScorchMetadata, runID int, runDir string) error {
	_ = os.RemoveAll(runDir + "/filebeat")

	var buf bytes.Buffer

	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(yamlIndent)

	err := encoder.Encode(md.FilebeatConfig(runID))
	if err != nil {
		return fmt.Errorf("marshaling Filebeat config: %w", err)
	}

	dst := runDir + "/filebeat/filebeat.yml"

	err = os.MkdirAll(filepath.Dir(dst), 0o750)
	if err != nil {
		return fmt.Errorf("creating directory for Filebeat config: %w", err)
	}

	err = os.WriteFile(dst, buf.Bytes(), 0o600)
	if err != nil {
		return fmt.Errorf("writing Filebeat config to file: %w", err)
	}

	reg := runDir + "/filebeat/registry"

	err = os.MkdirAll(reg, 0o750)
	if err != nil {
		return fmt.Errorf("creating directory for Filebeat registry: %w", err)
	}

	return nil
}
