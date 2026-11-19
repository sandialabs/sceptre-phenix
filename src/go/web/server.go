package web

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"

	"phenix/util/common"
	"phenix/util/plog"
	"phenix/web/audit"
	"phenix/web/broker"
	"phenix/web/forward"
	"phenix/web/idempotency"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/routing"
	"phenix/web/scorch"
	"phenix/web/weberror"
)

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

	var errs []error

	for position, u := range users {
		entry, err := ParseDefaultUser(position+1, u)
		if err != nil {
			plog.Error(plog.TypeSecurity, "skipping default user without a role", "position", position+1, "user", entry.Name)

			errs = append(errs, err)

			continue
		}

		uname := entry.Name
		pword := entry.Password
		rname := entry.Role

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

				setUserRole(user, rname, entry.Resources...)
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

		setUserRole(user, rname, entry.Resources...)
	}

	return errors.Join(errs...)
}

// Start runs the UI server until ctx is done, then shuts it down within the
// shutdown grace period and returns nil. It returns early if a server fails.
// It opens its listeners before it changes users, experiments, jobs or
// capture spools, so that it stops first when its ports are in use.
func Start(ctx context.Context, opts ...ServerOption) error {
	o = newServerOptions(opts...)

	if o.shutdownGrace < 0 {
		return fmt.Errorf("invalid ui.shutdown-grace-period %s: it must not be negative", o.shutdownGrace)
	}

	if o.netflowMaxSize < 0 {
		return fmt.Errorf("invalid ui.netflow.max-size %d bytes: it must not be negative", o.netflowMaxSize)
	}

	if o.captureMaxSize < 0 {
		return fmt.Errorf("invalid ui.captures.max-size %d MiB: it must not be negative", o.captureMaxSize)
	}

	fileServerEndpoint, err := NormalizeFileServerEndpoint(o.fileServerEndpoint)
	if err != nil {
		return fmt.Errorf("invalid ui.file-server-endpoint: %w", err)
	}

	if fileServerEndpoint != "" {
		o.features[FeatureFileServer] = true
	}

	if err := o.checkFeatures(); err != nil {
		return err
	}

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

	// the uptime check of the router reads the start time
	markStarted()

	// background loops run until shutdown, not until ctx is done
	background, stopBackground := context.WithCancel(context.WithoutCancel(ctx))

	router, err := newRouter(assets, withBuilderSweep(background, builderSweepEvery))
	if err != nil {
		stopBackground()

		return err
	}

	server := &uiServer{grace: o.shutdownGrace, servers: nil, socket: "", stopBackground: stopBackground}

	if err := server.listen(ctx, fileServerEndpoint, router); err != nil {
		server.closeListeners()

		return err
	}

	o.startCaptureSupport(background)

	_ = ConfigureUsers(o.users)

	recoverExperiments()

	plog.Info(plog.TypeSystem, "starting websockets broker")

	go broker.Start(background)

	plog.Info(plog.TypeSystem, "starting background job registry")

	ConfigureJobs()

	removeStaleCaptureSpools()

	plog.Info(plog.TypeSystem, "starting scorch processors")

	scorch.Start(background, o.basePath)

	plog.Info(plog.TypeSystem, "starting log publisher")

	go SyncMinimegaLogs(background, o.minimegaLogs)

	plog.Info(plog.TypeSystem, "starting disk image watcher")

	WatchDisks(background)

	plog.Info(plog.TypeSystem, "using base path", "path", o.basePath)
	plog.Info(plog.TypeSystem, "using JWT lifetime", "lifetime", o.jwtLifetime)

	return server.run(ctx)
}

