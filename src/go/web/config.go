package web

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"gopkg.in/yaml.v3"

	"phenix/api/config"
	"phenix/api/experiment"
	"phenix/store"
	"phenix/types"
	"phenix/types/version"
	"phenix/util/plog"
	"phenix/web/broker"
	bt "phenix/web/broker/brokertypes"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/util"
	"phenix/web/weberror"
)

const kindExperiment = "Experiment"
const MaxUploadSize = 1 << 20

// GetConfigs - GET /configs.
func GetConfigs(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "GetConfigs")

	var (
		ctx     = r.Context()
		role, _ = ctx.Value(middleware.ContextKeyRole).(rbac.Role)
		query   = r.URL.Query()
		kind    = query.Get("kind")
	)

	if !role.Allowed("configs", "list") {
		user, _ := ctx.Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"listing configs not allowed",
			"user",
			user,
		)
		err := weberror.NewWebError(
			nil,
			"listing configs not allowed for %s",
			user,
		)

		return err.SetStatus(http.StatusForbidden)
	}

	if kind == "" {
		kind = "all"
	}

	configs, err := config.List(kind)
	if err != nil {
		return weberror.NewWebError(err, "unable to get configs from store")
	}

	var allowed []store.Config

	for _, cfg := range configs {
		if !role.Allowed("configs", "list", cfg.FullName()) {
			continue
		}

		cfg.Spec = nil
		cfg.Status = nil

		allowed = append(allowed, cfg)
	}

	body, err := json.Marshal(util.WithRoot("configs", allowed))
	if err != nil {
		err := weberror.NewWebError(err, "unable to process configs")

		return err.SetStatus(http.StatusInternalServerError)
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis

	return nil
}

// DownloadConfigs - POST /configs/download.
//
//nolint:funlen // handler
func DownloadConfigs(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "DownloadConfigs")

	var (
		ctx     = r.Context()
		role, _ = ctx.Value(middleware.ContextKeyRole).(rbac.Role)
	)

	if !role.Allowed("configs", "get") {
		user, _ := ctx.Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"downloading config not allowed",
			"user",
			user,
		)
		err := weberror.NewWebError(
			nil,
			"downloading configs not allowed for %s",
			user,
		)

		return err.SetStatus(http.StatusForbidden)
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		err := weberror.NewWebError(err, "unable to read request")

		return err.SetStatus(http.StatusInternalServerError)
	}

	var configs []string

	if err := json.Unmarshal(body, &configs); err != nil {
		return weberror.NewWebError(err, "unable to parse request")
	}

	// TODO: check for len == 0

	if len(configs) == 1 {
		name := configs[0]

		if !role.Allowed("configs", "get", name) {
			user, _ := ctx.Value(middleware.ContextKeyUser).(string)
			plog.Warn(
				plog.TypeSecurity,
				"downloading config not allowed",
				"user",
				user,
				"config",
				name,
			)
			err := weberror.NewWebError(
				nil,
				"downloading config %s not allowed for %s",
				name,
				user,
			)

			return err.SetStatus(http.StatusForbidden)
		}

		cfg, err := config.Get(name, false)
		if err != nil {
			return weberror.NewWebError(err, "unable to get config %s from store", name)
		}

		// TODO: also clear passwords for users
		if cfg.Kind == kindExperiment {
			// Clear experiment name... not applicable to end users.
			delete(cfg.Spec, "experimentName")
		}

		body, err := yaml.Marshal(cfg)
		if err != nil {
			err := weberror.NewWebError(err, "unable to process config %s", name)

			return err.SetStatus(http.StatusInternalServerError)
		}

		fn := fmt.Sprintf("%s-%s.yml", cfg.Kind, cfg.Metadata.Name)

		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Disposition", "attachment; filename="+fn)
		user, _ := ctx.Value(middleware.ContextKeyUser).(string)
		plog.Info(
			plog.TypeAction,
			"downloaded config",
			"user",
			user,
			"config",
			name,
		)
		http.ServeContent(w, r, "", time.Now(), bytes.NewReader(body))

		return nil
	}

	zipper := zip.NewWriter(w)

	for _, name := range configs {
		if !role.Allowed("configs", "get", name) {
			continue
		}

		cfg, err := config.Get(name, false)
		if err != nil {
			return weberror.NewWebError(err, "unable to get config %s from store", name)
		}

		// TODO: also clear passwords for users
		if cfg.Kind == kindExperiment {
			// Clear experiment name... not applicable to end users.
			delete(cfg.Spec, "experimentName")
		}

		body, err := yaml.Marshal(cfg)
		if err != nil {
			err := weberror.NewWebError(err, "unable to process config %s", name)

			return err.SetStatus(http.StatusInternalServerError)
		}

		fn := fmt.Sprintf("%s-%s.yml", cfg.Kind, cfg.Metadata.Name)

		zf, err := zipper.Create(fn)
		if err != nil {
			plog.Error(plog.TypeSystem, "creating zip file entry", "file", fn, "err", err)

			continue
		}

		if _, err := zf.Write(body); err != nil {
			plog.Error(plog.TypeSystem, "writing config to zip file", "file", fn, "err", err)

			continue
		}
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename=configs.zip")

	// This will flush the zipped configs to the HTTP writer.
	if err := zipper.Close(); err != nil {
		plog.Error(plog.TypeSystem, "closing zip writer", "err", err)
	}

	user, _ := ctx.Value(middleware.ContextKeyUser).(string)
	plog.Info(
		plog.TypeAction,
		"downloaded configs",
		"user",
		user,
		"configs",
		strings.Join(configs, ","),
	)

	return nil
}

