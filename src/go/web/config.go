package web

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"phenix/api/config"
	"phenix/api/experiment"
	"phenix/store"
	"phenix/types"
	"phenix/types/version"
	"phenix/util/plog"
	"phenix/web/broker"
	"phenix/web/rbac"
	"phenix/web/util"
	"phenix/web/weberror"

	bt "phenix/web/broker/brokertypes"

	"github.com/gorilla/mux"
	"gopkg.in/yaml.v3"
)

// GET /configs
func GetConfigs(w http.ResponseWriter, r *http.Request) error {
	plog.Debug("HTTP handler called", "handler", "GetConfigs")

	var (
		ctx   = r.Context()
		role  = ctx.Value("role").(rbac.Role)
		query = r.URL.Query()
		kind  = query.Get("kind")
	)

	if !role.Allowed("configs", "list") {
		err := weberror.NewWebError(nil, "listing configs not allowed for %s", ctx.Value("user").(string))
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
	w.Write(body)

	return nil
}

// POST /configs/download
func DownloadConfigs(w http.ResponseWriter, r *http.Request) error {
	plog.Debug("HTTP handler called", "handler", "DownloadConfigs")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
	)

	if !role.Allowed("configs", "get") {
		err := weberror.NewWebError(nil, "downloading configs not allowed for %s", ctx.Value("user").(string))
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
			err := weberror.NewWebError(nil, "downloading config %s not allowed for %s", name, ctx.Value("user").(string))
			return err.SetStatus(http.StatusForbidden)
		}

		cfg, err := config.Get(name, false)
		if err != nil {
			return weberror.NewWebError(err, "unable to get config %s from store", name)
		}

		// TODO: also clear passwords for users
		if cfg.Kind == "Experiment" {
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
		if cfg.Kind == "Experiment" {
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
			// TODO
			continue
		}

		if _, err := zf.Write(body); err != nil {
			// TODO
			continue
		}
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename=configs.zip")

	// This will flush the zipped configs to the HTTP writer.
	if err := zipper.Close(); err != nil {
		// TODO
	}

	return nil
}

// POST /configs
func CreateConfig(w http.ResponseWriter, r *http.Request) error {
	plog.Debug("HTTP handler called", "handler", "CreateConfig")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
	)

	if !role.Allowed("configs", "create") {
		err := weberror.NewWebError(nil, "creating configs not allowed for %s", ctx.Value("user").(string))
		return err.SetStatus(http.StatusForbidden)
	}

	var (
		typ  = r.Header.Get("Content-Type")
		opts = []config.CreateOption{config.CreateWithValidation()}
	)

	switch {
	case typ == "application/json": // default to JSON if not set
		body, err := io.ReadAll(r.Body)
		if err != nil {
			err := weberror.NewWebError(err, "unable to parse request")
			return err.SetStatus(http.StatusInternalServerError)
		}

		opts = append(opts, config.CreateFromJSON(body))
	case typ == "application/x-yaml":
		body, err := io.ReadAll(r.Body)
		if err != nil {
			err := weberror.NewWebError(err, "unable to parse request")
			return err.SetStatus(http.StatusInternalServerError)
		}

		opts = append(opts, config.CreateFromYAML(body))
	case strings.HasPrefix(typ, "multipart/form-data"): // file upload
		r.ParseMultipartForm(1 << 20) // max 1M file size

		file, handler, err := r.FormFile("fileupload") // assume `fileupload` key used for upload
		if err != nil {
			err := weberror.NewWebError(err, "unable to access uploaded file")
			return err.SetStatus(http.StatusInternalServerError)
		}

		defer file.Close()

		switch filepath.Ext(handler.Filename) {
		case ".json":
			body, err := io.ReadAll(file)
			if err != nil {
				err := weberror.NewWebError(err, "unable to parse uploaded file")
				return err.SetStatus(http.StatusInternalServerError)
			}

			opts = append(opts, config.CreateFromJSON(body))
		case ".yaml", ".yml":
			body, err := io.ReadAll(file)
			if err != nil {
				err := weberror.NewWebError(err, "unable to parse uploaded file")
				return err.SetStatus(http.StatusInternalServerError)
			}

			opts = append(opts, config.CreateFromYAML(body))
		default:
			return weberror.NewWebError(nil, "unknown file extension for uploaded file: %s", handler.Filename)
		}
	default:
		return weberror.NewWebError(nil, "unknown content type provided when creating config: %s", typ)
	}

	c, err := config.Create(opts...)
	if err != nil {
		if errors.Is(err, store.ErrExist) {
			return weberror.NewWebError(err, "config with same name already exists")
		}

		if errors.Is(err, types.ErrValidationFailed) {
			cause := errors.Unwrap(err)
			lines := strings.Split(cause.Error(), "\n")

			return weberror.NewWebError(cause, lines[0]).WithMetadata("validation", cause.Error(), true)
		}

		if errors.Is(err, store.ErrInvalidFormat) {
			cause := errors.Unwrap(err)
			return weberror.NewWebError(cause, "invalid formatting").WithMetadata("validation", cause.Error(), true)
		}

		if errors.Is(err, version.ErrInvalidKind) {
			return weberror.NewWebError(err, "unknown config kind provided")
		}

		return weberror.NewWebError(err, "unable to create new config")
	}

	w.Header().Set("Location", strings.ToLower(fmt.Sprintf("/api/v1/configs/%s/%s", c.Kind, c.Metadata.Name)))
	w.WriteHeader(http.StatusCreated)

	c.Spec = nil
	c.Status = nil

	body, err := json.Marshal(c)
	if err != nil {
		plog.Error("marshaling config", "config", c.FullName(), "err", err)
		return nil
	}

	broker.Broadcast(
		bt.NewRequestPolicy("configs", "list", c.FullName()),
		bt.NewResource("config", c.FullName(), "create"),
		body,
	)

	return nil
}

