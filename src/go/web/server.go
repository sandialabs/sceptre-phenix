package web

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/gorilla/mux"

	"phenix/util/common"
	"phenix/util/plog"
	"phenix/web/broker"
	"phenix/web/forward"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/scorch"
	"phenix/web/weberror"
)

type route struct {
	path    string
	handler http.Handler
	methods []string
}

const (
	resourceUsers = "users"
	verbGet       = "get"
	verbPatch     = "patch"
)

var o serverOptions //nolint:gochecknoglobals // global options

// ConfigureUsers creates each missing default user (name:password:role[:resource...])
// with its role. It skips an entry without a role and joins the errors.
func ConfigureUsers(users []string) error {
	setUserRole := func(user *rbac.User, rname string, resources ...string) {
		if role, err := rbac.RoleFromConfig(rname); err == nil {
			_ = role.SetResourceNames(resources...)

			// allow user to get their own user details
			role.AddPolicy(
				[]string{resourceUsers},
				[]string{user.Username()},
				[]string{verbGet},
			)

			_ = user.SetRole(role)
		} else {
			plog.Error(
				plog.TypeSecurity,
				"getting role for user",
				"user",
				user.Username(),
				"role",
				rname,
				"err",
				err,
			)
		}
	}

	// Where an entry's password and role are, after its name.
	const (
		passwordField = 1
		roleField     = 2
	)

	var errs []error

	for position, u := range users {
		creds := strings.Split(u, ":")

		if len(creds) <= roleField {
			// Name the entry by position and by its name, never by a field
			// that may be a password.
			var name string
			if len(creds) > passwordField {
				name = creds[0]
			}

			plog.Error(plog.TypeSecurity, "skipping default user without a role", "position", position+1, "user", name)

			errs = append(errs, fmt.Errorf(
				"default user %d (%q) is not name:password:role[:resource...]", position+1, name,
			))

			continue
		}

		uname := creds[0]
		pword := creds[1]
		rname := creds[2]

		// User already exists.
		// Confirm existing user has specified role and update if necessary.
		if user, err := rbac.GetUser(uname); err == nil {
			if user.RoleName() != rname {
				plog.Info(
					plog.TypeSecurity,
					"updating role for existing user",
					"user",
					user.Username(),
					"old",
					user.RoleName(),
					"new",
					rname,
				)

				setUserRole(user, rname, creds[3:]...)
			}

			continue
		}

		// do not store a user whose role does not exist
		if _, err := rbac.RoleFromConfig(rname); err != nil {
			plog.Error(plog.TypeSecurity, "skipping default user with unknown role", "user", uname, "role", rname)

			continue
		}

		plog.Info(plog.TypeSecurity, "creating default user", "user", uname, "role", rname)

		user, err := rbac.NewUser(uname, pword, "", "")
		if errors.Is(err, rbac.ErrUserExists) {
			// Created since it was looked up, by another phenix sharing the
			// store, say. It is left as it is.
			plog.Info(plog.TypeSecurity, "default user already exists", "user", uname)

			continue
		} else if err != nil {
			plog.Error(plog.TypeSecurity, "creating default user", "user", uname, "err", err)

			errs = append(errs, err)

			continue
		}

		setUserRole(user, rname, creds[3:]...)
	}

	return errors.Join(errs...)
}