// listen opens the listeners of the file server, the main server and the Unix
// socket, as the options ask for them. It opens the main server before it
// removes an old socket file, so that it does not remove the socket of a
// phenix ui that runs on the same port.
func (s *uiServer) listen(ctx context.Context, fileServerEndpoint string, router http.Handler) error {
	lc := &net.ListenConfig{} //nolint:exhaustruct // defaults

	// a signal during startup shuts the server down once it runs
	ctx = context.WithoutCancel(ctx)

	if fileServerEndpoint != "" {
		plog.Info(plog.TypeSystem, "starting file server", "endpoint", fileServerEndpoint)

		ln, err := lc.Listen(ctx, "tcp", fileServerEndpoint)
		if err != nil {
			return fmt.Errorf("listening for the file server: %w", err)
		}

		s.servers = append(s.servers, servedHTTP{
			name:  "file server",
			srv:   newFileServer(fileServerEndpoint, o.jwtKey, o.proxyAuthHeader),
			ln:    ln,
			serve: (*http.Server).Serve,
		})
	}

	plog.Info(plog.TypeSecurity, "starting server", "tls_enabled", o.tlsEnabled())

	primary := servedHTTP{name: "HTTP server", srv: newHTTPServer(o.endpoint, router), ln: nil, serve: (*http.Server).Serve}

	if o.tlsEnabled() {
		plog.Info(plog.TypeSystem, "starting HTTPS server", "endpoint", o.endpoint)

		primary.name = "HTTPS server"
		primary.serve = func(srv *http.Server, ln net.Listener) error {
			return srv.ServeTLS(ln, o.tlsCrtPath, o.tlsKeyPath)
		}
	} else {
		plog.Info(plog.TypeSystem, "Starting HTTP server", "endpoint", o.endpoint)
	}

	// as ListenAndServe does
	addr := o.endpoint
	if addr == "" {
		addr = ":http"

		if o.tlsEnabled() {
			addr = ":https"
		}
	}

	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", addr, err)
	}

	primary.ln = ln
	s.servers = append(s.servers, primary)

	if common.UnixSocket != "" {
		socketRouter, err := newSocketRouter()
		if err != nil {
			return err
		}

		_ = os.Remove(common.UnixSocket)

		plog.Info(plog.TypeSystem, "starting Unix socket server", "path", common.UnixSocket)

		ln, err := lc.Listen(ctx, "unix", common.UnixSocket)
		if err != nil {
			return fmt.Errorf("listening on the Unix socket: %w", err)
		}

		s.socket = common.UnixSocket
		s.servers = append(s.servers, servedHTTP{
			name:  "Unix socket server",
			srv:   newHTTPServer("", socketRouter),
			ln:    ln,
			serve: (*http.Server).Serve,
		})

		if o.unixSocketGID != -1 {
			plog.Info(plog.TypeSystem, "setting Unix socket group permissions", "gid", o.unixSocketGID)

			if err := os.Chown(common.UnixSocket, -1, o.unixSocketGID); err != nil {
				return err
			}

			if err := os.Chmod(common.UnixSocket, 0o770); err != nil { //nolint:gosec // G302 the group may use the socket
				return err
			}
		}
	}

	return nil
}

// newRouter routes the UI's pages and assets, and the REST API behind its
// middleware, as the server options say. builderOpts configure the Builder API.
func newRouter(assets http.FileSystem, builderOpts ...builderOption) (*routing.Mux, error) {
	pages := pageHandler(assets)

	root := routing.Group{
		Prefix: "", Routes: rootRoutes(assets), Middleware: nil, Options: nil,
		NotFound: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			plog.Warn(
				plog.TypeSystem,
				"Unknown route requested",
				"route",
				middleware.RedactURI(r.RequestURI),
				"method",
				r.Method,
			)

			// index.html names the current build's hashed chunks, so browsers must
			// revalidate it rather than reuse a stale copy
			w.Header().Set("Cache-Control", "no-cache")

			pages.ServeHTTP(w, r)
		}),
		// a method a route does not take gets the JSON Error and Allow, as under /api/v1
		MethodNotAllowed: http.HandlerFunc(methodNotAllowed),
	}

	api := routing.Group{
		Prefix: "/api/v1", Routes: nil, Middleware: nil,
		// API routes take OPTIONS: the router sets Allow, and AnswerOptions answers 204
		Options: http.HandlerFunc(optionsAnswered),
		// The root group's NotFound serves the SPA index, so an API client would get 200 HTML.
		// A route behind a disabled feature flag would then also look enabled.
		NotFound:         apiNotFoundHandler(),
		MethodNotAllowed: http.HandlerFunc(methodNotAllowed),
	}

	if err := registerBuilderRoutes(&api, builderOpts...); err != nil {
		return nil, fmt.Errorf("configuring Builder API: %w", err)
	}

	api.Routes = append(api.Routes, apiRoutes()...)
	api.Routes = append(api.Routes, socketRoutes()...)

	api.Middleware = append(api.Middleware,
		// outermost, so the other middleware (full logging in particular) sees the
		// uncompressed response
		CompressResponses,
		middleware.AnswerOptions,
		middleware.Auth(o.jwtKey, o.proxyAuthHeader, middleware.OnRefusal(audit.TokenRefused)),
		// after Auth, so a refused request takes no sequence number
		middleware.Freshness,
		middleware.Version,
	)

	switch o.logMiddleware {
	case "full":
		plog.Info(plog.TypeSystem, "full HTTP logging is enabled")

		api.Middleware = append(api.Middleware, middleware.LogFull)
	case "requests":
		plog.Info(plog.TypeSystem, "requests-only HTTP logging is enabled")

		api.Middleware = append(api.Middleware, middleware.LogRequests)
	}

	return routing.NewMux(root, api)
}

