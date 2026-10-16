package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"gopkg.in/yaml.v3"

	"phenix/api/config"
	"phenix/api/workflow"
	"phenix/store"
	"phenix/util/common"
	"phenix/util/plog"
	"phenix/web/middleware"
	"phenix/web/rbac"
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

// configName returns the "<kind>/<name>" of a JSON or YAML config body without
// reading the server's environment: ${BRANCH_NAME} becomes branch and each
// other placeholder a distinct number, so a parse error quotes only the body.
func configName(body []byte, typ, branch string) (string, error) {
	var n int

	src := common.EnvPlaceholder.ReplaceAllStringFunc(string(body), func(match string) string {
		if common.EnvPlaceholder.FindStringSubmatch(match)[1] == "BRANCH_NAME" {
			return branch
		}

		n++

		return strconv.Itoa(n)
	})

	var head struct {
		Kind     string `json:"kind"     yaml:"kind"`
		Metadata struct {
			Name string `json:"name" yaml:"name"`
		} `json:"metadata" yaml:"metadata"`
	}

	var err error
	if typ == mimeJSON {
		err = json.Unmarshal([]byte(src), &head)
	} else {
		err = yaml.Unmarshal([]byte(src), &head)
	}

	if err != nil {
		return "", fmt.Errorf("%w: %w", store.ErrInvalidFormat, err)
	}

	return fmt.Sprintf("%s/%s", head.Kind, head.Metadata.Name), nil
}

// authorizeConfigWrite reports whether the config name exists, once role may
// update it if it does or create it if it does not.
func authorizeConfigWrite(ctx context.Context, role rbac.Role, name string) (bool, error) {
	// NewConfig rejects a kind the store does not hold, such as Workflow, and
	// a name containing a slash; its nil result would make store.Get panic.
	tester, err := store.NewConfig(name)
	if err != nil {
		return false, weberror.NewWebError(err, "invalid config %s", name)
	}

	exists := true

	if err := store.Get(tester); err != nil {
		if !errors.Is(err, store.ErrNotExist) {
			err := weberror.NewWebError(err, "checking store for config")

			return false, err.SetStatus(http.StatusInternalServerError)
		}

		exists = false
	}

	verb, action := "update", "updating"
	if !exists {
		verb, action = "create", "creating"
	}

	if !role.Allowed("configs", verb, name) {
		user, _ := ctx.Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			action+" config not allowed",
			"user",
			user,
		)

		err := weberror.NewWebError(
			nil,
			"%s config %s not allowed for %s",
			action,
			name,
			user,
		)

		return false, err.SetStatus(http.StatusForbidden)
	}

	return exists, nil
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