//nolint:funlen // server startup
func Start(opts ...ServerOption) error {
	o = newServerOptions(opts...)
	fileServerEndpoint, err := NormalizeFileServerEndpoint(o.fileServerEndpoint)
	if err != nil {
		return fmt.Errorf("invalid ui.file-server-endpoint: %w", err)
	}

	if fileServerEndpoint != "" {
		o.features["file-server"] = true
	}

	_ = ConfigureUsers(o.users)

	var assets http.FileSystem

	if o.unbundled {
		assets = http.Dir("web/public")

		plog.Info(plog.TypeSystem, "serving unbundled assets")
	} else {
		var err error
		assets, err = GetAssets()
		if err != nil {
			return err
		}
	}

	router := newRouter(assets)

	plog.Info(plog.TypeSystem, "starting websockets broker")

	go broker.Start()

	plog.Info(plog.TypeSystem, "starting scorch processors")

	go scorch.Start(o.basePath)

	plog.Info(plog.TypeSystem, "starting log publisher")

	go SyncMinimegaLogs(context.Background(), o.minimegaLogs)

	plog.Info(plog.TypeSystem, "using base path", "path", o.basePath)
	plog.Info(plog.TypeSystem, "using JWT lifetime", "lifetime", o.jwtLifetime)

	if fileServerEndpoint != "" {
		go StartFileServer(fileServerEndpoint, o.jwtKey, o.proxyAuthHeader)
	}

	if common.UnixSocket != "" {
		var (
			router = mux.NewRouter().StrictSlash(true)
			api    = router.PathPrefix("/api/v1").Subrouter()
		)

		addRoutesToRouter(api, socketRoutes()...)

		api.MethodNotAllowedHandler = http.HandlerFunc(methodNotAllowed)

		api.Use(middleware.NoAuth)
		api.Use(middleware.Freshness)
		api.Use(middleware.Version)

		_ = os.Remove(common.UnixSocket)

		plog.Info(plog.TypeSystem, "starting Unix socket server", "path", common.UnixSocket)

		server := http.Server{Handler: router} //nolint:gosec // Potential Slowloris Attack

		//nolint:exhaustruct // partial initialization
		listener, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", common.UnixSocket)
		if err != nil {
			return err
		}

		if o.unixSocketGID != -1 {
			plog.Info(
				plog.TypeSystem,
				"setting Unix socket group permissions",
				"gid",
				o.unixSocketGID,
			)

			err = os.Chown(common.UnixSocket, -1, o.unixSocketGID)
			if err != nil {
				return err
			}

			err := os.Chmod(common.UnixSocket, 0o770) //nolint:gosec // unix socket permissions
			if err != nil {
				return err
			}
		}

		go func() {
			err := server.Serve(listener)
			if err != nil {
				plog.Error(plog.TypeSystem, "serving Unix socket", "err", err)
			}
		}()
	}

	plog.Info(plog.TypeSecurity, "starting server", "tls_enabled", o.tlsEnabled())

	if o.tlsEnabled() {
		plog.Info(plog.TypeSystem, "starting HTTPS server", "endpoint", o.endpoint)

		//nolint:gosec // Use of net/http serve function that has no support for setting timeouts
		return http.ListenAndServeTLS(
			o.endpoint,
			o.tlsCrtPath,
			o.tlsKeyPath,
			router,
		)
	} else {
		plog.Info(plog.TypeSystem, "Starting HTTP server", "endpoint", o.endpoint)
		//nolint:gosec // Use of net/http serve function that has no support for setting timeouts
		return http.ListenAndServe(
			o.endpoint,
			router,
		)
	}
}

