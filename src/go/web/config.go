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
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"gopkg.in/yaml.v3"

	"phenix/api/config"
	"phenix/api/experiment"
	"phenix/store"
	"phenix/types"
	"phenix/types/version"
	"phenix/util/freshness"
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

	// an empty list, never null
	allowed := []store.Config{}

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

		cfg, err := config.Get(name)
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
		w.Header().Set("Content-Disposition", util.Attachment(fn))
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

	// Every config is read before the archive starts, so a failure answers
	// with an error rather than a broken archive, and the headers go out
	// before the zip writer's buffer first fills.
	type entry struct {
		name string
		body []byte
	}

	entries := make([]entry, 0, len(configs))

	for _, name := range configs {
		if !role.Allowed("configs", "get", name) {
			continue
		}

		cfg, err := config.Get(name)
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

		entries = append(entries, entry{name: fmt.Sprintf("%s-%s.yml", cfg.Kind, cfg.Metadata.Name), body: body})
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", util.Attachment("configs.zip"))
	// built as it is sent, so it cannot be resumed or cached
	w.Header().Set("Accept-Ranges", "none")
	w.Header().Set("Cache-Control", "no-store")

	zipper := zip.NewWriter(w)

	for _, e := range entries {
		zf, err := zipper.Create(e.name)
		if err != nil {
			plog.Error(plog.TypeSystem, "creating zip file entry", "file", e.name, "err", err)

			continue
		}

		if _, err := zf.Write(e.body); err != nil {
			plog.Error(plog.TypeSystem, "writing config to zip file", "file", e.name, "err", err)

			continue
		}
	}

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
		typ  = r.Header.Get("Content-Type")
		opts = []config.CreateOption{config.CreateWithValidation()}
		src  []byte
	)

	switch {
	case typ == mimeJSON: // default to JSON if not set
		body, err := io.ReadAll(r.Body)
		if err != nil {
			err := weberror.NewWebError(err, "unable to parse request")

			return err.SetStatus(http.StatusInternalServerError)
		}

		src = body
		opts = append(opts, config.CreateFromJSON(body))
	case typ == mimeYAML:
		body, err := io.ReadAll(r.Body)
		if err != nil {
			err := weberror.NewWebError(err, "unable to parse request")

			return err.SetStatus(http.StatusInternalServerError)
		}

		src = body
		opts = append(opts, config.CreateFromYAML(body))
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
			opts = append(opts, config.CreateFromJSON(body))
		case ".yaml", ".yml":
			body, err := io.ReadAll(file)
			if err != nil {
				err := weberror.NewWebError(err, "unable to parse uploaded file")

				return err.SetStatus(http.StatusInternalServerError)
			}

			src = body
			opts = append(opts, config.CreateFromYAML(body))
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

	c, err := config.Create(opts...)
	if err != nil {
		if errors.Is(err, store.ErrExist) {
			return weberror.NewWebError(err, "config with same name already exists")
		}

		if errors.Is(err, types.ErrValidationFailed) {
			return validationWebError(src, err)
		}

		if errors.Is(err, store.ErrInvalidFormat) {
			cause := errors.Unwrap(err)

			return weberror.NewWebError(cause, "invalid formatting").
				WithMetadata("validation", cause.Error(), true)
		}

		if errors.Is(err, version.ErrInvalidKind) {
			return weberror.NewWebError(err, "unknown config kind provided")
		}

		return weberror.NewWebError(err, "unable to create new config")
	}

	w.Header().Set("Location", apiLocation(strings.ToLower(fmt.Sprintf("configs/%s/%s", c.Kind, c.Metadata.Name))))
	setRevisionHeaders(w, c.Metadata.Revision)
	w.WriteHeader(http.StatusCreated)

	c.Spec = nil
	c.Status = nil

	body, err := json.Marshal(c)
	if err != nil {
		plog.Error(plog.TypeSystem, "marshaling config", "config", c.FullName(), "err", err)

		return nil
	}

	broker.Broadcast(
		bt.NewRequestPolicy("configs", "list", c.FullName()),
		bt.NewResource("config", c.FullName(), "create"),
		body,
	)

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

	cfg, err := config.Get(name)
	if err != nil {
		werr := weberror.NewWebError(err, "unable to get config %s from store", name)
		if errors.Is(err, store.ErrNotExist) {
			return werr.SetStatus(http.StatusNotFound)
		}

		return werr
	}

	if cfg.Kind == kindExperiment {
		// Clear experiment name... not applicable to end users.
		delete(cfg.Spec, "experimentName")
	}

	setRevisionHeaders(w, cfg.Metadata.Revision)

	if inm := r.Header.Get("If-None-Match"); inm != "" && cfg.Metadata.Revision > 0 &&
		freshness.MatchRevision(inm, cfg.Metadata.Revision) {
		w.WriteHeader(http.StatusNotModified)

		return nil
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

	ifRevision, err := ifMatchRevision(r, name)
	if err != nil {
		return err
	}

	var (
		typ = r.Header.Get("Content-Type")
		c   *store.Config
		src []byte
	)

	switch {
	case typ == "application/json": // default to JSON if not set
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

	if c.Kind == kindExperiment {
		// Reset experiment name in spec since we removed it before sending.
		c.Spec["experimentName"] = vars["name"]
	}

	c.IfRevision = ifRevision

	if err := config.Update(name, c); err != nil {
		return updateConfigWebError(err, name, src)
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

	if c.Kind == kindExperiment {
		// reconfiguring wrote the experiment again
		c.Metadata.Revision = storedRevision(c.FullName())
	}

	w.Header().Set("Location", apiLocation(strings.ToLower(fmt.Sprintf("configs/%s/%s", c.Kind, c.Metadata.Name))))
	setRevisionHeaders(w, c.Metadata.Revision)
	w.WriteHeader(http.StatusNoContent)

	c.Spec = nil
	c.Status = nil

	body, err := json.Marshal(c)
	if err != nil {
		plog.Error(plog.TypeSystem, "marshaling config", "config", c.FullName(), "err", err)

		return nil
	}

	broker.Broadcast(
		bt.NewRequestPolicy("configs", "list", c.FullName()),
		bt.NewResource(
			"config",
			name,
			"update",
		), // use old name in broadcast so client knows what to update
		body,
	)
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

	ifRevision, err := ifMatchRevision(r, name)
	if err != nil {
		return err
	}

	err = config.DeleteAtRevision(name, ifRevision)
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			return conflictWebError(err, name)
		}

		if errors.Is(err, store.ErrNotExist) {
			return weberror.NewWebError(err, "config %s does not exist", name).SetStatus(http.StatusNotFound)
		}

		return weberror.NewWebError(err, "unable to update config %s", name)
	}

	w.WriteHeader(http.StatusNoContent)

	broker.Broadcast(
		bt.NewRequestPolicy("configs", "list", name),
		bt.NewResource("config", name, "delete"),
		nil,
	)
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

// updateConfigWebError is the answer to a config update the store or the
// config API refused. src is the document the client sent.
func updateConfigWebError(err error, name string, src []byte) error {
	switch {
	case errors.Is(err, store.ErrConflict):
		return conflictWebError(err, name)
	case errors.Is(err, store.ErrNotExist):
		return weberror.NewWebError(err, "config to update (%s) does not exist", name).SetStatus(http.StatusNotFound)
	case errors.Is(err, types.ErrValidationFailed):
		return validationWebError(src, err)
	case errors.Is(err, store.ErrInvalidFormat):
		cause := errors.Unwrap(err)

		return weberror.NewWebError(cause, "invalid formatting").
			WithMetadata("validation", cause.Error(), true)
	default:
		return weberror.NewWebError(err, "unable to update config %s", name)
	}
}

// setRevisionHeaders names a config revision in the ETag and
// X-Phenix-Revision headers; revision 0 (a config not written since revisions
// were added) sets neither.
func setRevisionHeaders(w http.ResponseWriter, revision uint64) {
	if revision == 0 {
		return
	}

	w.Header().Set("ETag", freshness.ETag(revision))
	w.Header().Set(freshness.HeaderRevision, strconv.FormatUint(revision, 10))
}

// ifMatchRevision reads a write's If-Match header. It returns 0 when there is
// none, and otherwise the revision the store must still hold for the write to
// run: the current one, if the header matches it. A malformed header is a 400
// and one that does not match a 412.
func ifMatchRevision(r *http.Request, name string) (uint64, error) {
	ifMatch := r.Header.Get("If-Match")
	if ifMatch == "" {
		return 0, nil
	}

	if _, _, err := freshness.ParseIfMatch(ifMatch); err != nil {
		err := weberror.NewWebError(err, "invalid If-Match header %q", ifMatch)

		return 0, err.SetStatus(http.StatusBadRequest)
	}

	current, err := store.NewConfig(name)
	if err != nil {
		return 0, weberror.NewWebError(err, "invalid config name %s", name)
	}

	if err := store.Get(current); err != nil {
		// a missing config fails If-Match (RFC 9110, 13.1.1)
		if errors.Is(err, store.ErrNotExist) {
			err := weberror.NewWebError(err, "config %s does not exist", name)

			return 0, err.SetStatus(http.StatusPreconditionFailed)
		}

		return 0, weberror.NewWebError(err, "unable to get config %s from store", name)
	}

	if !freshness.MatchRevision(ifMatch, current.Metadata.Revision) {
		return 0, conflictWebError(
			fmt.Errorf("%w: at revision %d", store.ErrConflict, current.Metadata.Revision),
			name,
		)
	}

	return current.Metadata.Revision, nil
}

// conflictWebError is the 412 for a write refused because the config changed
// since the client read it. metadata.revision holds the current revision.
func conflictWebError(err error, name string) error {
	werr := weberror.NewWebError(err, "config %s changed since it was read", name).
		SetStatus(http.StatusPreconditionFailed)

	if revision := storedRevision(name); revision > 0 {
		werr = werr.WithMetadata("revision", strconv.FormatUint(revision, 10), true)
	}

	return werr
}

// storedRevision returns the named config's revision in the store, or 0 when
// it cannot be read.
func storedRevision(name string) uint64 {
	c, err := store.NewConfig(name)
	if err != nil {
		return 0
	}

	if err := store.Get(c); err != nil {
		return 0
	}

	return c.Metadata.Revision
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
