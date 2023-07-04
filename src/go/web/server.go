package web

import (
	"context"
	"net"
	"net/http"
	"os"
	"strings"

	"phenix/util/plog"
	"phenix/web/broker"
	"phenix/web/forward"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/scorch"
	"phenix/web/util"
	"phenix/web/weberror"

	assetfs "github.com/elazarl/go-bindata-assetfs"
	"github.com/gorilla/mux"
)

type route struct {
	path    string
	handler http.Handler
	methods []string
}

var o serverOptions

func ConfigureUsers(users []string) error {
	setUserRole := func(user *rbac.User, rname string, resources ...string) {
		if role, err := rbac.RoleFromConfig(rname); err == nil {
			role.SetResourceNames(resources...)

			// allow user to get their own user details
			role.AddPolicy(
				[]string{"users"},
				[]string{user.Username()},
				[]string{"get"},
			)

			user.SetRole(role)
		} else {
			plog.Error("getting role for user", "user", user.Username(), "role", rname, "err", err)
		}
	}

	for _, u := range users {
		creds := strings.Split(u, ":")
		uname := creds[0]
		pword := creds[1]
		rname := creds[2]

		// User already exists.
		// Confirm existing user has specified role and update if necessary.
		if user, err := rbac.GetUser(uname); err == nil {
			if user.RoleName() != rname {
				plog.Debug("updating role for existing user", "user", user.Username(), "old", user.RoleName(), "new", rname)

				setUserRole(user, rname, creds[3:]...)
			}

			continue
		}

		plog.Debug("creating default user", "user", uname, "role", rname)

		user := rbac.NewUser(uname, pword)

		setUserRole(user, rname, creds[3:]...)
	}

	return nil
}

