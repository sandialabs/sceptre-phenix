package web

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"phenix/api/config"
	"phenix/web/broker"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/scorch"
	"phenix/web/util"
	"phenix/web/weberror"

	log "github.com/activeshadow/libminimega/minilog"
	assetfs "github.com/elazarl/go-bindata-assetfs"
	"github.com/gorilla/mux"
)

var o serverOptions

func Start(opts ...ServerOption) error {
	o = newServerOptions(opts...)

	for _, u := range o.users {
		creds := strings.Split(u, ":")
		uname := creds[0]
		pword := creds[1]
		rname := creds[2]

		if _, err := config.Get("user/"+uname, false); err == nil {
			continue
		}

		user := rbac.NewUser(uname, pword)

		role, err := rbac.RoleFromConfig(rname)
		if err != nil {
			return fmt.Errorf("getting %s role: %w", rname, err)
		}

		role.SetResourceNames(creds[3:]...)

		// allow user to get their own user details
		role.AddPolicy(
			[]string{"users"},
			[]string{uname},
			[]string{"get"},
		)

		user.SetRole(role)

		log.Debug("creating default user - %+v", user)
	}

	router := mux.NewRouter().StrictSlash(true)

	var assets http.FileSystem

	if o.unbundled {
		assets = http.Dir("web/public")
		log.Info("Serving unbundled assets")
	} else {
		assets = &assetfs.AssetFS{
			Asset:     Asset,
			AssetDir:  AssetDir,
			AssetInfo: AssetInfo,
		}
	}

	router.HandleFunc("/builder", GetBuilder).Methods("GET")
	router.HandleFunc("/builder/save", SaveBuilderTopology).Methods("POST")

	log.Info("Setting up assets")

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
	api.HandleFunc("/vms", GetAllVMs).Methods("GET", "OPTIONS")
	api.HandleFunc("/applications", GetApplications).Methods("GET", "OPTIONS")
	api.HandleFunc("/topologies", GetTopologies).Methods("GET", "OPTIONS")
	api.HandleFunc("/topologies/{topo}/scenarios", GetScenarios).Methods("GET", "OPTIONS")
	api.HandleFunc("/disks", GetDisks).Methods("GET", "OPTIONS")
	api.HandleFunc("/hosts", GetClusterHosts).Methods("GET", "OPTIONS")
	api.HandleFunc("/logs", GetLogs).Methods("GET", "OPTIONS")
	api.HandleFunc("/users", GetUsers).Methods("GET", "OPTIONS")
	api.HandleFunc("/users", CreateUser).Methods("POST", "OPTIONS")
	api.HandleFunc("/users/{username}", GetUser).Methods("GET", "OPTIONS")
	api.HandleFunc("/users/{username}", UpdateUser).Methods("PATCH", "OPTIONS")
	api.HandleFunc("/users/{username}", DeleteUser).Methods("DELETE", "OPTIONS")
	api.HandleFunc("/signup", Signup).Methods("POST", "OPTIONS")
	api.HandleFunc("/login", Login).Methods("GET", "POST", "OPTIONS")
	api.HandleFunc("/logout", Logout).Methods("GET", "OPTIONS")
	api.Handle("/history", weberror.ErrorHandler(GetHistory)).Methods("POST", "OPTIONS")
	api.HandleFunc("/errors/{uuid}", GetError).Methods("GET", "OPTIONS")
	api.HandleFunc("/ws", broker.ServeWS).Methods("GET")
	api.Handle("/workflow/apply/{branch}", weberror.ErrorHandler(ApplyWorkflow)).Methods("POST", "OPTIONS")
	api.Handle("/workflow/configs/{branch}", weberror.ErrorHandler(WorkflowUpsertConfig)).Methods("POST", "OPTIONS")

	if o.allowCORS {
		log.Info("CORS is enabled on HTTP API endpoints")
		api.Use(middleware.AllowCORS)
	}

	switch o.logMiddleware {
	case "full":
		log.Info("full HTTP logging is enabled")
		api.Use(middleware.LogFull)
	case "requests":
		log.Info("requests-only HTTP logging is enabled")
		api.Use(middleware.LogRequests)
	}

	api.Use(middleware.AuthMiddleware(o.jwtKey))

	log.Info("Starting websockets broker")

	go broker.Start()

	log.Info("Starting scorch processors")

	go scorch.Start()

	log.Info("Starting log publisher")

	go PublishLogs(context.Background(), o.phenixLogs, o.minimegaLogs)

	log.Info("Using base path '%s'", o.basePath)

	if o.tlsEnabled() {
		log.Info("Starting HTTPS server on %s", o.endpoint)
		return http.ListenAndServeTLS(o.endpoint, o.tlsCrtPath, o.tlsKeyPath, router)
	} else {
		log.Info("Starting HTTP server on %s", o.endpoint)
		return http.ListenAndServe(o.endpoint, router)
	}
}
