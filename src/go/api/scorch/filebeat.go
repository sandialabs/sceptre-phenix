package scorch

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"phenix/api/scorch/scorchmd"

	"gopkg.in/yaml.v3"
)

type filebeatMetrics struct {
	Started int `json:"filebeat.harvester.started"`
	Open    int `json:"filebeat.harvester.open_files"`
	Closed  int `json:"filebeat.harvester.closed"`
	Running int `json:"filebeat.harvester.running"`
}

func (this filebeatMetrics) Progress(prev filebeatMetrics) bool {
	if this.Started > prev.Started {
		return true
	}

	if this.Open > prev.Open {
		return true
	}

	if this.Closed > prev.Closed {
		return true
	}

	if this.Running > prev.Running {
		return true
	}

	return false
}

func (this filebeatMetrics) Done() bool {
	if this.Started == 0 {
		return false
	}

	if this.Closed != this.Started {
		return false
	}

	if this.Open != 0 {
		return false
	}

	if this.Running != 0 {
		return false
	}

	return true
}

func createFilebeatConfig(md scorchmd.ScorchMetadata, expName, expDir, startTime string, runID int) (int, error) {
	inputs, err := mergeFilebeatConfig(md, expName, expDir, startTime, runID)
	if err != nil {
		return inputs, fmt.Errorf("merging Filebeat configs: %w", err)
	}

	if err := writeFilebeatConfig(md, expDir, runID); err != nil {
		return inputs, fmt.Errorf("writing Filebeat config: %w", err)
	}

	return inputs, nil
}

func mergeFilebeatConfig(md scorchmd.ScorchMetadata, expName, expDir, startTime string, runID int) (int, error) {
	c := md.FilebeatConfig(runID)

	if md.UseExpNameAsIndexName(runID) { // force index name to be experiment name
		if v, ok := c["output.elasticsearch"]; ok {
			o := v.(map[string]interface{})

			o["index"] = fmt.Sprintf("experiment-%s", expName)
			c["output.elasticsearch"] = o
		} else {
			c["output.elasticsearch"] = map[string]interface{}{"index": fmt.Sprintf("experiment-%s", expName)}
		}

		if v, ok := c["setup"]; ok {
			s := v.(map[string]interface{})

			s["ilm.enabled"] = false
			s["template.name"] = "filebeat"
			s["template.pattern"] = "experiment-*"
			s["template.overwrite"] = false

			c["setup"] = s
		} else {
			c["setup"] = map[string]interface{}{
				"ilm.enabled":        false,
				"template.name":      "filebeat",
				"template.pattern":   "experiment-*",
				"template.overwrite": false,
			}
		}
	}

	c["fields_under_root"] = true

	c["fields"] = map[string]interface{}{
		"s_time":            startTime,
		"scorch.experiment": expName,
		"scorch.run_id":     runID,
		"scorch.runtime":    startTime,
	}

	c["processors"] = []map[string]interface{}{
		{
			"timestamp": map[string]interface{}{
				"field":        "s_time",
				"layouts":      []string{"Mon Jan 02 15:04:05 -0700 2006"},
				"target_field": "scorch.start_time",
			},
		},
		{
			"drop_fields": map[string]interface{}{
				"fields": []string{"s_time"},
			},
		},
	}

	if _, ok := c["logging.level"]; !ok {
		// default log level to warning if not set in config
		c["logging.level"] = "warning"
	}

	c["logging.to_files"] = true

	c["logging.files"] = map[string]interface{}{
		"path":       fmt.Sprintf("%s/scorch/run-%d/filebeat", expDir, runID),
		"name":       "filebeat.log",
		"keepfiles":  "7",
		"permission": "0644",
	}

	var inputs []interface{}

	if v, ok := c["filebeat.inputs"]; ok {
		inputs, _ = v.([]interface{})
	}

	for _, cmp := range md.Components {
		run := md.Runs[runID]

		if !run.ContainsComponent(cmp.Name) {
			continue
		}

		baseDir := fmt.Sprintf("%s/scorch/run-%d/%s", expDir, runID, cmp.Name)

		if v, ok := cmp.Metadata["filebeat.inputs"]; ok {
			ins, _ := v.([]interface{})

			for _, e := range ins {
				in, _ := e.(map[string]interface{})

				var processors []interface{}

				if v, ok := in["processors"]; ok {
					processors, _ = v.([]interface{})
				}

				if v, ok := in["paths"]; ok {
					paths, _ := v.([]interface{})

					if len(paths) == 0 {
						continue
					}

					for idx, path := range paths {
						pathStr, ok := path.(string)
						if !ok {
							continue
						}

						fileName := filepath.Base(pathStr)

						// update path in Filebeat config to be full path used on disk
						paths[idx] = fmt.Sprintf("%s/loop-*-count-*/%s", baseDir, fileName)
					}

					in["paths"] = paths

					if _, ok := in["close_inactive"]; !ok {
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

					dissector := map[string]interface{}{
						"tokenizer":     fmt.Sprintf("%s/%%{scorch.itername}/%%{scorch.output_file}", baseDir),
						"field":         "log.file.path",
						"target_prefix": "",
					}

					processors = append(
						processors,
						map[string]interface{}{"dissect": dissector},
					)

					in["processors"] = processors
				}
			}

			inputs = append(inputs, ins...)
		}
	}

	c["filebeat.inputs"] = inputs

	c["filebeat.registry.path"] = fmt.Sprintf("%s/scorch/run-%d/filebeat/registry", expDir, runID)

	return len(inputs), nil
}

func writeFilebeatConfig(md scorchmd.ScorchMetadata, expDir string, runID int) error {
	baseDir := fmt.Sprintf("%s/scorch/run-%d", expDir, runID)

	os.RemoveAll(fmt.Sprintf("%s/filebeat", baseDir))

	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)

	if err := encoder.Encode(md.FilebeatConfig(runID)); err != nil {
		return fmt.Errorf("marshaling Filebeat config: %w", err)
	}

	dst := fmt.Sprintf("%s/filebeat/filebeat.yml", baseDir)

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return fmt.Errorf("creating directory for Filebeat config: %w", err)
	}

	if err := os.WriteFile(dst, buf.Bytes(), 0644); err != nil {
		return fmt.Errorf("writing Filebeat config to file: %w", err)
	}

	reg := fmt.Sprintf("%s/filebeat/registry", baseDir)

	if err := os.MkdirAll(reg, 0755); err != nil {
		return fmt.Errorf("creating directory for Filebeat registry: %w", err)
	}

	return nil
}