// rootRoutes are the routes outside /api/v1: downloads, WebShark, features,
// version, health probes, and the UI's assets, as the server options say.
func rootRoutes(assets http.FileSystem) []routing.Route {
	var routes []routing.Route

	if o.featured(FeatureTunnelerDownload) {
		plog.Info(plog.TypeSystem, "Serving phēnix tunneler downloads")

		routes = append(routes,
			routing.Row(http.MethodGet, "/downloads/tunneler", weberror.ErrorHandler(forward.ListTunnelers)),
			routing.Row(http.MethodGet, "/downloads/tunneler/{name}", weberror.ErrorHandler(forward.GetTunneler)),
			routing.Row(http.MethodHead, "/downloads/tunneler/{name}", weberror.ErrorHandler(forward.GetTunneler)),
		)
	}

	// capture grants are the credentials of these routes (see capability.go)
	grants := grantChecker{jwtKey: o.jwtKey, proxyAuthHeader: o.proxyAuthHeader}

	// streams a capture into a command outside the browser, with or without
	// WebShark, under the /webshark/ that proxies already pass on
	routes = append(routes, routing.Row(http.MethodGet, "/webshark/streams/{token}", weberror.ErrorHandler(grants.GetCaptureStreamByToken)))

	if o.featured(FeatureWebShark) {
		plog.Info(plog.TypeSystem, "Serving WebShark", "dir", o.websharkDir)

		routes = append(routes,
			routing.Row(http.MethodGet, "/webshark/json", http.HandlerFunc(grants.GetWebSharkJSON)),
			routing.Row(http.MethodGet, "/webshark/captures/{name}", http.HandlerFunc(grants.GetWebSharkCaptureFile)),
			routing.Row(http.MethodHead, "/webshark/captures/{name}", http.HandlerFunc(grants.GetWebSharkCaptureFile)),
			routing.Row(http.MethodGet, "/webshark/watch", http.HandlerFunc(grants.GetWebSharkWatch)),
			routing.Row("", "/webshark/", WebSharkHandler(o.websharkDir, o.basePath)),
		)
	} else {
		// rather than the phēnix UI, which would load itself in its own frame
		routes = append(routes, routing.Row("", "/webshark/",
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				weberror.Error(w, "WebShark is not installed on this phēnix server", http.StatusNotFound)
			}),
		))
	}

	// Health endpoints sit outside /api/v1, and so outside its auth,
	// compression and request logging: probes need no credentials, and only
	// their output fields depend on the request's.
	probes := newHealthHandlers()

	routes = append(routes,
		routing.Row(http.MethodGet, "/features", http.HandlerFunc(GetFeatures)),
		routing.Row(http.MethodGet, "/version", http.HandlerFunc(GetVersion)),
		routing.Row(http.MethodGet, "/healthz", probes.live),
		routing.Row(http.MethodHead, "/healthz", probes.live),
		routing.Row(http.MethodGet, "/readyz", probes.ready),
		routing.Row(http.MethodHead, "/readyz", probes.ready),
	)

	plog.Info(plog.TypeSystem, "setting up assets")

	spec := OpenAPISpecHandler(assets)

	return append(routes,
		routing.Row(http.MethodGet, openAPIPath, spec),
		routing.Row(http.MethodHead, openAPIPath, spec),
		routing.Row("", "/docs/", StaticHandler(assets, false)),
		routing.Row("", "/assets/", builderAssetHandler(assets, StaticHandler(assets, true))),
		routing.Row("", "/favicon.ico", StaticHandler(assets, false)),
	)
}

