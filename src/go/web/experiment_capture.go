package web

import (
	"errors"
	"io"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"

	expcapture "phenix/api/capture"
	"phenix/api/experiment"
	"phenix/api/livecapture"
	"phenix/store"
	"phenix/util/mm"
	"phenix/util/plog"
	"phenix/web/audit"
	"phenix/web/broker"
	bt "phenix/web/broker/brokertypes"
	"phenix/web/capability"
	"phenix/web/lifecycle"
	"phenix/web/middleware"
	"phenix/web/proto"
	"phenix/web/rbac"
	"phenix/web/util"
	"phenix/web/weberror"
)

//nolint:gochecknoinits // experiment hooks are registered once, before any runs
func init() {
	experiment.RegisterHook("stop", experimentCapturesStopped)
}

// experimentCapturesStopped tells the clients that every experiment capture of the stopped experiment exp has stopped,
// in one event named <exp>/*: minimega stops them as it clears the namespace, so none is left to list by ID.
func experimentCapturesStopped(_, exp string) {
	if !mm.CaptureFeatures() {
		return
	}

	// the stopped captures' files are complete now
	experiment.InvalidateFiles(exp)

	broker.Broadcast(
		bt.NewRequestPolicy("experiments/captures", "list", exp),
		bt.NewResource("experiment/capture", exp+"/*", "stop"),
		nil,
	)
}

// experimentCaptureReadAllowed reports whether the role may read the packets of the experiment captures of exp: get
// one (experiments/captures get) and read the experiment's files that it writes (experiments/files get).
func experimentCaptureReadAllowed(role rbac.Role, exp string) bool {
	return role.Allowed("experiments/captures", "get", exp) && role.Allowed("experiments/files", "get", exp)
}

// experimentCaptureAuditFields name the experiment capture id of exp, which writes file, in an audit event.
func experimentCaptureAuditFields(exp, id, file string) audit.Fields {
	f := audit.Fields{Experiment: exp, File: file}

	if id != "" {
		f.Extra = []any{"phenix.capture.id", id}
	}

	return f
}

// experimentCaptureError is the error to answer for err, from an experiment capture call of exp.
func experimentCaptureError(exp, id string, err error) *weberror.WebError {
	switch {
	case errors.Is(err, expcapture.ErrInvalid), errors.Is(err, livecapture.ErrUnsafePath):
		return weberror.New(http.StatusBadRequest, err.Error())
	case errors.Is(err, expcapture.ErrNotRunning), errors.Is(err, expcapture.ErrBudget),
		errors.Is(err, expcapture.ErrPathInUse):
		return weberror.New(http.StatusConflict, err.Error())
	case errors.Is(err, mm.ErrCaptureUnsupported):
		return weberror.New(http.StatusNotImplemented, err.Error())
	case errors.Is(err, expcapture.ErrNotFound), errors.Is(err, livecapture.ErrNotCapturing):
		return weberror.New(http.StatusNotFound, "experiment capture "+id+" not found in experiment "+exp)
	case errors.Is(err, store.ErrNotExist):
		return experimentError(exp, err)
	}

	if status := fileErrorStatus(err); status != http.StatusInternalServerError {
		return weberror.New(status, err.Error())
	}

	return weberror.New(http.StatusInternalServerError, err.Error())
}

// answerExperimentCaptureError is the error to answer request r with for err, from an experiment capture call of exp
// on the capture id (empty for none): nil for a request the client left.
func answerExperimentCaptureError(r *http.Request, exp, id string, err error) error {
	if requestCanceled(r, err) {
		return nil
	}

	werr := experimentCaptureError(exp, id, err)
	if werr.Status == http.StatusInternalServerError {
		plog.Error(plog.TypeSystem, "experiment capture call", "exp", exp, "capture", id, "url", r.URL.Path, "err", err)
	}

	return werr
}

// experimentCaptureFailed audits err of an experiment capture call as action a, and returns what to answer r with.
func experimentCaptureFailed(r *http.Request, a audit.Action, exp, id string, err error) error {
	audit.Failure(r, a, "experiment capture call failed", experimentCaptureAuditFields(exp, id, ""), err)

	return answerExperimentCaptureError(r, exp, id, err)
}