// CreateConfig - POST /configs.
//
//nolint:funlen // handler
func CreateConfig(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "CreateConfig")

	var (
		ctx     = r.Context()
		role, _ = ctx.Value(middleware.ContextKeyRole).(rbac.Role)
	)

	if !role.Allowed("configs", "create") {
		user, _ := ctx.Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"creating config not allowed",
			"user",
			user,
		)
		err := weberror.NewWebError(
			nil,
			"creating configs not allowed for %s",
			user,
		)

		return err.SetStatus(http.StatusForbidden)
	}

	var (
		typ   = r.Header.Get("Content-Type")
		body  []byte
		parse func([]byte) (*store.Config, error)
	)

	switch {
	case typ == mimeJSON: // default to JSON if not set
		var err error

		body, err = io.ReadAll(r.Body)
		if err != nil {
			err := weberror.NewWebError(err, "unable to parse request")

			return err.SetStatus(http.StatusInternalServerError)
		}

		parse = store.NewConfigFromJSON
	case typ == mimeYAML:
		var err error

		body, err = io.ReadAll(r.Body)
		if err != nil {
			err := weberror.NewWebError(err, "unable to parse request")

			return err.SetStatus(http.StatusInternalServerError)
		}

		parse = store.NewConfigFromYAML
	case strings.HasPrefix(typ, "multipart/form-data"): // file upload
		_ = r.ParseMultipartForm(MaxUploadSize)

		file, handler, err := r.FormFile("fileupload") // assume `fileupload` key used for upload
		if err != nil {
			err := weberror.NewWebError(err, "unable to access uploaded file")

			return err.SetStatus(http.StatusInternalServerError)
		}

		defer func() { _ = file.Close() }()

		switch filepath.Ext(handler.Filename) {
		case ".json":
			body, err = io.ReadAll(file)
			if err != nil {
				err := weberror.NewWebError(err, "unable to parse uploaded file")

				return err.SetStatus(http.StatusInternalServerError)
			}

			parse = store.NewConfigFromJSON
		case ".yaml", ".yml":
			body, err = io.ReadAll(file)
			if err != nil {
				err := weberror.NewWebError(err, "unable to parse uploaded file")

				return err.SetStatus(http.StatusInternalServerError)
			}

			parse = store.NewConfigFromYAML
		default:
			return weberror.NewWebError(
				nil,
				"unknown file extension for uploaded file: %s",
				handler.Filename,
			)
		}
	default:
		return weberror.NewWebError(
			nil,
			"unknown content type provided when creating config: %s",
			typ,
		)
	}

	c, err := parse(body)
	if err != nil {
		return weberror.NewWebError(err, "invalid formatting").
			WithMetadata("validation", err.Error(), true)
	}

	// The check above is for the creation of any config. This check is for the
	// config that the body names, so a role scoped to some kinds or names
	// creates no other config.
	if name := c.FullName(); !role.Allowed("configs", "create", name) {
		return configForbidden(ctx, "creating", name)
	}

	c, err = config.Create(config.CreateFromConfig(c), config.CreateWithValidation())
	if err != nil {
		if errors.Is(err, store.ErrExist) {
			return weberror.NewWebError(err, "config with same name already exists")
		}

		if errors.Is(err, types.ErrValidationFailed) {
			return validationWebError(body, err)
		}

		if errors.Is(err, version.ErrInvalidKind) {
			return weberror.NewWebError(err, "unknown config kind provided")
		}

		return weberror.NewWebError(err, "unable to create new config")
	}

	w.Header().
		Set("Location", strings.ToLower(fmt.Sprintf("/api/v1/configs/%s/%s", c.Kind, c.Metadata.Name)))
	w.WriteHeader(http.StatusCreated)

	if err := broadcastConfig(c, c.FullName(), "create"); err != nil {
		plog.Error(plog.TypeSystem, "marshaling config", "config", c.FullName(), "err", err)

		return nil
	}

	user, _ := ctx.Value(middleware.ContextKeyUser).(string)
	plog.Info(
		plog.TypeAction,
		"created config",
		"user",
		user,
		"config",
		c.FullName(),
	)

	return nil
}

