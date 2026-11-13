package web

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
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
	"phenix/web/audit"
	"phenix/web/broker"
	bt "phenix/web/broker/brokertypes"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/util"
	"phenix/web/weberror"
)

const (
	kindExperiment    = "Experiment"
	kindUser          = "User"
	mimeMultipartForm = "multipart/form-data"
)
const MaxUploadSize = 1 << 20

// configListKinds are the kind values of GET /configs, in lower case.
var configListKinds = []string{ //nolint:gochecknoglobals // constant list
	"all", "topology", "scenario", "experiment", "image", "user", "role",
}

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

	if !query.Has("kind") {
		kind = "all"
	}

	// the kinds in any letter case, as config.List reads them
	if err := checkEnum("kind", strings.ToLower(kind), configListKinds...); err != nil {
		return err
	}

	configs, err := config.List(kind)
	if errors.Is(err, config.ErrUnknownKind) {
		return weberror.NewWebError(err, "unknown config kind %s", kind)
	}

	if err != nil {
		return weberror.NewWebError(err, "unable to get configs from store").SetStatus(http.StatusInternalServerError)
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

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body)

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

		redactConfig(cfg)

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

		redactConfig(cfg)

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

// userSecretFields are the User config fields holding credentials: the
// password hash and the user's API tokens.
func userSecretFields() []string {
	return []string{"password", "tokens"}
}

// redactConfig removes from a config the configs API returns an experiment's
// name in its spec and a user's credentials, leaving the stored config as is.
func redactConfig(cfg *store.Config) {
	var drop []string

	switch {
	case cfg.Kind == kindExperiment:
		drop = []string{"experimentName"}
	case strings.EqualFold(cfg.Kind, kindUser):
		drop = userSecretFields()
	default:
		return
	}

	cfg.Spec = maps.Clone(cfg.Spec)
	for _, field := range drop {
		delete(cfg.Spec, field)
	}
}

// keepUserSecrets gives an update of the User config name the stored password
// and tokens in place of any it carries, since the configs API never returns
// them; the users routes change them. Without an If-Match revision, the update
// must still find the revision read here, so a concurrent change is not lost.
func keepUserSecrets(name string, c *store.Config) error {
	if !strings.EqualFold(c.Kind, kindUser) {
		return nil
	}

	old, err := store.NewConfig(name)
	if err != nil {
		return nil //nolint:nilerr // no stored config of that name to keep from
	}

	if err := store.Get(old); err != nil {
		if errors.Is(err, store.ErrNotExist) {
			return nil
		}

		return fmt.Errorf("getting config %s: %w", name, err)
	}

	if !strings.EqualFold(old.Kind, kindUser) {
		return nil
	}

	if c.Spec == nil {
		c.Spec = make(map[string]any)
	}

	for _, field := range userSecretFields() {
		if v, ok := old.Spec[field]; ok {
			c.Spec[field] = v
		} else {
			delete(c.Spec, field)
		}
	}

	if c.IfRevision == 0 {
		c.IfRevision = old.Metadata.Revision
	}

	return nil
}

// configPermName returns the Kind/name config permissions are checked against,
// or, for an unknown kind, the name as given, which kind-scoped roles do not match.
func configPermName(c *store.Config) string {
	if name := store.ConfigFullName(c.Kind, c.Metadata.Name); name != "" {
		return name
	}

	return c.FullName()
}