// GET /configs/{kind}/{name}
func GetConfig(w http.ResponseWriter, r *http.Request) error {
	plog.Debug("HTTP handler called", "handler", "GetConfig")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
		vars = mux.Vars(r)
		name = store.ConfigFullName(vars["kind"], vars["name"])
	)

	if !role.Allowed("configs", "get", name) {
		err := weberror.NewWebError(nil, "getting config %s not allowed for %s", name, ctx.Value("user").(string))
		return err.SetStatus(http.StatusForbidden)
	}

	upgrade := true

	if r.URL.Query().Get("noupgrade") != "" {
		upgrade = false
	}

	cfg, err := config.Get(name, upgrade)
	if err != nil {
		return weberror.NewWebError(err, "unable to get config %s from store", name)
	}

	if cfg.Kind == "Experiment" {
		// Clear experiment name... not applicable to end users.
		delete(cfg.Spec, "experimentName")
	}

	var body []byte

	switch typ := r.Header.Get("Accept"); typ {
	case "", "*/*", "application/json": // default to JSON if not set
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
		return weberror.NewWebError(nil, "unknown accept content type provided when creating config: %s", typ)
	}

	w.Write(body)

	return nil
}

// PUT /configs/{kind}/{name}
func UpdateConfig(w http.ResponseWriter, r *http.Request) error {
	plog.Debug("HTTP handler called", "handler", "UpdateConfig")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
		vars = mux.Vars(r)
		name = store.ConfigFullName(vars["kind"], vars["name"])
	)

	if !role.Allowed("configs", "update", name) {
		err := weberror.NewWebError(nil, "updating config %s not allowed for %s", name, ctx.Value("user").(string))
		return err.SetStatus(http.StatusForbidden)
	}

	var (
		typ = r.Header.Get("Content-Type")
		c   *store.Config
	)

	switch {
	case typ == "application/json": // default to JSON if not set
		body, err := io.ReadAll(r.Body)
		if err != nil {
			err := weberror.NewWebError(err, "unable to parse request")
			return err.SetStatus(http.StatusInternalServerError)
		}

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

		c, err = store.NewConfigFromYAML(body)
		if err != nil {
			err := weberror.NewWebError(err, "unable to parse request")
			return err.SetStatus(http.StatusInternalServerError)
		}
	case strings.HasPrefix(typ, "multipart/form-data"): // file upload
		r.ParseMultipartForm(1 << 20) // max 1M file size

		file, handler, err := r.FormFile("fileupload") // assume `fileupload` key used for upload
		if err != nil {
			err := weberror.NewWebError(err, "unable to access uploaded file")
			return err.SetStatus(http.StatusInternalServerError)
		}

		defer file.Close()

		switch filepath.Ext(handler.Filename) {
		case ".json":
			body, err := io.ReadAll(file)
			if err != nil {
				err := weberror.NewWebError(err, "unable to parse uploaded file")
				return err.SetStatus(http.StatusInternalServerError)
			}

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

			c, err = store.NewConfigFromYAML(body)
			if err != nil {
				err := weberror.NewWebError(err, "unable to parse uploaded file")
				return err.SetStatus(http.StatusInternalServerError)
			}
		default:
			return weberror.NewWebError(nil, "unknown file extension for uploaded file: %s", handler.Filename)
		}
	default:
		return weberror.NewWebError(nil, "unknown content type provided when updating config: %s", typ)
	}

	if c.Kind == "Experiment" {
		// Reset experiment name in spec since we removed it before sending.
		c.Spec["experimentName"] = vars["name"]
	}

	if err := config.Update(name, c); err != nil {
		if errors.Is(err, store.ErrNotExist) {
			return weberror.NewWebError(err, "config to update (%s) does not exist", name)
		}

		if errors.Is(err, types.ErrValidationFailed) {
			cause := errors.Unwrap(err)
			lines := strings.Split(cause.Error(), "\n")

			return weberror.NewWebError(cause, lines[0]).WithMetadata("validation", cause.Error(), true)
		}

		if errors.Is(err, store.ErrInvalidFormat) {
			cause := errors.Unwrap(err)
			return weberror.NewWebError(cause, "invalid formatting").WithMetadata("validation", cause.Error(), true)
		}

		return weberror.NewWebError(err, "unable to update config %s", name)
	}

	if c.Kind == "Experiment" {
		if err := experiment.Reconfigure(c.Metadata.Name); err != nil {
			return weberror.NewWebError(err, "unable to reconfigure updated experiment %s", c.Metadata.Name)
		}
	}

	w.Header().Set("Location", strings.ToLower(fmt.Sprintf("/api/v1/configs/%s/%s", c.Kind, c.Metadata.Name)))
	w.WriteHeader(http.StatusNoContent)

	c.Spec = nil
	c.Status = nil

	body, err := json.Marshal(c)
	if err != nil {
		plog.Error("marshaling config", "config", c.FullName(), "err", err)
		return nil
	}

	broker.Broadcast(
		bt.NewRequestPolicy("configs", "list", c.FullName()),
		bt.NewResource("config", name, "update"), // use old name in broadcast so client knows what to update
		body,
	)

	return nil
}

// DELETE /configs/{kind}/{name}
func DeleteConfig(w http.ResponseWriter, r *http.Request) error {
	plog.Debug("HTTP handler called", "handler", "DeleteConfig")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
		vars = mux.Vars(r)
		name = store.ConfigFullName(vars["kind"], vars["name"])
	)

	if !role.Allowed("configs", "delete", name) {
		err := weberror.NewWebError(nil, "deleting config %s not allowed for %s", name, ctx.Value("user").(string))
		return err.SetStatus(http.StatusForbidden)
	}

	if err := config.Delete(name); err != nil {
		return weberror.NewWebError(err, "unable to update config %s", name)
	}

	w.WriteHeader(http.StatusNoContent)

	broker.Broadcast(
		bt.NewRequestPolicy("configs", "list", name),
		bt.NewResource("config", name, "delete"),
		nil,
	)

	return nil
}
