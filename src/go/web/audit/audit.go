// Package audit logs security-relevant actions taken through the web API:
// who read a capture, opened a console, changed a disk image or deleted a
// file, and who was refused. Each event is a plog record of type AUDIT whose
// fields follow the Elastic Common Schema (ECS) as flat dotted keys, so the
// JSON system log file can be shipped to Elasticsearch (with Filebeat, say)
// as it is.
//
// A handler calls Denied at each RBAC refusal, Failure when the authorized
// operation fails, and Success once it has worked. Validation errors raised
// before the RBAC check are not audited.
package audit

import (
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"strings"

	"phenix/util/plog"
	"phenix/web/middleware"
)

// Outcome is an event's ECS event.outcome.
type Outcome string

const (
	OutcomeSuccess Outcome = "success"
	OutcomeFailure Outcome = "failure"
	OutcomeUnknown Outcome = "unknown"
)

// Action is an audited action, its ECS event.action.
type Action string

// The audited actions. Keep the table in docs/content/logging.md in sync.
const (
	// Packet captures.

	CaptureStart        Action = "capture-start"
	CaptureStop         Action = "capture-stop"
	CaptureStream       Action = "capture-stream"
	CaptureWebShark     Action = "capture-webshark-open"
	CaptureWebSharkRead Action = "capture-webshark-read"
	CaptureTokenCreate  Action = "capture-token-create"
	CaptureTokenRevoke  Action = "capture-token-revoke"

	// Experiment files, and files in a mounted VM disk with the VM set.

	FileDownload Action = "file-download"
	FileUpload   Action = "file-upload"
	FileDelete   Action = "file-delete"

	// The minimega console, VNC and port forwards.

	ConsoleCreate     Action = "console-create"
	ConsoleAttach     Action = "console-attach"
	ConsoleTerminate  Action = "console-terminate"
	ConsoleEnd        Action = "console-end"
	VNCAttach         Action = "vnc-attach"
	PortForwardAttach Action = "port-forward-attach"

	// Disk images.

	DiskSnapshot Action = "disk-snapshot"
	DiskClone    Action = "disk-clone"
	DiskRename   Action = "disk-rename"
	DiskDelete   Action = "disk-delete"
	DiskCommit   Action = "disk-commit"
	DiskRebase   Action = "disk-rebase"
	DiskResize   Action = "disk-resize"
	DiskUpload   Action = "disk-upload"
	DiskDownload Action = "disk-download"

	// A VM's disks and mounted files.

	VMSnapshot       Action = "vm-snapshot"
	VMRestore        Action = "vm-restore"
	VMCommit         Action = "vm-commit"
	VMMemorySnapshot Action = "vm-memory-snapshot"
	VMMount          Action = "vm-mount"
	VMUnmount        Action = "vm-unmount"
	VMFileUpload     Action = "vm-file-upload"
	VMFileCopy       Action = "vm-file-copy"
)

// ECS event.category and event.type values.
const (
	categoryFile    = "file"
	categoryIAM     = "iam"
	categoryNetwork = "network"
	categoryProcess = "process"
	categorySession = "session"

	typeAccess     = "access"
	typeChange     = "change"
	typeConnection = "connection"
	typeCreation   = "creation"
	typeDeletion   = "deletion"
	typeEnd        = "end"
	typeInfo       = "info"
	typeStart      = "start"
)

// ecs is the action's ECS event.category and event.type.
func (a Action) ecs() (string, string) {
	switch a {
	case CaptureStart:
		return categoryNetwork, typeStart
	case CaptureStop:
		return categoryNetwork, typeEnd
	case CaptureStream, CaptureWebShark, CaptureWebSharkRead:
		return categoryNetwork, typeAccess
	case PortForwardAttach:
		return categoryNetwork, typeConnection
	case CaptureTokenCreate:
		return categoryIAM, typeCreation
	case CaptureTokenRevoke:
		return categoryIAM, typeDeletion
	case FileDownload, DiskDownload:
		return categoryFile, typeAccess
	case FileUpload, DiskSnapshot, DiskClone, DiskUpload, VMSnapshot, VMCommit, VMMemorySnapshot, VMFileUpload, VMFileCopy:
		return categoryFile, typeCreation
	case DiskRename, DiskCommit, DiskRebase, DiskResize, VMRestore:
		return categoryFile, typeChange
	case FileDelete, DiskDelete:
		return categoryFile, typeDeletion
	case ConsoleCreate:
		return categoryProcess, typeStart
	case ConsoleAttach:
		return categoryProcess, typeAccess
	case ConsoleTerminate, ConsoleEnd:
		return categoryProcess, typeEnd
	case VNCAttach, VMMount:
		return categorySession, typeStart
	case VMUnmount:
		return categorySession, typeEnd
	default:
		return "", typeInfo
	}
}