// authorizeConfigCreate checks that the requester may create a config of c's
// kind and name, so a role limited to some kinds cannot create User or Role
// configs and grant itself more access.
func authorizeConfigCreate(r *http.Request, c *store.Config) error {
	var (
		role = middleware.RoleFromContext(r.Context())
		name = configPermName(c)
	)

	if role.Allowed("configs", "create", name) {
		return nil
	}

	user := middleware.UserFromContext(r.Context())
	audit.Denied(r, audit.ConfigCreate, "creating config not allowed", audit.Fields{Config: name})

	return weberror.NewWebError(nil, "creating config %s not allowed for %s", name, user).
		SetStatus(http.StatusForbidden)
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
		audit.Denied(r, audit.ConfigCreate, "creating config not allowed", audit.Fields{})
		err := weberror.NewWebError(
			nil,
			"creating configs not allowed for %s",
			user,
		)

		return err.SetStatus(http.StatusForbidden)
	}

	typ, err := configMediaType(r, "creating")
	if err != nil {
		return err
	}

	var (
		body  []byte
		parse func([]byte) (*store.Config, error)
	)

	switch typ {
	case mimeJSON:
		body, err = io.ReadAll(r.Body)
		if err != nil {
			err := weberror.NewWebError(err, "unable to parse request")

			return err.SetStatus(http.StatusInternalServerError)
		}

		parse = store.DecodeConfigJSON
	case mimeYAML:
		body, err = io.ReadAll(r.Body)
		if err != nil {
			err := weberror.NewWebError(err, "unable to parse request")

			return err.SetStatus(http.StatusInternalServerError)
		}

		parse = store.DecodeConfigYAML
	default: // multipart/form-data, a file upload
		file, handler, err := uploadedConfigFile(r)
		if err != nil {
			return err
		}

		defer func() { _ = file.Close() }()

		switch filepath.Ext(handler.Filename) {
		case ".json":
			body, err = io.ReadAll(file)
			if err != nil {
				err := weberror.NewWebError(err, "unable to parse uploaded file")

				return err.SetStatus(http.StatusInternalServerError)
			}

			parse = store.DecodeConfigJSON
		case ".yaml", ".yml":
			body, err = io.ReadAll(file)
			if err != nil {
				err := weberror.NewWebError(err, "unable to parse uploaded file")

				return err.SetStatus(http.StatusInternalServerError)
			}

			parse = store.DecodeConfigYAML
		default:
			return weberror.NewWebError(
				nil,
				"unknown file extension for uploaded file: %s",
				handler.Filename,
			)
		}
	}

	// Only ${BRANCH_NAME} expands, so a client cannot read the server's
	// environment through a stored config.
	c, err := parse(store.ExpandBranchName(body, os.LookupEnv))
	if err != nil {
		audit.Failure(r, audit.ConfigCreate, "creating config failed", audit.Fields{}, err)

		return weberror.NewWebError(err, "invalid formatting").
			WithMetadata("validation", err.Error(), true)
	}

	// The check above allows creating some config. This one checks the config
	// the body names, so a role scoped to some kinds or names creates no other.
	name := configPermName(c)

	c.FilesRoot = experiment.BaseDirRoot()

	c, err = config.Create(
		config.CreateFromConfig(c),
		config.CreateWithCheck(func(c *store.Config) error { return authorizeConfigCreate(r, c) }),
		config.CreateWithValidation(),
	)
	if err != nil {
		var werr *weberror.WebError
		if errors.As(err, &werr) {
			// a refusal, audited by authorizeConfigCreate
			return werr
		}

		audit.Failure(r, audit.ConfigCreate, "creating config failed", audit.Fields{Config: name}, err)

		if errors.Is(err, store.ErrExist) {
			return weberror.NewWebError(err, "config with same name already exists").SetStatus(http.StatusConflict)
		}

		if werr := baseDirWebError(err); werr != nil {
			return werr
		}

		if errors.Is(err, types.ErrValidationFailed) {
			return validationWebError(body, err)
		}

		if errors.Is(err, version.ErrInvalidKind) {
			return weberror.NewWebError(err, "unknown config kind provided")
		}

		return weberror.NewWebError(err, "unable to create new config")
	}

	audit.Success(r, audit.ConfigCreate, "created config", audit.Fields{Config: c.FullName()})

	w.Header().Set("Location", apiLocation(strings.ToLower(fmt.Sprintf("configs/%s/%s", c.Kind, c.Metadata.Name))))
	setRevisionHeaders(w, c.Metadata.Revision)
	w.WriteHeader(http.StatusCreated)

	if err := broadcastConfig(c, c.FullName(), "create"); err != nil {
		plog.Error(plog.TypeSystem, "marshaling config", "config", c.FullName(), "err", err)

		return nil
	}

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

	if name == "" {
		return unknownConfigKind(vars["kind"])
	}

	cfg, err := config.Get(name)
	if err != nil {
		werr := weberror.NewWebError(err, "unable to get config %s from store", name)
		if errors.Is(err, store.ErrNotExist) {
			return werr.SetStatus(http.StatusNotFound)
		}

		return werr
	}

	redactConfig(cfg)

	// the ETag names the stored revision, secrets included
	setRevisionHeaders(w, cfg.Metadata.Revision)

	if inm := r.Header.Get("If-None-Match"); inm != "" && cfg.Metadata.Revision > 0 &&
		freshness.MatchRevision(inm, cfg.Metadata.Revision) {
		w.WriteHeader(http.StatusNotModified)

		return nil
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

		w.Header().Set("Content-Type", mimeJSON)
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
			"unknown accept content type provided when getting config: %s",
			typ,
		).SetStatus(http.StatusNotAcceptable)
	}

	_, _ = w.Write(body)

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
		audit.Denied(r, audit.ConfigUpdate, "updating config not allowed", audit.Fields{Config: name})
		err := weberror.NewWebError(
			nil,
			"updating config %s not allowed for %s",
			name,
			user,
		)

		return err.SetStatus(http.StatusForbidden)
	}

	if name == "" {
		return unknownConfigKind(vars["kind"])
	}

	ifRevision, err := ifMatchRevision(r, name)
	if err != nil {
		return err
	}

	c, src, err := decodeUpdateConfigBody(r)
	if err != nil {
		return err
	}

	// The config is stored under the body's kind and name, which can differ from
	// the path's (a rename or another config), so it needs update and create too.
	if target := configPermName(c); target != name {
		if !role.Allowed("configs", "update", target) {
			user, _ := ctx.Value(middleware.ContextKeyUser).(string)
			audit.Denied(r, audit.ConfigUpdate, "updating config not allowed", audit.Fields{Config: target})

			return weberror.NewWebError(nil, "updating config %s not allowed for %s", target, user).
				SetStatus(http.StatusForbidden)
		}

		if err := authorizeConfigCreate(r, c); err != nil {
			return err
		}
	}

	// Reset experiment name in spec since we removed it before sending. A
	// config without a spec fails validation in config.Update.
	if c.Kind == kindExperiment && c.Spec != nil {
		c.Spec["experimentName"] = vars["name"]
	}

	c.IfRevision = ifRevision
	c.FilesRoot = experiment.BaseDirRoot()

	updated := audit.Fields{Config: name}
	if renamed := configPermName(c); renamed != name {
		updated.Extra = []any{"phenix.config.new_name", renamed}
	}

	if err := keepUserSecrets(name, c); err != nil {
		audit.Failure(r, audit.ConfigUpdate, "updating config failed", updated, err)

		return weberror.NewWebError(err, "unable to read config %s", name).
			SetStatus(http.StatusInternalServerError)
	}

	// A rename or kind change removes the old name's published Builder
	// documents, so it waits for a publication under way.
	if c.FullName() != name {
		defer lockBuilderPublishing(name)()
	}

	if err := config.Update(name, c); err != nil {
		audit.Failure(r, audit.ConfigUpdate, "updating config failed", updated, err)

		return updateConfigWebError(err, name, src)
	}

	if c.Kind == kindExperiment {
		err := experiment.Reconfigure(context.Background(), c.Metadata.Name)
		if err != nil {
			audit.Failure(r, audit.ConfigUpdate, "updating config failed", updated, err)

			return weberror.NewWebError(
				err,
				"unable to reconfigure updated experiment %s",
				c.Metadata.Name,
			)
		}
	}

	audit.Success(r, audit.ConfigUpdate, "updated config", updated)

	if c.Kind == kindExperiment {
		// reconfiguring wrote the experiment again
		c.Metadata.Revision = storedRevision(c.FullName())
	}

	w.Header().Set("Location", apiLocation(strings.ToLower(fmt.Sprintf("configs/%s/%s", c.Kind, c.Metadata.Name))))
	setRevisionHeaders(w, c.Metadata.Revision)
	w.WriteHeader(http.StatusNoContent)

	// The old name, so clients know which config to update.
	if err := broadcastConfig(c, name, "update"); err != nil {
		plog.Error(plog.TypeSystem, "marshaling config", "config", c.FullName(), "err", err)

		return nil
	}

	return nil
}