// GetConfig - GET /configs/{kind}/{name}.
func GetConfig(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "GetConfig")

	var (
		ctx     = r.Context()
		role, _ = ctx.Value(middleware.ContextKeyRole).(rbac.Role)
		vars    = mux.Vars(r)
		name    = store.ConfigFullName(vars["kind"], vars["name"])
	)

	if !role.Allowed("configs", "get", name) {
		user, _ := ctx.Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"getting config not allowed",
			"user",
			user,
			"config",
			name,
		)
		err := weberror.NewWebError(
			nil,
			"getting config %s not allowed for %s",
			name,
			user,
		)

		return err.SetStatus(http.StatusForbidden)
	}

	upgrade := r.URL.Query().Get("noupgrade") == ""

	cfg, err := config.Get(name, upgrade)
	if err != nil {
		return weberror.NewWebError(err, "unable to get config %s from store", name)
	}

	if cfg.Kind == kindExperiment {
		// Clear experiment name... not applicable to end users.
		delete(cfg.Spec, "experimentName")
	}

	var body []byte

	switch typ := r.Header.Get("Accept"); typ {
	case "", mimeAny, mimeJSON: // default to JSON if not set
		var err error

		body, err = json.Marshal(cfg)
		if err != nil {
			err := weberror.NewWebError(err, "unable to process config %s", name)

			return err.SetStatus(http.StatusInternalServerError)
		}

		w.Header().Set("Content-Type", "application/json")
	case "application/x-yaml":
		var err error

		body, err = yaml.Marshal(cfg)
		if err != nil {
			err := weberror.NewWebError(err, "unable to process config %s", name)

			return err.SetStatus(http.StatusInternalServerError)
		}

		w.Header().Set("Content-Type", "application/x-yaml")
	default:
		return weberror.NewWebError(
			nil,
			"unknown accept content type provided when creating config: %s",
			typ,
		)
	}

	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis

	return nil
}