// apiRoutes are the routes below /api/v1, after the Builder routes and
// before the socket routes, as the server options say.
//
//nolint:funlen // the route table
func apiRoutes() []routing.Route {
	// the routes that start a background job honor Idempotency-Key
	idempotent := idempotency.New(idempotency.DefaultTTL, idempotency.DefaultMaxEntries, idempotency.DefaultMaxBytes)

	webSettings := newSettingsRoutes(o.runtimeSettings)
	settings := newRuntimeSettingsRoutes(o.runtimeSettings)

	routes := []routing.Route{
		routing.Row(http.MethodGet, "/configs", weberror.ErrorHandler(GetConfigs)),
		routing.Row(http.MethodPost, "/configs", weberror.ErrorHandler(CreateConfig)),
		routing.Row(http.MethodGet, "/configs/{kind}/{name}", weberror.ErrorHandler(GetConfig)),
		routing.Row(http.MethodPut, "/configs/{kind}/{name}", weberror.ErrorHandler(UpdateConfig)),
		routing.Row(http.MethodDelete, "/configs/{kind}/{name}", weberror.ErrorHandler(DeleteConfig)),
		routing.Row(http.MethodPost, "/configs/download", weberror.ErrorHandler(DownloadConfigs)),
		routing.Row(http.MethodGet, "/schemas/{version}", weberror.ErrorHandler(GetSchemaSpec)),
		routing.Row(http.MethodGet, "/schemas/{kind}/{version}", weberror.ErrorHandler(GetSchema)),

		routing.Row(http.MethodGet, "/experiments", weberror.ErrorHandler(GetExperiments)),
		routing.Row(http.MethodPost, "/experiments", weberror.ErrorHandler(CreateExperiment)),
		routing.Row(http.MethodGet, "/experiments/{name}", weberror.ErrorHandler(GetExperiment)),
		routing.Row(http.MethodPatch, "/experiments/{name}", weberror.ErrorHandler(UpdateExperiment)),
		routing.Row(http.MethodDelete, "/experiments/{name}", weberror.ErrorHandler(DeleteExperiment)),
		routing.Row(http.MethodGet, "/experiments/{name}/apps", weberror.ErrorHandler(GetExperimentApps)),
		routing.Row(http.MethodGet, "/experiments/{name}/apps/input", weberror.ErrorHandler(GetExperimentAppsInput)),
		routing.Row(http.MethodGet, "/experiments/{name}/schedule/input", weberror.ErrorHandler(GetExperimentSchedulerInput)),
		routing.Row(
			http.MethodGet,
			"/experiments/{name}/scorch/components/{cmp}/input",
			weberror.ErrorHandler(GetExperimentScorchComponentInput),
		),
		routing.Row(http.MethodPost, "/experiments/{name}/start", weberror.ErrorHandler(StartExperiment)),
		routing.Row(http.MethodPost, "/experiments/{name}/stop", weberror.ErrorHandler(StopExperiment)),
		routing.Row(http.MethodPost, "/experiments/{name}/restart", weberror.ErrorHandler(RestartExperiment)),
		routing.Row(http.MethodGet, "/experiments/{exp}/netflow/ws", weberror.ErrorHandler(GetNetflowWebSocket)),
		routing.Row(http.MethodGet, "/experiments/{name}/topology", weberror.ErrorHandler(GetExperimentTopology)),
		routing.Row(http.MethodGet, "/experiments/{name}/topology/search", weberror.ErrorHandler(SearchExperimentTopology)),
		// canceling triggered apps is a socket route
		routing.Row(http.MethodPost, "/experiments/{name}/trigger", weberror.ErrorHandler(TriggerExperimentApps)),
		routing.Row(http.MethodGet, "/experiments/{name}/schedule", weberror.ErrorHandler(GetExperimentSchedule)),
		routing.Row(http.MethodPost, "/experiments/{name}/schedule", weberror.ErrorHandler(ScheduleExperiment)),
		routing.Row(http.MethodGet, "/schedulers", weberror.ErrorHandler(GetSchedulers)),
		routing.Row(http.MethodGet, "/experiments/{name}/captures", weberror.ErrorHandler(GetExperimentCaptures)),
		routing.Row(http.MethodPost, "/experiments/{name}/captures", weberror.ErrorHandler(StartExperimentCapture)),
		routing.Row(http.MethodGet, "/experiments/{name}/captures/{id}", weberror.ErrorHandler(GetExperimentCapture)),
		routing.Row(http.MethodDelete, "/experiments/{name}/captures/{id}", weberror.ErrorHandler(StopExperimentCapture)),
		routing.Row(http.MethodGet, "/experiments/{name}/captures/{id}/stream", weberror.ErrorHandler(GetExperimentCaptureStream)),
		routing.Row(http.MethodPost, "/experiments/{exp}/captureSubnet", weberror.ErrorHandler(StartCaptureSubnet)),
		routing.Row(http.MethodPost, "/experiments/{exp}/stopCaptureSubnet", weberror.ErrorHandler(StopCaptureSubnet)),
		routing.Row(http.MethodPost, "/experiments/{exp}/captureVlan", weberror.ErrorHandler(StartCaptureVLAN)),
		routing.Row(http.MethodPost, "/experiments/{exp}/stopCaptureVlan", weberror.ErrorHandler(StopCaptureVLAN)),
		routing.Row(http.MethodPost, "/experiments/{exp}/webshark", weberror.ErrorHandler(CreateWebSharkCapture)),
		routing.Row(http.MethodPost, "/experiments/{exp}/captureTokens", weberror.ErrorHandler(CreateCaptureToken)),
		routing.Row(http.MethodGet, "/experiments/{exp}/captureTokens", http.HandlerFunc(ListCaptureTokens)),
		routing.Row(http.MethodDelete, "/experiments/{exp}/captureTokens/{id}", weberror.ErrorHandler(DeleteCaptureToken)),

		routing.Row(http.MethodGet, "/experiments/{name}/files", weberror.ErrorHandler(GetExperimentFiles)),
		routing.Row(http.MethodPost, "/experiments/{name}/files/download", weberror.ErrorHandler(DownloadExperimentFiles)),
		routing.Row(http.MethodPost, "/experiments/{name}/files/archive", idempotent.Wrap(weberror.ErrorHandler(ArchiveExperimentFiles))),
		routing.Row(http.MethodPost, "/experiments/{name}/files/delete", weberror.ErrorHandler(DeleteExperimentFiles)),
		routing.Row(http.MethodGet, "/experiments/{name}/files/{filename}", weberror.ErrorHandler(GetExperimentFile)),
		routing.Row(http.MethodHead, "/experiments/{name}/files/{filename}", weberror.ErrorHandler(GetExperimentFile)),
		routing.Row(http.MethodGet, "/experiments/{name}/files/{filename}/checksum", weberror.ErrorHandler(GetExperimentFileChecksum)),
		routing.Row(http.MethodDelete, "/experiments/{name}/files/{filename}", weberror.ErrorHandler(DeleteExperimentFile)),
	}

	routes = append(routes, scorch.Routes()...)
	routes = append(
		routes,
		routing.Row(http.MethodGet, "/experiments/{name}/soh", weberror.ErrorHandler(GetExperimentSoH)),
		routing.Row(http.MethodGet, "/soh", weberror.ErrorHandler(GetSoHSummary)),
		routing.Row(http.MethodGet, "/experiments/{exp}/vms", weberror.ErrorHandler(GetVMs)),
		routing.Row(http.MethodPatch, "/experiments/{exp}/vms", weberror.ErrorHandler(UpdateVMs)),
		routing.Row(http.MethodPost, "/experiments/{exp}/vms/actions", idempotent.Wrap(weberror.ErrorHandler(RunVMActions))),
		routing.Row(http.MethodPost, "/experiments/{exp}/vms/resetSettings", weberror.ErrorHandler(ResetVMSettings)),
		routing.Row(http.MethodGet, "/experiments/{exp}/vms/{name}", weberror.ErrorHandler(GetVM)),
		routing.Row(http.MethodPatch, "/experiments/{exp}/vms/{name}", weberror.ErrorHandler(UpdateVM)),
		routing.Row(http.MethodDelete, "/experiments/{exp}/vms/{name}", weberror.ErrorHandler(DeleteVM)),
		routing.Row(http.MethodPost, "/experiments/{exp}/vms/{name}/reset", weberror.ErrorHandler(ResetVM)),
		routing.Row(http.MethodPost, "/experiments/{exp}/vms/{name}/restart", weberror.ErrorHandler(RestartVM)),
		routing.Row(http.MethodPost, "/experiments/{exp}/vms/{name}/start", weberror.ErrorHandler(StartVM)),
		routing.Row(http.MethodPost, "/experiments/{exp}/vms/{name}/stop", weberror.ErrorHandler(StopVM)),
		routing.Row(http.MethodPost, "/experiments/{exp}/vms/{name}/shutdown", weberror.ErrorHandler(ShutdownVM)),
		routing.Row(http.MethodPost, "/experiments/{exp}/vms/{name}/redeploy", weberror.ErrorHandler(RedeployVM)),
		routing.Row(http.MethodGet, "/experiments/{exp}/vms/{name}/qos", weberror.ErrorHandler(GetVMQoS)),
		routing.Row(http.MethodPut, "/experiments/{exp}/vms/{name}/interfaces/{iface}/qos", weberror.ErrorHandler(SetVMInterfaceQoS)),
		routing.Row(http.MethodDelete, "/experiments/{exp}/vms/{name}/interfaces/{iface}/qos", weberror.ErrorHandler(ClearVMInterfaceQoS)),
		routing.Row(http.MethodPost, "/experiments/{exp}/vms/{name}/cdrom", weberror.ErrorHandler(ChangeOpticalDisc)),
		routing.Row(http.MethodDelete, "/experiments/{exp}/vms/{name}/cdrom", weberror.ErrorHandler(EjectOpticalDisc)),
		routing.Row(http.MethodGet, "/experiments/{exp}/vms/{name}/screenshot.png", weberror.ErrorHandler(GetScreenshot)),
		routing.Row(http.MethodPost, "/experiments/{exp}/vms/{name}/screenshots", weberror.ErrorHandler(SaveScreenshot)),
		routing.Row(http.MethodGet, "/experiments/{exp}/vms/{name}/vnc", weberror.ErrorHandler(GetVNC)),
		routing.Row(http.MethodGet, "/experiments/{exp}/vms/{name}/vnc/ws", weberror.ErrorHandler(GetVNCWebSocket)),
		routing.Row(http.MethodGet, "/experiments/{exp}/vms/{name}/captures", weberror.ErrorHandler(GetVMCaptures)),
		routing.Row(http.MethodPost, "/experiments/{exp}/vms/{name}/captures", weberror.ErrorHandler(StartVMCapture)),
		routing.Row(http.MethodDelete, "/experiments/{exp}/vms/{name}/captures", weberror.ErrorHandler(StopVMCaptures)),
		routing.Row(http.MethodGet, "/experiments/{exp}/vms/{name}/captures/{iface}/stream", weberror.ErrorHandler(GetVMCaptureStream)),
		routing.Row(http.MethodGet, "/experiments/{exp}/vms/{name}/snapshots", weberror.ErrorHandler(GetVMSnapshots)),
		routing.Row(http.MethodPost, "/experiments/{exp}/vms/{name}/snapshots", weberror.ErrorHandler(SnapshotVM)),
		routing.Row(http.MethodPost, "/experiments/{exp}/vms/{name}/snapshots/{snapshot}", weberror.ErrorHandler(RestoreVM)),
		routing.Row(http.MethodDelete, "/experiments/{exp}/vms/{name}/snapshots/{snapshot}", weberror.ErrorHandler(DeleteVMSnapshot)),
		routing.Row(http.MethodPost, "/experiments/{exp}/vms/{name}/commit", weberror.ErrorHandler(CommitVM)),
		routing.Row(http.MethodPost, "/experiments/{exp}/vms/{name}/memorySnapshot", weberror.ErrorHandler(CreateVMMemorySnapshot)),
		// listing, creating and deleting port forwards are socket routes
		routing.Row(
			http.MethodGet,
			"/experiments/{exp}/vms/{name}/forwards/{host}/{port}/ws",
			weberror.ErrorHandler(forward.GetPortForwardWebSocket),
		),
	)

	if o.featured(FeatureVMMount) {
		routes = append(
			routes,
			routing.Row(http.MethodPost, "/experiments/{exp}/vms/{name}/mount", weberror.ErrorHandler(MountVM)),
			routing.Row(http.MethodDelete, "/experiments/{exp}/vms/{name}/unmount", weberror.ErrorHandler(UnmountVM)),
			// the handlers of the mount files read path from the query and answer 400 without it
			routing.Row(http.MethodGet, "/experiments/{exp}/vms/{name}/files", weberror.ErrorHandler(GetMountFiles)),
			routing.Row(http.MethodGet, "/experiments/{exp}/vms/{name}/files/download", weberror.ErrorHandler(DownloadMountFile)),
			routing.Row(http.MethodHead, "/experiments/{exp}/vms/{name}/files/download", weberror.ErrorHandler(DownloadMountFile)),
			routing.Row(http.MethodPut, "/experiments/{exp}/vms/{name}/files/upload", weberror.ErrorHandler(UploadMountFile)),
			routing.Row(http.MethodPost, "/experiments/{exp}/vms/{name}/files/copy", weberror.ErrorHandler(CopyExperimentFileToMount)),
		)
	}

	routes = append(
		routes,
		routing.Row(http.MethodGet, "/disks", weberror.ErrorHandler(GetDisks)),
		// the disk handlers read their query parameters and answer 400 for a missing one
		routing.Row(http.MethodPost, "/disks/snapshot", weberror.ErrorHandler(SnapshotDisk)),
		routing.Row(http.MethodPost, "/disks/rebase", weberror.ErrorHandler(RebaseDisk)),
		routing.Row(http.MethodPost, "/disks/resize", weberror.ErrorHandler(ResizeDisk)),
		routing.Row(http.MethodPost, "/disks/commit", weberror.ErrorHandler(CommitDisk)),
		routing.Row(http.MethodPost, "/disks/clone", weberror.ErrorHandler(CloneDisk)),
		routing.Row(http.MethodPost, "/disks/clone/jobs", idempotent.Wrap(weberror.ErrorHandler(CloneDiskJob))),
		routing.Row(http.MethodDelete, "/disks", weberror.ErrorHandler(DeleteDisk)),
		routing.Row(http.MethodPost, "/disks/rename", weberror.ErrorHandler(RenameDisk)),
		routing.Row(http.MethodPost, "/disks", weberror.ErrorHandler(UploadDisk)),
		routing.Row(http.MethodGet, "/disks/download", weberror.ErrorHandler(DownloadDisk)),
		routing.Row(http.MethodHead, "/disks/download", weberror.ErrorHandler(DownloadDisk)),
		routing.Row(http.MethodGet, "/disks/checksum", weberror.ErrorHandler(GetDiskChecksum)),
		routing.Row(http.MethodGet, "/disks/details", weberror.ErrorHandler(GetDiskDetails)),
		routing.Row(http.MethodGet, "/vms", weberror.ErrorHandler(GetAllVMs)),
		routing.Row(http.MethodGet, "/applications", weberror.ErrorHandler(GetApplications)),
		routing.Row(http.MethodGet, "/topologies", weberror.ErrorHandler(GetTopologies)),
		routing.Row(http.MethodGet, "/topologies/{topo}/scenarios", weberror.ErrorHandler(GetScenarios)),
		routing.Row(http.MethodGet, "/hosts", weberror.ErrorHandler(GetClusterHosts)),
		routing.Row(http.MethodGet, "/vlans", weberror.ErrorHandler(GetVLANs)),
		routing.Row(http.MethodGet, "/vlans/free", weberror.ErrorHandler(FindFreeVLANs)),
		routing.Row(http.MethodPost, "/vlans/check", weberror.ErrorHandler(CheckVLANs)),
		routing.Row(http.MethodGet, "/vlans/blacklist", weberror.ErrorHandler(GetVLANBlacklist)),
		routing.Row(http.MethodPut, "/vlans/blacklist/{id}", weberror.ErrorHandler(AddVLANBlacklist)),
		routing.Row(http.MethodDelete, "/vlans/blacklist/{id}", weberror.ErrorHandler(RemoveVLANBlacklist)),
		routing.Row(http.MethodDelete, "/vlans/allocations/{namespace}", weberror.ErrorHandler(ReleaseVLANAllocations)),
		routing.Row(http.MethodGet, "/vlans/{id}", weberror.ErrorHandler(GetVLAN)),
		routing.Row(http.MethodGet, "/experiments/{name}/vlans", weberror.ErrorHandler(GetExperimentVLANs)),
		routing.Row(http.MethodPut, "/experiments/{name}/vlans/aliases/{alias}", weberror.ErrorHandler(PinExperimentVLANAlias)),
		routing.Row(http.MethodDelete, "/experiments/{name}/vlans/aliases/{alias}", weberror.ErrorHandler(UnpinExperimentVLANAlias)),
		routing.Row(http.MethodPut, "/experiments/{name}/vlans/range", weberror.ErrorHandler(SetExperimentVLANRange)),
		routing.Row(http.MethodDelete, "/experiments/{name}/vlans/range", weberror.ErrorHandler(ClearExperimentVLANRange)),
		routing.Row(http.MethodPost, "/experiments/{name}/vlans/pin-live", weberror.ErrorHandler(PinExperimentLiveVLANs)),
		// changing users and roles and creating tokens are socket routes
		routing.Row(http.MethodGet, "/users", weberror.ErrorHandler(GetUsers)),
		routing.Row(http.MethodGet, "/users/{username}", weberror.ErrorHandler(GetUser)),
		routing.Row(http.MethodGet, "/roles", weberror.ErrorHandler(GetRoles)),
		routing.Row(http.MethodGet, "/roles/{name}", weberror.ErrorHandler(GetRole)),
		routing.Row(http.MethodPost, "/signup", weberror.ErrorHandler(Signup)),
		routing.Row(http.MethodGet, "/login", weberror.ErrorHandler(Login)),
		routing.Row(http.MethodPost, "/login", weberror.ErrorHandler(Login)),
		routing.Row(http.MethodPost, "/logout", weberror.ErrorHandler(Logout)),
		routing.Row(http.MethodGet, "/ws", http.HandlerFunc(broker.ServeWS)),
		routing.Row(http.MethodPost, "/console", weberror.ErrorHandler(CreateConsole)),
		routing.Row(http.MethodGet, "/console/{pid}", weberror.ErrorHandler(GetConsole)),
		routing.Row(http.MethodDelete, "/console/{pid}", weberror.ErrorHandler(DeleteConsole)),
		routing.Row(http.MethodGet, "/console/{pid}/ws", weberror.ErrorHandler(WsConsole)),
		routing.Row(http.MethodPost, "/console/{pid}/size", weberror.ErrorHandler(ResizeConsole)),
		// background jobs; listing, reading and canceling them are socket routes
		routing.Row(http.MethodDelete, "/jobs/{id}", weberror.ErrorHandler(DeleteJob)),
		routing.Row(http.MethodGet, "/jobs/{id}/artifact", weberror.ErrorHandler(GetJobArtifact)),
		routing.Row(http.MethodHead, "/jobs/{id}/artifact", weberror.ErrorHandler(GetJobArtifact)),
		routing.Row(http.MethodGet, "/settings", weberror.ErrorHandler(webSettings.GetSettings)),
		routing.Row(http.MethodPost, "/settings", weberror.ErrorHandler(webSettings.SetSettings)),
		routing.Row(http.MethodGet, "/settings/password", weberror.ErrorHandler(GetPasswordRequirements)),
		routing.Row(http.MethodGet, "/settings/timeout", weberror.ErrorHandler(GetTimeoutSettings)),
		// bulk config deletes, RBAC permissions, minimega commands and runtime settings, as the CLI has them
		routing.Row(http.MethodPost, "/configs/delete", weberror.ErrorHandler(DeleteConfigs)),
		routing.Row(http.MethodGet, "/permissions", weberror.ErrorHandler(GetPermissions)),
		routing.Row(http.MethodPost, "/minimega/commands", weberror.ErrorHandler(RunMinimegaCommand)),
		routing.Row(http.MethodGet, "/runtimeSettings", weberror.ErrorHandler(settings.GetRuntimeSettings)),
		routing.Row(http.MethodGet, "/runtimeSettings/{key}", weberror.ErrorHandler(settings.GetRuntimeSetting)),
		routing.Row(http.MethodPut, "/runtimeSettings/{key}", weberror.ErrorHandler(settings.SetRuntimeSetting)),
		routing.Row(http.MethodDelete, "/runtimeSettings/{key}", weberror.ErrorHandler(settings.UnsetRuntimeSetting)),
	)

	return routes
}

