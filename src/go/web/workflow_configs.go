package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"phenix/api/config"
	"phenix/api/workflow"
	"phenix/store"
	"phenix/web/weberror"
)

// parseDryRun reports whether the dryRun query parameter asks for a dry run.
// A bare dryRun is refused rather than taken as a real request.
func parseDryRun(q url.Values) (bool, error) {
	if !q.Has("dryRun") {
		return false, nil
	}

	value := q.Get("dryRun")

	dryRun, err := strconv.ParseBool(value)
	if err != nil {
		return false, weberror.NewWebError(err, "invalid dryRun value %q", value)
	}

	return dryRun, nil
}

// checkConfig runs the checks a dry run and a real upsert share: the schema,
// then, for a Topology, the node checks an experiment would fail on.
func checkConfig(cfg *store.Config) error {
	if err := config.Validate(cfg); err != nil {
		return err
	}

	return workflow.CheckConfig(*cfg)
}

// updateOrValidate checks cfg and, unless dryRun is true, replaces the stored
// config name with it through [config.Update].
func updateOrValidate(name string, cfg *store.Config, dryRun bool) error {
	if err := checkConfig(cfg); err != nil {
		return err
	}

	if dryRun {
		return nil
	}

	return config.Update(name, cfg)
}

// createOrValidate checks cfg and, unless dryRun is true, stores it through
// [config.Create] and returns the stored config.
func createOrValidate(cfg *store.Config, dryRun bool) (*store.Config, error) {
	if err := checkConfig(cfg); err != nil {
		return cfg, err
	}

	if dryRun {
		return cfg, nil
	}

	return config.Create(config.CreateFromConfig(cfg), config.CreateWithValidation())
}

// writeConfigDryRun writes the 200 response to a config dry run: what the
// upsert would do, cfg's expanded kind and name, and its pending ref.
func writeConfigDryRun(w http.ResponseWriter, exists bool, cfg *store.Config) {
	action := workflow.ActionCreate
	if exists {
		action = workflow.ActionUpdate
	}

	// No HTML escaping, so the "&" in a pending ref can be copied from the raw
	// body. A ConfigResult holds only strings and a bool, so encoding it cannot
	// fail; TrimSpace drops the newline Encode appends.
	var buf bytes.Buffer

	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(workflow.ConfigResult{
		Action:  action,
		Kind:    cfg.Kind,
		Name:    cfg.Metadata.Name,
		DryRun:  true,
		Pending: workflow.PendingRef(*cfg),
	})

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(bytes.TrimSpace(buf.Bytes()))
}