// newRouter routes the UI pages, the assets and the REST API, as the server options say.
func newRouter(assets http.FileSystem) *mux.Router { //nolint:funlen,maintidx // the route table
	router := mux.NewRouter().StrictSlash(true)

	if o.featured("tunneler-download") {
		plog.Info(plog.TypeSystem, "Serving phēnix tunneler downloads")
		router.Handle("/downloads/tunneler/{name}", weberror.ErrorHandler(forward.GetTunneler)).Methods("GET", "HEAD")
	}

	router.HandleFunc("/features", GetFeatures).Methods("GET")
	router.HandleFunc("/version", GetVersion).Methods("GET")

	plog.Info(plog.TypeSystem, "setting up assets")

	router.Handle(openAPIPath, OpenAPISpecHandler(assets)).Methods("GET", "HEAD")
	router.PathPrefix("/docs/").Handler(
		StaticHandler(assets, false),
	)

	router.PathPrefix("/novnc/").Handler(
		StaticHandler(assets, false),
	)

	router.PathPrefix("/xterm.js/").Handler(
		StaticHandler(assets, false),
	)

	router.PathPrefix("/assets/").Handler(
		StaticHandler(assets, true),
	)

	router.Handle("/favicon.ico", StaticHandler(assets, false))

	router.NotFoundHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		plog.Warn(
			plog.TypeSystem,
			"Unknown route requested",
			"route",
			r.RequestURI,
			"method",
			r.Method,
		)

		// index.html names the current build's hashed chunks, so browsers must
		// revalidate it rather than reuse a stale copy
		w.Header().Set("Cache-Control", "no-cache")

		if o.unbundled {
			http.ServeFile(w, r, "web/public/index.html")
		} else {
			f, err := assets.Open("index.html")
			if err != nil {
				plog.Error(plog.TypeSystem, "opening index.html from assets", "err", err)
				weberror.Error(w, "not found", http.StatusNotFound)
				return
			}
			defer f.Close()

			fi, err := f.Stat()
			if err != nil {
				plog.Error(plog.TypeSystem, "statting index.html from assets", "err", err)
				weberror.Error(w, "not found", http.StatusNotFound)
				return
			}

			http.ServeContent(w, r, "index.html", fi.ModTime(), f)
		}
	})

	api := router.PathPrefix("/api/v1").Subrouter()

	// The router's NotFoundHandler serves the SPA index, so an API client would get 200 HTML.
	// A route behind a disabled feature flag would then also look enabled.
	api.NotFoundHandler = apiNotFoundHandler()
	api.MethodNotAllowedHandler = http.HandlerFunc(methodNotAllowed)

	// API routes take OPTIONS, which AnswerOptions answers with 204
	api.Handle("/configs", weberror.ErrorHandler(GetConfigs)).Methods("GET", "OPTIONS")
	api.Handle("/configs", weberror.ErrorHandler(CreateConfig)).Methods("POST", "OPTIONS")
	api.Handle("/configs/{kind}/{name}", weberror.ErrorHandler(GetConfig)).Methods("GET", "OPTIONS")
	api.Handle("/configs/{kind}/{name}", weberror.ErrorHandler(UpdateConfig)).
		Methods("PUT", "OPTIONS")
	api.Handle("/configs/{kind}/{name}", weberror.ErrorHandler(DeleteConfig)).
		Methods("DELETE", "OPTIONS")
	api.Handle("/configs/download", weberror.ErrorHandler(DownloadConfigs)).
		Methods("POST", "OPTIONS")
	api.Handle("/schemas/{version}", weberror.ErrorHandler(GetSchemaSpec)).Methods("GET", "OPTIONS")
	api.Handle("/schemas/{kind}/{version}", weberror.ErrorHandler(GetSchema)).
		Methods("GET", "OPTIONS")

	api.Handle("/experiments", weberror.ErrorHandler(GetExperiments)).Methods("GET", "OPTIONS")
	api.Handle("/experiments", weberror.ErrorHandler(CreateExperiment)).Methods("POST", "OPTIONS")
	api.Handle("/experiments/{name}", weberror.ErrorHandler(GetExperiment)).
		Methods("GET", "OPTIONS")
	api.Handle("/experiments/{name}", weberror.ErrorHandler(UpdateExperiment)).
		Methods("PATCH", "OPTIONS")
	api.Handle("/experiments/{name}", weberror.ErrorHandler(DeleteExperiment)).Methods("DELETE", "OPTIONS")
	api.Handle("/experiments/{name}/apps", weberror.ErrorHandler(GetExperimentApps)).
		Methods("GET", "OPTIONS")
	api.Handle("/experiments/{name}/start", weberror.ErrorHandler(StartExperiment)).
		Methods("POST", "OPTIONS")
	api.Handle("/experiments/{name}/stop", weberror.ErrorHandler(StopExperiment)).
		Methods("POST", "OPTIONS")
	api.Handle("/experiments/{exp}/netflow", weberror.ErrorHandler(GetNetflow)).Methods("GET", "OPTIONS")
	api.Handle("/experiments/{exp}/netflow", weberror.ErrorHandler(StartNetflow)).Methods("POST", "OPTIONS")
	api.Handle("/experiments/{exp}/netflow", weberror.ErrorHandler(StopNetflow)).Methods("DELETE", "OPTIONS")
	api.Handle("/experiments/{exp}/netflow/ws", weberror.ErrorHandler(GetNetflowWebSocket)).Methods("GET", "OPTIONS")
	api.Handle("/experiments/{name}/topology", weberror.ErrorHandler(GetExperimentTopology)).Methods("GET", "OPTIONS")
	api.Handle("/experiments/{name}/topology/search", weberror.ErrorHandler(SearchExperimentTopology)).
		Methods("GET", "OPTIONS")
	api.Handle("/experiments/{name}/trigger", weberror.ErrorHandler(TriggerExperimentApps)).Methods("POST", "OPTIONS")
	api.Handle("/experiments/{name}/trigger", weberror.ErrorHandler(CancelTriggeredExperimentApps)).
		Methods("DELETE", "OPTIONS")
	api.Handle("/experiments/{name}/schedule", weberror.ErrorHandler(GetExperimentSchedule)).Methods("GET", "OPTIONS")
	api.Handle("/experiments/{name}/schedule", weberror.ErrorHandler(ScheduleExperiment)).Methods("POST", "OPTIONS")
	api.Handle("/experiments/{name}/captures", weberror.ErrorHandler(GetExperimentCaptures)).Methods("GET", "OPTIONS")
	api.Handle("/experiments/{exp}/captureSubnet", weberror.ErrorHandler(StartCaptureSubnet)).
		Methods("POST", "OPTIONS")
	api.Handle("/experiments/{exp}/stopCaptureSubnet", weberror.ErrorHandler(StopCaptureSubnet)).
		Methods("POST", "OPTIONS")
	api.Handle("/experiments/{name}/files", weberror.ErrorHandler(GetExperimentFiles)).Methods("GET", "OPTIONS")
	api.Handle("/experiments/{name}/files/{filename}", weberror.ErrorHandler(GetExperimentFile)).
		Methods("GET", "OPTIONS")
	api.Handle("/experiments/{name}/scorch/components/{run}/{loop}/{stage}/{cmp}", weberror.ErrorHandler(scorch.GetComponentOutput)).
		Methods("GET", "OPTIONS")
	api.Handle("/experiments/{name}/scorch/components/{run}/{loop}/{stage}/{cmp}/ws", weberror.ErrorHandler(scorch.StreamComponentOutput)).
		Methods("GET", "OPTIONS")
	api.Handle("/experiments/{name}/scorch/pipelines", weberror.ErrorHandler(scorch.GetPipelines)).
		Methods("GET", "OPTIONS")
	api.Handle("/experiments/{name}/scorch/pipelines/{run}/{loop}", weberror.ErrorHandler(scorch.GetPipeline)).
		Methods("GET", "OPTIONS")
	api.Handle("/experiments/{name}/scorch/pipelines/{run}", weberror.ErrorHandler(scorch.StartPipeline)).
		Methods("POST", "OPTIONS")
	api.Handle("/experiments/{name}/scorch/pipelines/{run}", weberror.ErrorHandler(scorch.CancelPipeline)).
		Methods("DELETE", "OPTIONS")
	api.Handle("/experiments/{name}/scorch/terminals", weberror.ErrorHandler(scorch.GetTerminals)).
		Methods("GET", "OPTIONS")
	api.Handle("/experiments/{name}/scorch/terminals/{pid}", weberror.ErrorHandler(scorch.ConnectTerminal)).
		Methods("GET", "OPTIONS")
	api.Handle("/experiments/{name}/scorch/terminals/{pid}/exit/{id}", weberror.ErrorHandler(scorch.ExitTerminal)).
		Methods("POST", "OPTIONS")
	api.Handle("/experiments/{name}/scorch/terminals/{pid}/ws/{id}", weberror.ErrorHandler(scorch.StreamTerminal)).
		Methods("GET", "OPTIONS")
	api.Handle("/experiments/{name}/scorch/terminals/{run}/{loop}/{stage}/{cmp}", weberror.ErrorHandler(scorch.ConnectTerminal)).
		Methods("GET", "OPTIONS")
	api.Handle("/experiments/{name}/soh", weberror.ErrorHandler(GetExperimentSoH)).Methods("GET", "OPTIONS")
	api.Handle("/experiments/{exp}/vms", weberror.ErrorHandler(GetVMs)).Methods("GET", "OPTIONS")
	api.Handle("/experiments/{exp}/vms", weberror.ErrorHandler(UpdateVMs)).Methods("PATCH", "OPTIONS")
	api.Handle("/experiments/{exp}/vms/{name}", weberror.ErrorHandler(GetVM)).Methods("GET", "OPTIONS")
	api.Handle("/experiments/{exp}/vms/{name}", weberror.ErrorHandler(UpdateVM)).Methods("PATCH", "OPTIONS")
	api.Handle("/experiments/{exp}/vms/{name}", weberror.ErrorHandler(DeleteVM)).Methods("DELETE", "OPTIONS")
	api.Handle("/experiments/{exp}/vms/{name}/reset", weberror.ErrorHandler(ResetVM)).Methods("POST", "OPTIONS")
	api.Handle("/experiments/{exp}/vms/{name}/restart", weberror.ErrorHandler(RestartVM)).Methods("POST", "OPTIONS")
	api.Handle("/experiments/{exp}/vms/{name}/start", weberror.ErrorHandler(StartVM)).Methods("POST", "OPTIONS")
	api.Handle("/experiments/{exp}/vms/{name}/stop", weberror.ErrorHandler(StopVM)).Methods("POST", "OPTIONS")
	api.Handle("/experiments/{exp}/vms/{name}/shutdown", weberror.ErrorHandler(ShutdownVM)).Methods("POST", "OPTIONS")
	api.Handle("/experiments/{exp}/vms/{name}/redeploy", weberror.ErrorHandler(RedeployVM)).Methods("POST", "OPTIONS")
	api.Handle("/experiments/{exp}/vms/{name}/cdrom", weberror.ErrorHandler(ChangeOpticalDisc)).
		Methods("POST", "OPTIONS")
	api.Handle("/experiments/{exp}/vms/{name}/cdrom", weberror.ErrorHandler(EjectOpticalDisc)).
		Methods("DELETE", "OPTIONS")
	api.Handle("/experiments/{exp}/vms/{name}/screenshot.png", weberror.ErrorHandler(GetScreenshot)).
		Methods("GET", "OPTIONS")
	api.Handle("/experiments/{exp}/vms/{name}/vnc", weberror.ErrorHandler(GetVNC)).Methods("GET", "OPTIONS")
	api.Handle("/experiments/{exp}/vms/{name}/vnc/ws", weberror.ErrorHandler(GetVNCWebSocket)).
		Methods("GET", "OPTIONS")
	api.Handle("/experiments/{exp}/vms/{name}/captures", weberror.ErrorHandler(GetVMCaptures)).
		Methods("GET", "OPTIONS")
	api.Handle("/experiments/{exp}/vms/{name}/captures", weberror.ErrorHandler(StartVMCapture)).
		Methods("POST", "OPTIONS")
	api.Handle("/experiments/{exp}/vms/{name}/captures", weberror.ErrorHandler(StopVMCaptures)).
		Methods("DELETE", "OPTIONS")
	api.Handle("/experiments/{exp}/vms/{name}/snapshots", weberror.ErrorHandler(GetVMSnapshots)).
		Methods("GET", "OPTIONS")
	api.Handle("/experiments/{exp}/vms/{name}/snapshots", weberror.ErrorHandler(SnapshotVM)).Methods("POST", "OPTIONS")
	api.Handle("/experiments/{exp}/vms/{name}/snapshots/{snapshot}", weberror.ErrorHandler(RestoreVM)).
		Methods("POST", "OPTIONS")
	api.Handle("/experiments/{exp}/vms/{name}/commit", weberror.ErrorHandler(CommitVM)).Methods("POST", "OPTIONS")
	api.Handle("/experiments/{exp}/vms/{name}/memorySnapshot", weberror.ErrorHandler(CreateVMMemorySnapshot)).
		Methods("POST", "OPTIONS")

	api.Handle("/experiments/{exp}/vms/{name}/forwards", weberror.ErrorHandler(forward.GetPortForwards)).
		Methods("GET", "OPTIONS")
	api.Handle("/experiments/{exp}/vms/{name}/forwards", weberror.ErrorHandler(forward.CreatePortForward)).
		Methods("POST", "OPTIONS")
	api.Handle("/experiments/{exp}/vms/{name}/forwards", weberror.ErrorHandler(forward.DeletePortForward)).
		Methods("DELETE", "OPTIONS")
	api.Handle("/experiments/{exp}/vms/{name}/forwards/{host}/{port}/ws", weberror.ErrorHandler(forward.GetPortForwardWebSocket)).
		Methods("GET", "OPTIONS")

	if o.featured("vm-mount") {
		api.Handle("/experiments/{exp}/vms/{name}/mount", weberror.ErrorHandler(MountVM)).Methods("POST", "OPTIONS")
		api.Handle("/experiments/{exp}/vms/{name}/unmount", weberror.ErrorHandler(UnmountVM)).
			Methods("DELETE", "OPTIONS")
		api.Handle("/experiments/{exp}/vms/{name}/files", weberror.ErrorHandler(GetMountFiles)).
			Methods("GET", "OPTIONS").
			Queries("path", "{path}")
		api.Handle("/experiments/{exp}/vms/{name}/files/download", weberror.ErrorHandler(DownloadMountFile)).
			Methods("GET", "HEAD", "OPTIONS").
			Queries("path", "{path}")
		api.Handle("/experiments/{exp}/vms/{name}/files/upload", weberror.ErrorHandler(UploadMountFile)).
			Methods("PUT", "OPTIONS").
			Queries("path", "{path}")
		api.Handle("/experiments/{exp}/vms/{name}/files/copy", weberror.ErrorHandler(CopyExperimentFileToMount)).
			Methods("POST", "OPTIONS")
	}

	api.Handle("/disks", weberror.ErrorHandler(GetDisks)).Methods("GET", "OPTIONS")
	api.Handle("/disks/snapshot", weberror.ErrorHandler(SnapshotDisk)).
		Methods("POST", "OPTIONS").
		Queries("disk", "{disk}", "new", "{new}")
	api.Handle("/disks/rebase", weberror.ErrorHandler(RebaseDisk)).
		Methods("POST", "OPTIONS").
		Queries("disk", "{disk}", "backing", "{backing}", "unsafe", "{unsafe}")
	api.Handle("/disks/resize", weberror.ErrorHandler(ResizeDisk)).
		Methods("POST", "OPTIONS").
		Queries("disk", "{disk}", "size", "{size}")
	api.Handle("/disks/commit", weberror.ErrorHandler(CommitDisk)).Methods("POST", "OPTIONS").Queries("disk", "{disk}")
	api.Handle("/disks/clone", weberror.ErrorHandler(CloneDisk)).
		Methods("POST", "OPTIONS").
		Queries("disk", "{disk}", "new", "{new}")
	api.Handle("/disks", weberror.ErrorHandler(DeleteDisk)).Methods("DELETE", "OPTIONS").Queries("disk", "{disk}")
	api.Handle("/disks/rename", weberror.ErrorHandler(RenameDisk)).
		Methods("POST", "OPTIONS").
		Queries("disk", "{disk}", "new", "{new}")
	api.Handle("/disks", weberror.ErrorHandler(UploadDisk)).Methods("POST", "OPTIONS")
	api.Handle("/disks/download", weberror.ErrorHandler(DownloadDisk)).
		Methods("GET", "HEAD", "OPTIONS").
		Queries("disk", "{disk}")

	api.Handle("/vms", weberror.ErrorHandler(GetAllVMs)).Methods("GET", "OPTIONS")
	api.Handle("/applications", weberror.ErrorHandler(GetApplications)).Methods("GET", "OPTIONS")
	api.Handle("/topologies", weberror.ErrorHandler(GetTopologies)).Methods("GET", "OPTIONS")
	api.Handle("/topologies/{topo}/scenarios", weberror.ErrorHandler(GetScenarios)).Methods("GET", "OPTIONS")
	api.Handle("/hosts", weberror.ErrorHandler(GetClusterHosts)).Methods("GET", "OPTIONS")
	api.Handle("/users", weberror.ErrorHandler(GetUsers)).Methods("GET", "OPTIONS")
	api.Handle("/users", weberror.ErrorHandler(CreateUser)).Methods("POST", "OPTIONS")
	api.Handle("/users/{username}", weberror.ErrorHandler(GetUser)).Methods("GET", "OPTIONS")
	api.Handle("/users/{username}", weberror.ErrorHandler(UpdateUser)).Methods("PATCH", "OPTIONS")
	api.Handle("/users/{username}", weberror.ErrorHandler(DeleteUser)).Methods("DELETE", "OPTIONS")
	api.Handle("/users/{username}/tokens", weberror.ErrorHandler(CreateUserToken)).Methods("POST", "OPTIONS")
	api.Handle("/roles", weberror.ErrorHandler(GetRoles)).Methods("GET", "OPTIONS")
	api.Handle("/signup", weberror.ErrorHandler(Signup)).Methods("POST", "OPTIONS")
	api.Handle("/login", weberror.ErrorHandler(Login)).Methods("GET", "POST", "OPTIONS")
	api.Handle("/logout", weberror.ErrorHandler(Logout)).Methods("POST", "OPTIONS")
	api.Handle("/logs", weberror.ErrorHandler(GetLogs)).Methods("GET", "OPTIONS")
	api.HandleFunc("/ws", broker.ServeWS).Methods("GET")
	api.Handle("/console", weberror.ErrorHandler(CreateConsole)).Methods("POST", "OPTIONS")
	api.Handle("/console/{pid}/ws", weberror.ErrorHandler(WsConsole)).Methods("GET", "OPTIONS")
	api.Handle("/console/{pid}/size", weberror.ErrorHandler(ResizeConsole)).
		Methods("POST", "OPTIONS").
		Queries("cols", "{cols:[0-9]+}", "rows", "{rows:[0-9]+}")

	api.Handle("/settings", weberror.ErrorHandler(GetSettings)).Methods("GET", "OPTIONS")
	api.Handle("/settings", weberror.ErrorHandler(SetSettings)).Methods("POST", "OPTIONS")
	api.Handle("/settings/password", weberror.ErrorHandler(GetPasswordRequirements)).Methods("GET", "OPTIONS")
	api.Handle("/settings/timeout", weberror.ErrorHandler(GetTimeoutSettings)).Methods("GET", "OPTIONS")

	// with OPTIONS, as every API route takes it
	for _, r := range socketRoutes() {
		api.Handle(r.path, r.handler).Methods(append(r.methods, "OPTIONS")...)
	}

	// outermost, so the other middleware (full logging in particular) sees the
	// uncompressed response
	api.Use(CompressResponses)

	api.Use(middleware.AnswerOptions)

	api.Use(middleware.Auth(o.jwtKey, o.proxyAuthHeader))
	// after Auth, so a refused request takes no sequence number
	api.Use(middleware.Freshness)
	api.Use(middleware.Version)

	switch o.logMiddleware {
	case "full":
		plog.Info(plog.TypeSystem, "full HTTP logging is enabled")
		api.Use(middleware.LogFull)
	case "requests":
		plog.Info(plog.TypeSystem, "requests-only HTTP logging is enabled")
		api.Use(middleware.LogRequests)
	}

	return router
}

