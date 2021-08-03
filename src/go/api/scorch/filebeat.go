package scorch

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"phenix/api/scorch/scorchmd"
	"time"

	"gopkg.in/yaml.v3"
)

func createFilebeatConfig(md scorchmd.ScorchMetadata, expName, expDir string, runID int) error {
	if err := mergeFilebeatConfig(md, expName, expDir, runID); err != nil {
		return fmt.Errorf("merging Filebeat configs: %w", err)
	}

	if err := writeFilebeatConfig(md, expDir, runID); err != nil {
		return fmt.Errorf("writing Filebeat config: %w", err)
	}

	return nil
}

func mergeFilebeatConfig(md scorchmd.ScorchMetadata, expName, expDir string, runID int) error {
	var (
		c   = md.Filebeat.Config
		now = time.Now().UTC()
	)

	c["fields_under_root"] = true

	c["fields"] = map[string]interface{}{
		"s_time":            now,
		"scorch.experiment": expName,
		"scorch.run_id":     runID,
		"scorch.runtime":    now,
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

					// add a few default Filebeat processors for first path only

					if pathStr, ok := paths[0].(string); ok {
						fileName := filepath.Base(pathStr)

						dissector := map[string]interface{}{
							"tokenizer":     fmt.Sprintf("%s/%%{scorch.itername}/%s", baseDir, fileName),
							"field":         "log.file.path",
							"target_prefix": "",
						}

						addFields := map[string]interface{}{
							"target": "",
							"fields": map[string]interface{}{
								"scorch.output_file": fileName,
							},
						}

						processors = append(
							processors,
							map[string]interface{}{"dissect": dissector},
							map[string]interface{}{"add_fields": addFields},
						)

						in["processors"] = processors
					}
				}
			}

			inputs = append(inputs, ins...)
		}
	}

	c["filebeat.inputs"] = inputs

	c["filebeat.registry.path"] = fmt.Sprintf("%s/scorch/run-%d/filebeat/registry", expDir, runID)

	return nil
}

func writeFilebeatConfig(md scorchmd.ScorchMetadata, expDir string, runID int) error {
	baseDir := fmt.Sprintf("%s/scorch/run-%d", expDir, runID)

	os.RemoveAll(fmt.Sprintf("%s/filebeat", baseDir))

	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)

	if err := encoder.Encode(md.Filebeat.Config); err != nil {
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