// apiLocation returns the absolute path of the API resource at p, relative to
// /api/v1/, as a client behind the proxy reaches it: o.basePath is the prefix
// the UI is served behind, normalized to start and end with "/" (a test may
// set it unnormalized).
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

// newSocketRouter routes the API routes the Unix socket serves, to every
// local client as global-admin, since the socket's file permissions are its
// access control.
func newSocketRouter() (*routing.Mux, error) {
	return routing.NewMux(routing.Group{
		Prefix: "/api/v1", Routes: socketRoutes(),
		Middleware: []func(http.Handler) http.Handler{
			middleware.UnixSocket,
			middleware.NoAuth,
			middleware.Freshness,
			middleware.Version,
		},
		Options: nil, NotFound: nil, MethodNotAllowed: http.HandlerFunc(methodNotAllowed),
	})
}

// socketRoutes are the API routes the Unix socket serves as well.
func socketRoutes() []routing.Route {
	routes := []routing.Route{
		routing.Row(http.MethodPost, "/workflow/apply/{branch}", weberror.ErrorHandler(ApplyWorkflow)),
		routing.Row(http.MethodPost, "/workflow/configs/{branch}", weberror.ErrorHandler(WorkflowUpsertConfig)),
		routing.Row(http.MethodGet, "/options", weberror.ErrorHandler(GetOptions)),
		routing.Row(http.MethodGet, "/jobs", weberror.ErrorHandler(GetJobs)),
		routing.Row(http.MethodGet, "/jobs/{id}", weberror.ErrorHandler(GetJob)),
		routing.Row(http.MethodPost, "/jobs/{id}/cancel", weberror.ErrorHandler(CancelJob)),
		// for phenix experiment trigger --cancel
		routing.Row(http.MethodDelete, "/experiments/{name}/trigger", weberror.ErrorHandler(CancelTriggeredExperimentApps)),
		routing.Row(http.MethodGet, "/experiments/{exp}/netflow", weberror.ErrorHandler(GetNetflow)),
		routing.Row(http.MethodPost, "/experiments/{exp}/netflow", weberror.ErrorHandler(StartNetflow)),
		routing.Row(http.MethodDelete, "/experiments/{exp}/netflow", weberror.ErrorHandler(StopNetflow)),
		routing.Row(http.MethodGet, "/experiments/{exp}/netflow/captures", weberror.ErrorHandler(GetNetflowCaptures)),
		routing.Row(http.MethodGet, "/experiments/{exp}/netflow/captures/{id}", weberror.ErrorHandler(DownloadNetflowCapture)),
		routing.Row(http.MethodHead, "/experiments/{exp}/netflow/captures/{id}", weberror.ErrorHandler(DownloadNetflowCapture)),
		routing.Row(http.MethodGet, "/logs", weberror.ErrorHandler(GetLogs)),
		routing.Row(http.MethodGet, "/logs/files", weberror.ErrorHandler(GetLogFiles)),
		routing.Row(http.MethodGet, "/logs/files/{name}", weberror.ErrorHandler(DownloadLogFile)),
		routing.Row(http.MethodHead, "/logs/files/{name}", weberror.ErrorHandler(DownloadLogFile)),
		// the server holds the port forwards, and its websockets and capture
		// grants follow the changes of users and roles
		routing.Row(http.MethodGet, "/experiments/{exp}/vms/{name}/forwards", weberror.ErrorHandler(forward.GetPortForwards)),
		routing.Row(http.MethodPost, "/experiments/{exp}/vms/{name}/forwards", weberror.ErrorHandler(forward.CreatePortForward)),
		routing.Row(http.MethodDelete, "/experiments/{exp}/vms/{name}/forwards", weberror.ErrorHandler(forward.DeletePortForward)),
		routing.Row(http.MethodPost, "/users", weberror.ErrorHandler(CreateUser)),
		routing.Row(http.MethodPatch, "/users/{username}", weberror.ErrorHandler(UpdateUser)),
		routing.Row(http.MethodDelete, "/users/{username}", weberror.ErrorHandler(DeleteUser)),
		routing.Row(http.MethodPost, "/users/{username}/tokens", weberror.ErrorHandler(CreateUserToken)),
		routing.Row(http.MethodPost, "/roles", weberror.ErrorHandler(CreateRole)),
		routing.Row(http.MethodPatch, "/roles/{name}", weberror.ErrorHandler(UpdateRole)),
		routing.Row(http.MethodDelete, "/roles/{name}", weberror.ErrorHandler(DeleteRole)),
	}

	// for phenix experiment scorch
	return append(routes, scorch.SocketRoutes()...)
}

// methodNotAllowed answers a request for a path that does not take its
// method, with the JSON Error rather than the router's empty 405. The router
// has set the Allow header.
func methodNotAllowed(w http.ResponseWriter, _ *http.Request) {
	weberror.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

// optionsAnswered answers an OPTIONS request with 204 and no body. The router
// has set the Allow header.
func optionsAnswered(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

// apiNotFoundHandler answers 404 to an API request that matches no route, and never serves the SPA index.
// Router middleware, including authentication, does not run here, so it must disclose nothing the path does not.
func apiNotFoundHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		plog.Warn(
			plog.TypeSystem, "Unknown API route requested", "route", middleware.RedactURI(r.RequestURI), "method", r.Method,
		)
		weberror.Error(w, "no API route matches this request", http.StatusNotFound)
	})
}
