package web

import (
	"net/http"

	"phenix/types/version"
	v1 "phenix/types/version/v1"
	v2 "phenix/types/version/v2"
	"phenix/util/plog"
	"phenix/web/rbac"
	"phenix/web/weberror"

	"github.com/gorilla/mux"
	jsoniter "github.com/json-iterator/go"
	"gopkg.in/yaml.v2"
)

var jsoner = jsoniter.ConfigCompatibleWithStandardLibrary

// GET /schemas/{version}
func GetSchemaSpec(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "GetSchemaSpec")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
		vars = mux.Vars(r)
		ver  = vars["version"]
	)

	if !role.Allowed("schemas", "get") {
		plog.Warn(plog.TypeSecurity, "getting schema spec not allowed", "user", ctx.Value("user").(string), "spec", ver)
		err := weberror.NewWebError(nil, "getting schema spec for %s not allowed for %s", ver, ctx.Value("user").(string))
		return err.SetStatus(http.StatusForbidden)
	}

	var spec map[string]interface{}

	switch ver {
	case "v1":
		if err := yaml.Unmarshal(v1.OpenAPI, &spec); err != nil {
			err := weberror.NewWebError(err, "unable to process %s spec", ver)
			return err.SetStatus(http.StatusInternalServerError)
		}
	case "v2":
		if err := yaml.Unmarshal(v2.OpenAPI, &spec); err != nil {
			err := weberror.NewWebError(err, "unable to process %s spec", ver)
			return err.SetStatus(http.StatusInternalServerError)
		}
	default:
		return weberror.NewWebError(nil, "unknown version %s", ver)
	}

	var body []byte

	switch typ := r.Header.Get("Accept"); typ {
	case "", "*/*", "application/json": // default to JSON if not set
		var err error

		body, err = jsoner.Marshal(spec)
		if err != nil {
			err := weberror.NewWebError(err, "unable to process %s spec", ver)
			return err.SetStatus(http.StatusInternalServerError)
		}

		w.Header().Set("Content-Type", "application/json")
	case "application/x-yaml":
		var err error

		body, err = yaml.Marshal(spec)
		if err != nil {
			err := weberror.NewWebError(err, "unable to process %s spec", ver)
			return err.SetStatus(http.StatusInternalServerError)
		}

		w.Header().Set("Content-Type", "application/x-yaml")
	default:
		return weberror.NewWebError(nil, "unknown accept content type provided when requesting spec: %s", typ)
	}

	w.Write(body)

	return nil
}

// GET /schemas/{kind}/{version}
func GetSchema(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "GetSchema")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
		vars = mux.Vars(r)
		kind = vars["kind"]
		ver  = vars["version"]
	)

	if !role.Allowed("schemas", "get", kind) {
		plog.Warn(plog.TypeSecurity, "getting schema not allowed", "user", ctx.Value("user").(string), "schema", kind)
		err := weberror.NewWebError(nil, "getting schema %s not allowed for %s", kind, ctx.Value("user").(string))
		return err.SetStatus(http.StatusForbidden)
	}

	schema, err := version.GetVersionedSchemaForKind(kind, ver)
	if err != nil {
		err := weberror.NewWebError(err, "unable to get version %s of schema for %s", ver, kind)
		return err.SetStatus(http.StatusInternalServerError)
	}

	var body []byte

	switch typ := r.Header.Get("Accept"); typ {
	case "", "*/*", "application/json": // default to JSON if not set
		var err error

		body, err = jsoner.Marshal(schema)
		if err != nil {
			err := weberror.NewWebError(err, "unable to process schema %s", kind)
			return err.SetStatus(http.StatusInternalServerError)
		}

		w.Header().Set("Content-Type", "application/json")
	case "application/x-yaml":
		var err error

		body, err = yaml.Marshal(schema)
		if err != nil {
			err := weberror.NewWebError(err, "unable to process schema %s", kind)
			return err.SetStatus(http.StatusInternalServerError)
		}

		w.Header().Set("Content-Type", "application/x-yaml")
	default:
		return weberror.NewWebError(nil, "unknown accept content type provided when requesting schema: %s", typ)
	}

	w.Write(body)

	return nil
}