// writeExperimentCapture answers status with the experiment capture c of exp.
func writeExperimentCapture(w http.ResponseWriter, exp string, c mm.NamespaceCapture, status int) error {
	body, err := marshaler.Marshal(util.ExperimentCaptureToProtobuf(c, expcapture.File(exp, c)))
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	w.Header().Set("Content-Type", mimeJSON)
	w.WriteHeader(status)
	_, _ = w.Write(body)

	return nil
}

// experimentCaptureList is the experiment captures, the capture support and the mirror budget of exp, which runs
// when running is set, for its capture list. Experiment captures or a budget that cannot be read give the reason, not
// an error; only a request that the client left fails.
func experimentCaptureList(r *http.Request, exp string, running bool) (*proto.CaptureList, error) {
	support := mm.CaptureSupportStatus()

	list := &proto.CaptureList{
		Captures:                nil,
		ExperimentCaptures:      []*proto.ExperimentCapture{},
		Support:                 &proto.CaptureSupport{Available: support.Available, Reason: support.Reason, Version: support.Version},
		Budget:                  []*proto.MirrorBudget{},
		BudgetError:             "",
		ExperimentCapturesError: "",
	}

	if !running || !support.Available {
		return list, nil
	}

	ctx := r.Context()

	captures, err := expcapture.List(ctx, exp)
	if err != nil {
		if endedWith(ctx, err) {
			return nil, err //nolint:wrapcheck // the client left
		}

		plog.Warn(plog.TypeSystem, "listing experiment captures", "exp", exp, "err", err)

		list.ExperimentCapturesError = err.Error()
	}

	for _, c := range captures {
		list.ExperimentCaptures = append(list.ExperimentCaptures, util.ExperimentCaptureToProtobuf(c, expcapture.File(exp, c)))
	}

	budgets, err := expcapture.Budgets(ctx, exp)
	if err != nil {
		if endedWith(ctx, err) {
			return nil, err //nolint:wrapcheck // the client left
		}

		list.BudgetError = err.Error()

		return list, nil
	}

	for _, b := range budgets {
		list.Budget = append(list.Budget, &proto.MirrorBudget{
			Host:       b.Host,
			Bridge:     b.Bridge,
			Total:      uint32(b.Total),      //nolint:gosec // G115 a count of mirrors
			Experiment: uint32(b.Experiment), //nolint:gosec // G115 a count of mirrors
			Limit:      uint32(b.Limit),      //nolint:gosec // G115 the mirror limit
			Level:      b.Level,
		})
	}

	return list, nil
}