func Start(opts ...ServerOption) error {
	o = newServerOptions(opts...)

	ConfigureUsers(o.users)

	var (
		router = mux.NewRouter().StrictSlash(true)
		assets http.FileSystem
	)

	if o.unbundled {
		assets = http.Dir("web/public")
		plog.Info("serving unbundled assets")
	} else {
		assets = &assetfs.AssetFS{
			Asset:     Asset,
			AssetDir:  AssetDir,
			AssetInfo: AssetInfo,
		}
	}

	if o.featured("tunneler-download") {
		plog.Info("Serving phēnix tunneler downloads")
		router.HandleFunc("/downloads/tunneler/{name}", forward.GetTunneler).Methods("GET")
	}

	router.HandleFunc("/features", GetFeatures).Methods("GET")
	router.HandleFunc("/version", GetVersion).Methods("GET")
	router.HandleFunc("/builder", GetBuilder).Methods("GET")
	router.HandleFunc("/builder/save", SaveBuilderTopology).Methods("POST")

	plog.Info("setting up assets")

	router.PathPrefix("/docs/").Handler(
		http.FileServer(assets),
	)

	router.PathPrefix("/novnc/").Handler(
		http.FileServer(assets),
	)

	router.PathPrefix("/xterm.js/").Handler(
		http.FileServer(assets),
	)

	router.PathPrefix("/assets/").Handler(
		http.FileServer(assets),
	)

	router.PathPrefix("/grapheditor/").Handler(
		http.FileServer(assets),
	)

	router.NotFoundHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch a := assets.(type) {
		case *assetfs.AssetFS:
			util.NewBinaryFileSystem(a).ServeFile(w, r, "index.html")
		case http.FileSystem:
			http.ServeFile(w, r, "web/public/index.html")
		}
	})

	api := router.PathPrefix("/api/v1").Subrouter()

	// OPTIONS method needed for CORS
	api.Handle("/builder/topologies", weberror.ErrorHandler(GetBuilderTopologies)).Methods("GET", "OPTIONS")
	api.Handle("/builder/topologies/{name}", weberror.ErrorHandler(GetBuilderTopology)).Methods("GET", "OPTIONS")
	api.Handle("/configs", weberror.ErrorHandler(GetConfigs)).Methods("GET", "OPTIONS")
	api.Handle("/configs", weberror.ErrorHandler(CreateConfig)).Methods("POST", "OPTIONS")
	api.Handle("/configs/{kind}/{name}", weberror.ErrorHandler(GetConfig)).Methods("GET", "OPTIONS")
	api.Handle("/configs/{kind}/{name}", weberror.ErrorHandler(UpdateConfig)).Methods("PUT", "OPTIONS")
	api.Handle("/configs/{kind}/{name}", weberror.ErrorHandler(DeleteConfig)).Methods("DELETE", "OPTIONS")
	api.Handle("/configs/download", weberror.ErrorHandler(DownloadConfigs)).Methods("POST", "OPTIONS")
	api.Handle("/schemas/{version}", weberror.ErrorHandler(GetSchemaSpec)).Methods("GET", "OPTIONS")
	api.Handle("/schemas/{kind}/{version}", weberror.ErrorHandler(GetSchema)).Methods("GET", "OPTIONS")
	api.HandleFunc("/experiments", GetExperiments).Methods("GET", "OPTIONS")
	api.HandleFunc("/experiments", CreateExperiment).Methods("POST", "OPTIONS")
	api.Handle("/experiments/builder", weberror.ErrorHandler(CreateExperimentFromBuilder)).Methods("POST", "OPTIONS")
	api.Handle("/experiments/builder", weberror.ErrorHandler(UpdateExperimentFromBuilder)).Methods("PUT", "OPTIONS")
	api.Handle("/experiments/{name}", weberror.ErrorHandler(GetExperiment)).Methods("GET", "OPTIONS")
	api.Handle("/experiments/{name}", weberror.ErrorHandler(UpdateExperiment)).Methods("PATCH", "OPTIONS")
	api.HandleFunc("/experiments/{name}", DeleteExperiment).Methods("DELETE", "OPTIONS")
	api.Handle("/experiments/{name}/apps", weberror.ErrorHandler(GetExperimentApps)).Methods("GET", "OPTIONS")
	api.Handle("/experiments/{name}/start", weberror.ErrorHandler(StartExperiment)).Methods("POST", "OPTIONS")
	api.Handle("/experiments/{name}/stop", weberror.ErrorHandler(StopExperiment)).Methods("POST", "OPTIONS")
	api.HandleFunc("/experiments/{name}/trigger", TriggerExperimentApps).Methods("POST", "OPTIONS")
	api.HandleFunc("/experiments/{name}/trigger", CancelTriggeredExperimentApps).Methods("DELETE", "OPTIONS")
	api.HandleFunc("/experiments/{name}/schedule", GetExperimentSchedule).Methods("GET", "OPTIONS")
	api.HandleFunc("/experiments/{name}/schedule", ScheduleExperiment).Methods("POST", "OPTIONS")
	api.HandleFunc("/experiments/{name}/captures", GetExperimentCaptures).Methods("GET", "OPTIONS")
	api.HandleFunc("/experiments/{exp}/captureSubnet", StartCaptureSubnet).Methods("POST", "OPTIONS")
	api.HandleFunc("/experiments/{exp}/stopCaptureSubnet", StopCaptureSubnet).Methods("POST", "OPTIONS")
	api.HandleFunc("/experiments/{name}/files", GetExperimentFiles).Methods("GET", "OPTIONS")
	api.HandleFunc("/experiments/{name}/files/{filename}", GetExperimentFile).Methods("GET", "OPTIONS")
	api.Handle("/experiments/{name}/scorch/components/{run}/{loop}/{stage}/{cmp}", weberror.ErrorHandler(scorch.GetComponentOutput)).Methods("GET", "OPTIONS")
	api.HandleFunc("/experiments/{name}/scorch/components/{run}/{loop}/{stage}/{cmp}/ws", scorch.StreamComponentOutput).Methods("GET", "OPTIONS")
	api.Handle("/experiments/{name}/scorch/pipelines", weberror.ErrorHandler(scorch.GetPipelines)).Methods("GET", "OPTIONS")
	api.Handle("/experiments/{name}/scorch/pipelines/{run}/{loop}", weberror.ErrorHandler(scorch.GetPipeline)).Methods("GET", "OPTIONS")
	api.Handle("/experiments/{name}/scorch/pipelines/{run}", weberror.ErrorHandler(scorch.StartPipeline)).Methods("POST", "OPTIONS")
	api.Handle("/experiments/{name}/scorch/pipelines/{run}", weberror.ErrorHandler(scorch.CancelPipeline)).Methods("DELETE", "OPTIONS")
	api.HandleFunc("/experiments/{name}/scorch/terminals", scorch.GetTerminals).Methods("GET", "OPTIONS")
	api.HandleFunc("/experiments/{name}/scorch/terminals/{pid}", scorch.ConnectTerminal).Methods("GET", "OPTIONS")
	api.HandleFunc("/experiments/{name}/scorch/terminals/{pid}/exit/{id}", scorch.ExitTerminal).Methods("POST", "OPTIONS")
	api.HandleFunc("/experiments/{name}/scorch/terminals/{pid}/ws/{id}", scorch.StreamTerminal).Methods("GET", "OPTIONS")
	api.HandleFunc("/experiments/{name}/soh", GetExperimentSoH).Methods("GET", "OPTIONS")
	api.HandleFunc("/experiments/{exp}/vms", GetVMs).Methods("GET", "OPTIONS")
	api.HandleFunc("/experiments/{exp}/vms", UpdateVMs).Methods("PATCH", "OPTIONS")
	api.HandleFunc("/experiments/{exp}/vms/{name}", GetVM).Methods("GET", "OPTIONS")
	api.HandleFunc("/experiments/{exp}/vms/{name}", UpdateVM).Methods("PATCH", "OPTIONS")
	api.HandleFunc("/experiments/{exp}/vms/{name}", DeleteVM).Methods("DELETE", "OPTIONS")
	api.HandleFunc("/experiments/{exp}/vms/{name}/reset", ResetVM).Methods("GET", "OPTIONS")
	api.HandleFunc("/experiments/{exp}/vms/{name}/restart", RestartVM).Methods("GET", "OPTIONS")
	api.HandleFunc("/experiments/{exp}/vms/{name}/start", StartVM).Methods("POST", "OPTIONS")
	api.HandleFunc("/experiments/{exp}/vms/{name}/stop", StopVM).Methods("POST", "OPTIONS")
	api.HandleFunc("/experiments/{exp}/vms/{name}/shutdown", ShutdownVM).Methods("GET", "OPTIONS")
	api.HandleFunc("/experiments/{exp}/vms/{name}/redeploy", RedeployVM).Methods("POST", "OPTIONS")
	api.HandleFunc("/experiments/{exp}/vms/{name}/screenshot.png", GetScreenshot).Methods("GET", "OPTIONS")
	api.HandleFunc("/experiments/{exp}/vms/{name}/vnc", GetVNC).Methods("GET", "OPTIONS")
	api.HandleFunc("/experiments/{exp}/vms/{name}/vnc/ws", GetVNCWebSocket).Methods("GET", "OPTIONS")
	api.HandleFunc("/experiments/{exp}/vms/{name}/captures", GetVMCaptures).Methods("GET", "OPTIONS")
	api.HandleFunc("/experiments/{exp}/vms/{name}/captures", StartVMCapture).Methods("POST", "OPTIONS")
	api.HandleFunc("/experiments/{exp}/vms/{name}/captures", StopVMCaptures).Methods("DELETE", "OPTIONS")
	api.HandleFunc("/experiments/{exp}/vms/{name}/snapshots", GetVMSnapshots).Methods("GET", "OPTIONS")
	api.HandleFunc("/experiments/{exp}/vms/{name}/snapshots", SnapshotVM).Methods("POST", "OPTIONS")
	api.HandleFunc("/experiments/{exp}/vms/{name}/snapshots/{snapshot}", RestoreVM).Methods("POST", "OPTIONS")
	api.HandleFunc("/experiments/{exp}/vms/{name}/commit", CommitVM).Methods("POST", "OPTIONS")
	api.HandleFunc("/experiments/{exp}/vms/{name}/memorySnapshot", CreateVMMemorySnapshot).Methods("POST", "OPTIONS")

	api.HandleFunc("/experiments/{exp}/vms/{name}/forwards", forward.GetPortForwards).Methods("GET", "OPTIONS")
	api.HandleFunc("/experiments/{exp}/vms/{name}/forwards", forward.CreatePortForward).Methods("POST", "OPTIONS")
	api.HandleFunc("/experiments/{exp}/vms/{name}/forwards", forward.DeletePortForward).Methods("DELETE", "OPTIONS")
	api.HandleFunc("/experiments/{exp}/vms/{name}/forwards/{host}/{port}/ws", forward.GetPortForwardWebSocket).Methods("GET", "OPTIONS")

	if o.featured("vm-mount") {
		api.HandleFunc("/experiments/{exp}/vms/{name}/mount", MountVM).Methods("POST", "OPTIONS")
		api.HandleFunc("/experiments/{exp}/vms/{name}/unmount", UnmountVM).Methods("DELETE", "OPTIONS")
		api.HandleFunc("/experiments/{exp}/vms/{name}/files", GetMountFiles).Methods("GET", "OPTIONS").Queries("path", "{path}")
		api.HandleFunc("/experiments/{exp}/vms/{name}/files/download", DownloadMountFile).Methods("GET", "OPTIONS").Queries("path", "{path}")
		api.HandleFunc("/experiments/{exp}/vms/{name}/files/upload", UploadMountFile).Methods("PUT", "OPTIONS").Queries("path", "{path}")
	}

	api.HandleFunc("/vms", GetAllVMs).Methods("GET", "OPTIONS")
	api.HandleFunc("/applications", GetApplications).Methods("GET", "OPTIONS")
	api.HandleFunc("/topologies", GetTopologies).Methods("GET", "OPTIONS")
	api.HandleFunc("/topologies/{topo}/scenarios", GetScenarios).Methods("GET", "OPTIONS")
	api.HandleFunc("/disks", GetDisks).Methods("GET", "OPTIONS")
	api.HandleFunc("/hosts", GetClusterHosts).Methods("GET", "OPTIONS")
	api.HandleFunc("/users", GetUsers).Methods("GET", "OPTIONS")
	api.HandleFunc("/users", CreateUser).Methods("POST", "OPTIONS")
	api.HandleFunc("/users/{username}", GetUser).Methods("GET", "OPTIONS")
	api.HandleFunc("/users/{username}", UpdateUser).Methods("PATCH", "OPTIONS")
	api.HandleFunc("/users/{username}", DeleteUser).Methods("DELETE", "OPTIONS")
	api.HandleFunc("/users/{username}/tokens", CreateUserToken).Methods("POST", "OPTIONS")
	api.HandleFunc("/roles", GetRoles).Methods("GET", "OPTIONS")
	api.HandleFunc("/signup", Signup).Methods("POST", "OPTIONS")
	api.HandleFunc("/login", Login).Methods("GET", "POST", "OPTIONS")
	api.HandleFunc("/logout", Logout).Methods("GET", "OPTIONS")
	api.Handle("/history", weberror.ErrorHandler(GetHistory)).Methods("POST", "OPTIONS")
	api.HandleFunc("/ws", broker.ServeWS).Methods("GET")
	api.HandleFunc("/console", CreateConsole).Methods("POST", "OPTIONS")
	api.HandleFunc("/console/{pid}/ws", WsConsole).Methods("GET", "OPTIONS")
	api.HandleFunc("/console/{pid}/size", ResizeConsole).Methods("POST", "OPTIONS").Queries("cols", "{cols:[0-9]+}", "rows", "{rows:[0-9]+}")

	workflowRoutes := []route{
		{"/workflow/apply/{branch}", weberror.ErrorHandler(ApplyWorkflow), []string{"POST"}},
		{"/workflow/configs/{branch}", weberror.ErrorHandler(WorkflowUpsertConfig), []string{"POST"}},
	}

	errorRoutes := []route{
		{"/errors/{uuid}", weberror.ErrorHandler(GetError), []string{"GET"}},
	}

	addRoutesToRouter(api, workflowRoutes...)
	addRoutesToRouter(api, errorRoutes...)

	if o.allowCORS {
		plog.Info("CORS is enabled on HTTP API endpoints")
		api.Use(middleware.AllowCORS)
	}

	switch o.logMiddleware {
	case "full":
		plog.Info("full HTTP logging is enabled")
		api.Use(middleware.LogFull)
	case "requests":
		plog.Info("requests-only HTTP logging is enabled")
		api.Use(middleware.LogRequests)
	}

	api.Use(middleware.Auth(o.jwtKey, o.proxyAuthHeader))

	plog.Info("starting websockets broker")

	go broker.Start()

	plog.Info("starting scorch processors")

	go scorch.Start(o.basePath)

	plog.Info("starting log publisher")

	go PublishMinimegaLogs(context.Background(), o.minimegaLogs)

	plog.Info("using base path", "path", o.basePath)
	plog.Info("using JWT lifetime", "lifetime", o.jwtLifetime)

	if o.unixSocket != "" {
		var (
			router = mux.NewRouter().StrictSlash(true)
			api    = router.PathPrefix("/api/v1").Subrouter()
		)

		addRoutesToRouter(api, workflowRoutes...)
		addRoutesToRouter(api, errorRoutes...)

		api.Use(middleware.NoAuth)

		os.Remove(o.unixSocket)

		plog.Info("starting Unix socket server", "path", o.unixSocket)

		server := http.Server{Handler: router}
		listener, err := net.Listen("unix", o.unixSocket)
		if err != nil {
			return err
		}

		go func() {
			if err := server.Serve(listener); err != nil {
				plog.Error("serving Unix socket", "err", err)
			}
		}()
	}

	if o.tlsEnabled() {
		plog.Info("starting HTTPS server", "endpoint", o.endpoint)
		return http.ListenAndServeTLS(o.endpoint, o.tlsCrtPath, o.tlsKeyPath, router)
	} else {
		plog.Info("Starting HTTP server", "endpoint", o.endpoint)
		return http.ListenAndServe(o.endpoint, router)
	}
}

func addRoutesToRouter(router *mux.Router, routes ...route) {
	for _, r := range routes {
		// OPTIONS method needed for CORS
		methods := append(r.methods, "OPTIONS")
		router.Handle(r.path, r.handler).Methods(methods...)
	}
}