// Fields name what an action acted on. Zero values are left out.
type Fields struct {
	// User is the acting user (user.name) for a request whose context names
	// none, such as one a capture grant authorizes.
	User string

	Experiment string // phenix.experiment.name
	VM         string // phenix.vm.name
	Interface  *int   // phenix.vm.interface

	// File is the file acted on (file.path, and its base name as file.name);
	// Target is a file the action wrote or named, such as a new image, a copy's
	// destination or a backing image (phenix.file.target_path).
	File   string
	Target string

	PID int // process.pid

	// Reason is a refusal's event.reason; Denied defaults it to "forbidden".
	Reason string

	// Extra holds further ECS or phenix.* key/value pairs.
	Extra []any
}

// Success logs that an authorized request's action worked.
func Success(r *http.Request, a Action, msg string, f Fields) {
	emit(r, event{level: slog.LevelInfo, action: a, denied: false, outcome: OutcomeSuccess, msg: msg, err: nil}, f)
}

// Denied logs that RBAC refused a request's action.
func Denied(r *http.Request, a Action, msg string, f Fields) {
	if f.Reason == "" {
		f.Reason = "forbidden"
	}

	emit(r, event{
		level: slog.LevelWarn, action: a, denied: true, outcome: OutcomeFailure, msg: msg, err: nil,
	}, f)
}

// Failure logs that an authorized request's action failed with err.
func Failure(r *http.Request, a Action, msg string, f Fields, err error) {
	emit(r, event{level: slog.LevelWarn, action: a, denied: false, outcome: OutcomeFailure, msg: msg, err: err}, f)
}

// Background logs an action taken for user outside any request, such as a
// console ending on its own.
func Background(user string, a Action, msg string, outcome Outcome, f Fields) {
	f.User = user

	emit(nil, event{level: slog.LevelInfo, action: a, denied: false, outcome: outcome, msg: msg, err: nil}, f)
}

// event is what an audit record says happened.
type event struct {
	level   slog.Level
	action  Action
	denied  bool
	outcome Outcome
	msg     string
	err     error
}

// emit logs the event, of request r (nil outside any request), with its ECS
// fields.
func emit(r *http.Request, e event, f Fields) {
	category, typ := e.action.ecs()

	types := []string{typ}
	if e.denied {
		types = append(types, "denied")
	}

	attrs := []any{
		"event.kind", "event",
		"event.module", "phenix",
		"event.dataset", "phenix.audit",
		"event.category", category,
		"event.type", types,
		"event.action", string(e.action),
		"event.outcome", string(e.outcome),
	}

	if f.Reason != "" {
		attrs = append(attrs, "event.reason", f.Reason)
	}

	attrs = append(attrs, actor(r, f.User)...)
	attrs = append(attrs, subject(f)...)

	if e.err != nil {
		attrs = append(attrs, "error.message", e.err.Error())
	}

	plog.Audit(e.level, e.msg, attrs...)
}

// actor is the fields naming who acted, and from where: the request's user,
// or user when given, and the request's client. user.name is always present.
func actor(r *http.Request, user string) []any {
	if r == nil {
		return []any{"user.name", user}
	}

	ctx := r.Context()

	if user == "" {
		user = middleware.UserFromContext(ctx)
	}

	attrs := []any{"user.name", user}

	if role := middleware.RoleFromContext(ctx); role.Spec != nil && role.Spec.Name != "" {
		attrs = append(attrs, "user.roles", []string{role.Spec.Name})
	}

	attrs = append(attrs, "source.ip", remoteIP(r.RemoteAddr))

	// unverified: any client can send it
	if first, _, _ := strings.Cut(r.Header.Get("X-Forwarded-For"), ","); strings.TrimSpace(first) != "" {
		attrs = append(attrs, "network.forwarded_ip", strings.TrimSpace(first))
	}

	// the path only: a query can carry a token
	return append(attrs, "http.request.method", r.Method, "url.path", r.URL.Path)
}

// subject is the fields naming what the action acted on.
func subject(f Fields) []any {
	var attrs []any

	if f.Experiment != "" {
		attrs = append(attrs, "phenix.experiment.name", f.Experiment)
	}

	if f.VM != "" {
		attrs = append(attrs, "phenix.vm.name", f.VM)
	}

	if f.Interface != nil {
		attrs = append(attrs, "phenix.vm.interface", *f.Interface)
	}

	if f.File != "" {
		attrs = append(attrs, "file.path", f.File, "file.name", filepath.Base(f.File))
	}

	if f.Target != "" {
		attrs = append(attrs, "phenix.file.target_path", f.Target)
	}

	if f.PID != 0 {
		attrs = append(attrs, "process.pid", f.PID)
	}

	return append(attrs, f.Extra...)
}

// remoteIP is the host part of a request's remote address, or the address
// as it is when it has no port.
func remoteIP(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}

	return host
}