// UpdateConfig - PUT /configs/{kind}/{name}.
//
//nolint:funlen // handler
func UpdateConfig(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "UpdateConfig")

	var (
		ctx     = r.Context()
		role, _ = ctx.Value(middleware.ContextKeyRole).(rbac.Role)
		vars    = mux.Vars(r)
		name    = store.ConfigFullName(vars["kind"], vars["name"])
	)

	if !role.Allowed("configs", "update", name) {
		user, _ := ctx.Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"updating config not allowed",
			"user",
			user,
			"config",
			name,
		)
		err := weberror.NewWebError(
			nil,
			"updating config %s not allowed for %s",
			name,
			user,
		)

		return err.SetStatus(http.StatusForbidden)
	}

	var (
		typ = r.Header.Get("Content-Type")
		c   *store.Config
		src []byte
	)

	switch {
	case typ == mimeJSON: // default to JSON if not set
		body, err := io.ReadAll(r.Body)
		if err != nil {
			err := weberror.NewWebError(err, "unable to parse request")

			return err.SetStatus(http.StatusInternalServerError)
		}

		src = body
		c, err = store.NewConfigFromJSON(body)
		if err != nil {
			err := weberror.NewWebError(err, "unable to parse request")

			return err.SetStatus(http.StatusInternalServerError)
		}
	case typ == "application/x-yaml":
		body, err := io.ReadAll(r.Body)
		if err != nil {
			err := weberror.NewWebError(err, "unable to parse request")

			return err.SetStatus(http.StatusInternalServerError)
		}

		src = body
		c, err = store.NewConfigFromYAML(body)
		if err != nil {
			err := weberror.NewWebError(err, "unable to parse request")

			return err.SetStatus(http.StatusInternalServerError)
		}
	case strings.HasPrefix(typ, "multipart/form-data"): // file upload
		_ = r.ParseMultipartForm(MaxUploadSize)

		file, handler, err := r.FormFile("fileupload") // assume `fileupload` key used for upload
		if err != nil {
			err := weberror.NewWebError(err, "unable to access uploaded file")

			return err.SetStatus(http.StatusInternalServerError)
		}

		defer func() { _ = file.Close() }()

		switch filepath.Ext(handler.Filename) {
		case ".json":
			body, err := io.ReadAll(file)
			if err != nil {
				err := weberror.NewWebError(err, "unable to parse uploaded file")

				return err.SetStatus(http.StatusInternalServerError)
			}

			src = body
			c, err = store.NewConfigFromJSON(body)
			if err != nil {
				err := weberror.NewWebError(err, "unable to parse uploaded file")

				return err.SetStatus(http.StatusInternalServerError)
			}
		case ".yaml", ".yml":
			body, err := io.ReadAll(file)
			if err != nil {
				err := weberror.NewWebError(err, "unable to parse uploaded file")

				return err.SetStatus(http.StatusInternalServerError)
			}

			src = body
			c, err = store.NewConfigFromYAML(body)
			if err != nil {
				err := weberror.NewWebError(err, "unable to parse uploaded file")

				return err.SetStatus(http.StatusInternalServerError)
			}
		default:
			return weberror.NewWebError(
				nil,
				"unknown file extension for uploaded file: %s",
				handler.Filename,
			)
		}
	default:
		return weberror.NewWebError(
			nil,
			"unknown content type provided when updating config: %s",
			typ,
		)
	}

	// The config is stored under the kind and name that the body gives. These
	// can differ from those of the path: a rename, or a different config. Thus
	// the caller must also have permission to update that config.
	if target := c.FullName(); target != name && !role.Allowed("configs", "update", target) {
		return configForbidden(ctx, "updating", target)
	}

	if c.Kind == kindExperiment {
		// Reset experiment name in spec since we removed it before sending.
		c.Spec["experimentName"] = vars["name"]
	}

	// Renaming a topology removes the published Builder documents of its
	// old name, as deleting it does, so it waits for a publication under way.
	if c.Metadata.Name != vars["name"] {
		defer lockBuilderPublishing(name)()
	}

	if err := config.Update(name, c); err != nil {
		if errors.Is(err, store.ErrNotExist) {
			return weberror.NewWebError(err, "config to update (%s) does not exist", name)
		}

		if errors.Is(err, types.ErrValidationFailed) {
			return validationWebError(src, err)
		}

		if errors.Is(err, store.ErrInvalidFormat) {
			cause := errors.Unwrap(err)

			return weberror.NewWebError(cause, "invalid formatting").
				WithMetadata("validation", cause.Error(), true)
		}

		return weberror.NewWebError(err, "unable to update config %s", name)
	}

	if c.Kind == kindExperiment {
		err := experiment.Reconfigure(c.Metadata.Name)
		if err != nil {
			return weberror.NewWebError(
				err,
				"unable to reconfigure updated experiment %s",
				c.Metadata.Name,
			)
		}
	}

	w.Header().
		Set("Location", strings.ToLower(fmt.Sprintf("/api/v1/configs/%s/%s", c.Kind, c.Metadata.Name)))
	w.WriteHeader(http.StatusNoContent)

	// The old name, so clients know which config to update.
	if err := broadcastConfig(c, name, "update"); err != nil {
		plog.Error(plog.TypeSystem, "marshaling config", "config", c.FullName(), "err", err)

		return nil
	}
	user, _ := ctx.Value(middleware.ContextKeyUser).(string)
	plog.Info(
		plog.TypeAction,
		"updated config",
		"user",
		user,
		"config",
		name,
	)

	return nil
}