// decodeUpdateConfigBody reads and decodes the config in an UpdateConfig
// request body. It also returns the raw body.
func decodeUpdateConfigBody(r *http.Request) (*store.Config, []byte, error) {
	typ, err := configMediaType(r, "updating")
	if err != nil {
		return nil, nil, err
	}

	var (
		input io.Reader = r.Body
		msg             = "unable to parse request"
		parse func([]byte) (*store.Config, error)
	)

	switch typ {
	case mimeJSON:
		parse = store.DecodeConfigJSON
	case mimeYAML:
		parse = store.DecodeConfigYAML
	default: // multipart/form-data, a file upload
		file, handler, err := uploadedConfigFile(r)
		if err != nil {
			return nil, nil, err
		}

		defer func() { _ = file.Close() }()

		input, msg = file, "unable to parse uploaded file"

		switch filepath.Ext(handler.Filename) {
		case ".json":
			parse = store.DecodeConfigJSON
		case ".yaml", ".yml":
			parse = store.DecodeConfigYAML
		default:
			return nil, nil, weberror.NewWebError(
				nil,
				"unknown file extension for uploaded file: %s",
				handler.Filename,
			)
		}
	}

	src, err := io.ReadAll(input)
	if err != nil {
		return nil, nil, weberror.NewWebError(err, "%s", msg).SetStatus(http.StatusInternalServerError)
	}

	c, err := parse(store.ExpandBranchName(src, os.LookupEnv))
	if err != nil {
		return nil, nil, weberror.NewWebError(err, "%s", msg)
	}

	return c, src, nil
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
		audit.Denied(r, audit.ConfigDelete, "deleting config not allowed", audit.Fields{Config: name})
		err := weberror.NewWebError(
			nil,
			"deleting config %s not allowed for %s",
			name,
			user,
		)

		return err.SetStatus(http.StatusForbidden)
	}

	if name == "" {
		return unknownConfigKind(vars["kind"])
	}

	ifRevision, err := ifMatchRevision(r, name)
	if err != nil {
		return err
	}

	// Deleting a topology removes its published Builder documents, so it
	// waits for a publication under way.
	defer lockBuilderPublishing(name)()

	if err := deleteConfigAtRevision(name, ifRevision); err != nil {
		audit.Failure(r, audit.ConfigDelete, "deleting config failed", audit.Fields{Config: name}, err)

		if errors.Is(err, store.ErrConflict) {
			return conflictWebError(err, name)
		}

		if errors.Is(err, store.ErrNotExist) {
			return weberror.NewWebError(err, "config %s does not exist", name).SetStatus(http.StatusNotFound)
		}

		return weberror.NewWebError(err, "unable to update config %s", name)
	}

	audit.Success(r, audit.ConfigDelete, "deleted config", audit.Fields{Config: name})
	w.WriteHeader(http.StatusNoContent)

	return nil
}