// StartExperimentCapture - POST /experiments/{exp}/captures.
func StartExperimentCapture(w http.ResponseWriter, r *http.Request) error {
	var (
		role = middleware.RoleFromContext(r.Context())
		exp  = mux.Vars(r)["exp"]
	)

	if !role.Allowed("experiments/captures", "create", exp) {
		audit.Denied(r, audit.CaptureStart, "starting experiment capture not allowed", audit.Fields{Experiment: exp})

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		plog.Error(plog.TypeSystem, "reading request body", "err", err)

		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	var req proto.StartExperimentCaptureRequest
	if err := unmarshaler.Unmarshal(body, &req); err != nil {
		return weberror.New(http.StatusBadRequest, err.Error())
	}

	change, cancel := changeContext(r, vmsChangeTimeout)
	defer cancel()

	started, err := expcapture.Start(change, exp, expcapture.StartRequest{
		VLANs:   req.GetVlans(),
		Filter:  req.GetFilter(),
		Snaplen: int(req.GetSnaplen()),
		Name:    req.GetName(),
		Format:  req.GetFormat(),
	}, o.captureMaxSize)
	if err != nil {
		return experimentCaptureFailed(r, audit.CaptureStart, exp, "", err)
	}

	c := started.Capture

	// the capture writes to the experiment's files directory
	experiment.InvalidateFiles(exp)

	pb := util.ExperimentCaptureToProtobuf(c, expcapture.File(exp, c))

	event, _ := marshaler.Marshal(pb)
	broker.Broadcast(
		bt.NewRequestPolicy("experiments/captures", "list", exp),
		bt.NewResource("experiment/capture", exp+"/"+c.ID, "start"),
		event,
	)

	audit.Success(r, audit.CaptureStart, "experiment capture started", experimentCaptureAuditFields(exp, c.ID, pb.GetFile()))

	warnings := started.Warnings
	if warnings == nil {
		warnings = []string{}
	}

	resp, err := marshaler.Marshal(&proto.StartExperimentCaptureResponse{Capture: pb, Warnings: warnings})
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	w.Header().Set("Content-Type", mimeJSON)
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write(resp)

	return nil
}

// GetExperimentCapture - GET /experiments/{exp}/captures/{id}.
func GetExperimentCapture(w http.ResponseWriter, r *http.Request) error {
	var (
		role = middleware.RoleFromContext(r.Context())
		vars = mux.Vars(r)
		exp  = vars["exp"]
		id   = vars["id"]
	)

	if !role.Allowed("experiments/captures", "get", exp) {
		return weberror.New(http.StatusForbidden, "forbidden")
	}

	c, err := expcapture.Get(r.Context(), exp, id)
	if err != nil {
		return answerExperimentCaptureError(r, exp, id, err)
	}

	return writeExperimentCapture(w, exp, c, http.StatusOK)
}

// StopExperimentCapture - DELETE /experiments/{exp}/captures/{id}.
func StopExperimentCapture(w http.ResponseWriter, r *http.Request) error {
	var (
		role   = middleware.RoleFromContext(r.Context())
		vars   = mux.Vars(r)
		exp    = vars["exp"]
		id     = vars["id"]
		fields = experimentCaptureAuditFields(exp, id, "")
	)

	if !role.Allowed("experiments/captures", "delete", exp) {
		audit.Denied(r, audit.CaptureStop, "stopping experiment capture not allowed", fields)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	change, cancel := changeContext(r, vmsChangeTimeout)
	defer cancel()

	c, err := expcapture.Stop(change, exp, id)
	if err != nil {
		return experimentCaptureFailed(r, audit.CaptureStop, exp, id, err)
	}

	// the stopped capture's file is now complete
	experiment.InvalidateFiles(exp)

	revokeCaptureGrants("experiment capture stopped", func(g capability.Grant) bool {
		return g.Target.Exp == exp && g.Target.Capture == id && g.Allows(capability.OpStream)
	}, "exp", exp, "capture", id)

	pb := util.ExperimentCaptureToProtobuf(c, expcapture.File(exp, c))

	event, _ := marshaler.Marshal(pb)
	broker.Broadcast(
		bt.NewRequestPolicy("experiments/captures", "list", exp),
		bt.NewResource("experiment/capture", exp+"/"+id, "stop"),
		event,
	)

	audit.Success(r, audit.CaptureStop, "experiment capture stopped", experimentCaptureAuditFields(exp, id, pb.GetFile()))

	return writeExperimentCapture(w, exp, c, http.StatusOK)
}

// GetExperimentCaptureStream - GET /experiments/{exp}/captures/{id}/stream
//
// Streams a running experiment capture as GetVMCaptureStream streams a VM interface's.
func GetExperimentCaptureStream(w http.ResponseWriter, r *http.Request) error {
	var (
		role    = middleware.RoleFromContext(r.Context())
		vars    = mux.Vars(r)
		exp     = vars["exp"]
		id      = vars["id"]
		fromNow = streamsFromNow(r)
		fields  = experimentCaptureAuditFields(exp, id, "")
	)

	if !experimentCaptureReadAllowed(role, exp) {
		audit.Denied(r, audit.CaptureStream, "streaming experiment capture not allowed", fields)

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	if websocket.IsWebSocketUpgrade(r) && lifecycle.RefuseWhileStopping(w) {
		return nil
	}

	target := capability.Target{Exp: exp, File: "", VM: "", Interface: 0, Capture: id, Live: true, Local: ""}

	tap, err := openCaptureTap(r, target, fields)
	if err != nil {
		return err
	}

	defer tap.Release()

	audit.Success(r, audit.CaptureStream, "streaming experiment capture", fields)

	if websocket.IsWebSocketUpgrade(r) {
		streamCaptureWebSocket(w, r, tap, fromNow)

		return nil
	}

	streamCapture(w, r, tap, fromNow)

	return nil
}