// apiLocation returns the path of the API resource p (relative to /api/v1/) behind the
// UI base path o.basePath, which it makes start and end with "/".
func apiLocation(p string) string {
	base := o.basePath
	if !strings.HasPrefix(base, "/") {
		base = "/" + base
	}

	if !strings.HasSuffix(base, "/") {
		base += "/"
	}

	return base + "api/v1/" + p
}

// socketRoutes are the API routes the Unix socket serves as well.
func socketRoutes() []route {
	return []route{
		{"/workflow/apply/{branch}", weberror.ErrorHandler(ApplyWorkflow), []string{"POST"}},
		{
			"/workflow/configs/{branch}",
			weberror.ErrorHandler(WorkflowUpsertConfig),
			[]string{"POST"},
		},
		{"/options", weberror.ErrorHandler(GetOptions), []string{"GET"}},
	}
}

// methodNotAllowed answers a request for an API path that does not take its
// method, with the JSON Error rather than gorilla's empty 405.
func methodNotAllowed(w http.ResponseWriter, _ *http.Request) {
	weberror.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

func addRoutesToRouter(router *mux.Router, routes ...route) {
	for _, r := range routes {
		router.Handle(r.path, r.handler).Methods(r.methods...)
	}
}

// apiNotFoundHandler answers 404 to an API request that matches no route, and never serves the SPA index.
// Router middleware, including authentication, does not run here, so it must disclose nothing the path does not.
func apiNotFoundHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		plog.Warn(
			plog.TypeSystem,
			"Unknown API route requested",
			"route",
			r.URL.Path,
			"method",
			r.Method,
		)

		weberror.Error(w, "no API route matches this request", http.StatusNotFound)
	})
}