// unknownConfigKind is the 404 for a config path whose kind phenix does not
// know: no config can exist there.
func unknownConfigKind(kind string) error {
	return weberror.NewWebError(nil, "unknown config kind %s", kind).SetStatus(http.StatusNotFound)
}

// configMediaType returns the media type of a config request body without its
// parameters: JSON when the request has no Content-Type. A type that the
// config routes do not take is a 415; action names the route in its message.
func configMediaType(r *http.Request, action string) (string, error) {
	typ := r.Header.Get("Content-Type")
	if typ == "" {
		return mimeJSON, nil
	}

	media, _, err := mime.ParseMediaType(typ)
	if err != nil || media != mimeJSON && media != mimeYAML && media != mimeMultipartForm {
		return "", weberror.NewWebError(nil, "unknown content type provided when %s config: %s", action, typ).
			SetStatus(http.StatusUnsupportedMediaType)
	}

	return media, nil
}

// uploadedConfigFile returns the fileupload file of a multipart config
// request. A body that is not a valid multipart form, or that has no
// fileupload file, is a 400. A failure to store the upload is a 500.
func uploadedConfigFile(r *http.Request) (multipart.File, *multipart.FileHeader, error) {
	err := r.ParseMultipartForm(MaxUploadSize)
	if err == nil {
		file, handler, ferr := r.FormFile("fileupload")
		if ferr == nil {
			return file, handler, nil
		}

		err = ferr
	}

	msg := "unable to access uploaded file"
	if errors.Is(err, http.ErrMissingFile) {
		msg = "the request has no fileupload file"
	}

	werr := weberror.NewWebError(err, "%s", msg)

	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		werr = werr.SetStatus(http.StatusInternalServerError)
	}

	return nil, nil, werr
}

// broadcastConfig sends c without its spec and status to everyone who may list
// it. name is the full name clients know, which after a rename is the old one.
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
	case errors.Is(err, experiment.ErrBaseDirOutside):
		return baseDirWebError(err)
	default:
		return weberror.NewWebError(err, "unable to update config %s", name)
	}
}

// baseDirWebError is the 400 for an experiment whose base directory the web API
// refuses (see experiment.ErrBaseDirOutside), or nil for any other error.
func baseDirWebError(err error) *weberror.WebError {
	if !errors.Is(err, experiment.ErrBaseDirOutside) {
		return nil
	}

	return weberror.NewWebError(
		err, "the experiment base directory (spec.baseDir) must be a folder inside %s", experiment.BaseDirRoot(),
	).WithCode(weberror.CodeInvalidRequest)
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
	werr := weberror.NewWebError(err, "config %s changed after you read it", name).
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

// deleteConfig deletes the config name through the config API, which runs
// the delete hooks of the kind. Then it tells everyone who may list the
// config that it is gone.
func deleteConfig(name string) error {
	return deleteConfigAtRevision(name, 0)
}

// deleteConfigAtRevision is [deleteConfig] that deletes nothing unless the
// config is at revision, when revision is not zero.
func deleteConfigAtRevision(name string, revision uint64) error {
	// Through the API, the delete of an experiment leaves a base directory
	// outside the experiments folder.
	if err := config.DeleteAtRevision(name, revision, config.DeleteWithFilesRoot(experiment.BaseDirRoot())); err != nil {
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
