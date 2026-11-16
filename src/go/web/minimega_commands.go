package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"phenix/api/minimega"
	"phenix/util/common"
	"phenix/web/audit"
	"phenix/web/middleware"
	"phenix/web/weberror"
)

// maxMinimegaCommandBytes is the longest command line POST /minimega/commands
// takes.
const maxMinimegaCommandBytes = 64 << 10

// minimegaCommandRequest is the body of POST /minimega/commands.
type minimegaCommandRequest struct {
	Command   string `json:"command"`
	Namespace string `json:"namespace"`
}

// minimegaCommandResponse is the answer of POST /minimega/commands.
type minimegaCommandResponse struct {
	minimega.Result

	Command string `json:"command"`
}

// RunMinimegaCommand - POST /minimega/commands.
//
// It sends one command line to minimega, in the namespace the body names if
// any, as phenix mm does, and answers with each host's answer and errors. It
// needs the minimega/commands create permission, which only Global Admin has
// of the built-in roles, and audits every command.
func RunMinimegaCommand(w http.ResponseWriter, r *http.Request) error {
	role := middleware.RoleFromContext(r.Context())

	if !role.Allowed("minimega/commands", "create") {
		audit.Denied(r, audit.MinimegaCommand, "running minimega commands not allowed", audit.Fields{})

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	var req minimegaCommandRequest

	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxFilesRequestBytes)).Decode(&req); err != nil {
		return weberror.New(http.StatusBadRequest, "invalid request body: "+err.Error())
	}

	switch {
	case strings.TrimSpace(req.Command) == "":
		return weberror.New(http.StatusBadRequest, "give the minimega command to run")
	case len(req.Command) > maxMinimegaCommandBytes:
		return weberror.New(http.StatusBadRequest, fmt.Sprintf("the command is longer than %d bytes", maxMinimegaCommandBytes))
	case strings.ContainsAny(req.Command, "\n\r"):
		return weberror.New(http.StatusBadRequest, "the command must be one line")
	}

	line := minimega.CommandLine(req.Namespace, []string{req.Command})
	sent := audit.Fields{Extra: []any{"phenix.minimega.command", line}}

	result, err := minimega.RunAt(r.Context(), common.MinimegaBase, line)

	switch {
	case requestCanceled(r, err):
		// minimega still runs a command it got
		audit.Failure(r, audit.MinimegaCommand, "running minimega command canceled", sent, err)

		return nil
	case errors.Is(err, minimega.ErrConnectionLost):
		audit.Failure(r, audit.MinimegaCommand, "running minimega command failed", sent, err)

		return weberror.NewWebError(err, "minimega did not answer").SetStatus(http.StatusBadGateway)
	case err != nil:
		audit.Failure(r, audit.MinimegaCommand, "minimega command reported errors", sent, err)
	default:
		audit.Success(r, audit.MinimegaCommand, "ran minimega command", sent)
	}

	body, err := json.Marshal(minimegaCommandResponse{Command: line, Result: result})
	if err != nil {
		return weberror.New(http.StatusInternalServerError, err.Error())
	}

	w.Header().Set("Content-Type", mimeJSON)
	_, _ = w.Write(body)

	return nil
}