// DeleteConfig - DELETE /configs/{kind}/{name}.
func DeleteConfig(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "DeleteConfig")

	var (
		ctx     = r.Context()
		role, _ = ctx.Value(middleware.ContextKeyRole).(rbac.Role)
		vars    = mux.Vars(r)
		name    = store.ConfigFullName(vars["kind"], vars["name"])
	)

	if !role.Allowed("configs", "delete", name) {
		user, _ := ctx.Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"deleting config not allowed",
			"user",
			user,
			"config",
			name,
		)
		err := weberror.NewWebError(
			nil,
			"deleting config %s not allowed for %s",
			name,
			user,
		)

		return err.SetStatus(http.StatusForbidden)
	}

	// Deleting a topology removes its published Builder documents, so it
	// waits for a publication under way.
	defer lockBuilderPublishing(name)()

	if err := deleteConfig(name); err != nil {
		return weberror.NewWebError(err, "unable to update config %s", name)
	}

	w.WriteHeader(http.StatusNoContent)
	user, _ := ctx.Value(middleware.ContextKeyUser).(string)
	plog.Info(
		plog.TypeAction,
		"deleted config",
		"user",
		user,
		"config",
		name,
	)

	return nil
}

// configForbidden logs and returns the refusal of a request whose role may
// not act on the config name. action says what the request asked for, such
// as "creating".
func configForbidden(ctx context.Context, action, name string) error {
	user, _ := ctx.Value(middleware.ContextKeyUser).(string)
	plog.Warn(
		plog.TypeSecurity,
		action+" config not allowed",
		"user",
		user,
		"config",
		name,
	)

	return weberror.NewWebError(nil, "%s config %s not allowed for %s", action, name, user).
		SetStatus(http.StatusForbidden)
}

// broadcastConfig tells everyone who may list the config c that it was
// created or updated (action). name is the config's full name as clients
// know it: after a rename, its old name. The broadcast carries c without its
// spec and status.
func broadcastConfig(c *store.Config, name, action string) error {
	summary := *c
	summary.Spec = nil
	summary.Status = nil

	body, err := json.Marshal(summary)
	if err != nil {
		return fmt.Errorf("encoding config broadcast: %w", err)
	}

	broker.Broadcast(
		bt.NewRequestPolicy("configs", "list", c.FullName()),
		bt.NewResource("config", name, action),
		body,
	)

	return nil
}

// deleteConfig deletes the config name through the config API, which runs
// the delete hooks of the kind. Then it tells everyone who may list the
// config that it is gone.
func deleteConfig(name string) error {
	if err := config.Delete(name); err != nil {
		return err //nolint:wrapcheck // callers word the error themselves
	}

	broker.Broadcast(
		bt.NewRequestPolicy("configs", "list", name),
		bt.NewResource("config", name, "delete"),
		nil,
	)

	return nil
}

// validationWebError turns a config validation error into a 400 whose message
// is the first line from [types.ExplainValidationError]; metadata.validation
// holds all of them and metadata.validation-raw the validator's own text. src
// is the document the client sent.
func validationWebError(src []byte, err error) error {
	cause := errors.Unwrap(err)
	if cause == nil {
		cause = err
	}

	lines := types.ExplainValidationError(src, cause)

	return weberror.NewWebError(cause, "%s", lines[0]).
		WithMetadata("validation", strings.Join(lines, "\n"), true).
		WithMetadata("validation-raw", cause.Error(), true)
}
