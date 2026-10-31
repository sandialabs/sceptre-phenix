// Package audit logs the security-relevant actions taken through the web API, and refusals, as plog AUDIT records whose
// fields follow the Elastic Common Schema as flat dotted keys. A handler calls Denied at each RBAC refusal, Failure when
// the authorized operation fails, and Success once it works; a background job is audited when it finishes, with
// Background or BackgroundFailure. docs/content/logging.md lists the actions.
package audit

import (
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"phenix/util/plog"
	"phenix/web/middleware"
)

// Outcome is an event's ECS event.outcome.
type Outcome string

const (
	OutcomeSuccess Outcome = "success"
	OutcomeFailure Outcome = "failure"
)

// The longest values of request fields a client chooses that an event holds.
const (
	maxForwardedIP = 64 // an IPv6 address with a zone and port fits
	maxPath        = 1024
)

// Action is an audited action, its ECS event.action.
type Action string

// The audited actions. TestActionsMatchDocs checks them against the table in
// docs/content/logging.md.
const (
	// Sign-in and sign-out.

	SignIn  Action = "sign-in"
	SignOut Action = "sign-out"

	// A request's token refused by the auth middleware (see TokenRefused).

	RequestAuth Action = "request-auth"

	// Users, their roles and API tokens.

	UserCreate         Action = "user-create"
	UserUpdate         Action = "user-update"
	UserRoleChange     Action = "user-role-change"
	UserPasswordChange Action = "user-password-change"
	UserDelete         Action = "user-delete"
	UserTokenCreate    Action = "user-token-create" //nolint:gosec // G101 an action name, not a credential

	// Configs, Role and User configs included.

	ConfigCreate Action = "config-create"
	ConfigUpdate Action = "config-update"
	ConfigDelete Action = "config-delete"

	// Workflows applied, and the server's settings.

	WorkflowApply  Action = "workflow-apply"
	SettingsUpdate Action = "settings-update"

	// Experiments and their apps.

	ExperimentCreate   Action = "experiment-create"
	ExperimentUpdate   Action = "experiment-update"
	ExperimentSchedule Action = "experiment-schedule"
	ExperimentDelete   Action = "experiment-delete"
	ExperimentStart    Action = "experiment-start"
	ExperimentStop     Action = "experiment-stop"
	AppTrigger         Action = "app-trigger"
	AppTriggerCancel   Action = "app-trigger-cancel"

	// SCORCH runs and terminals.

	ScorchStart        Action = "scorch-start"
	ScorchCancel       Action = "scorch-cancel"
	ScorchClear        Action = "scorch-clear"
	ScorchTerminalExit Action = "scorch-terminal-exit"

	// A VM's power.

	VMStart     Action = "vm-start"
	VMStop      Action = "vm-stop"
	VMShutdown  Action = "vm-shutdown"
	VMRestart   Action = "vm-restart"
	VMResetDisk Action = "vm-reset-disk"
	VMRedeploy  Action = "vm-redeploy"
	VMKill      Action = "vm-kill"

	// A VM's settings and optical disc.

	VMUpdate     Action = "vm-update"
	VMDiscChange Action = "vm-cdrom-change"
	VMDiscEject  Action = "vm-cdrom-eject"

	// Packet captures.

	CaptureStart        Action = "capture-start"
	CaptureStop         Action = "capture-stop"
	CaptureStream       Action = "capture-stream"
	CaptureWebShark     Action = "capture-webshark-open"
	CaptureWebSharkRead Action = "capture-webshark-read"
	CaptureTokenCreate  Action = "capture-token-create"
	CaptureTokenRevoke  Action = "capture-token-revoke"
	NetflowStart        Action = "netflow-start"
	NetflowStop         Action = "netflow-stop"
	NetflowDownload     Action = "netflow-download"

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

	// The server's log files.

	LogDownload Action = "log-download"
)

// ECS event.category and event.type values.
const (
	categoryAuthentication = "authentication"
	categoryConfiguration  = "configuration"
	categoryFile           = "file"
	categoryHost           = "host"
	categoryIAM            = "iam"
	categoryNetwork        = "network"
	categoryProcess        = "process"
	categorySession        = "session"

	typeAccess     = "access"
	typeChange     = "change"
	typeConnection = "connection"
	typeCreation   = "creation"
	typeDeletion   = "deletion"
	typeEnd        = "end"
	typeInfo       = "info"
	typeStart      = "start"
)

// kind is an action's ECS event.category and event.type.
type kind struct {
	category, typ string
}

// kinds are the audited actions' ECS event.category and event.type.
//
//nolint:gochecknoglobals // constant table
var kinds = map[Action]kind{
	SignIn:  {categoryAuthentication, typeStart},
	SignOut: {categoryAuthentication, typeEnd},

	RequestAuth: {categoryAuthentication, typeInfo},

	UserCreate:         {categoryIAM, typeCreation},
	UserUpdate:         {categoryIAM, typeChange},
	UserRoleChange:     {categoryIAM, typeChange},
	UserPasswordChange: {categoryIAM, typeChange},
	UserDelete:         {categoryIAM, typeDeletion},
	UserTokenCreate:    {categoryIAM, typeCreation},

	ConfigCreate: {categoryConfiguration, typeCreation},
	ConfigUpdate: {categoryConfiguration, typeChange},
	ConfigDelete: {categoryConfiguration, typeDeletion},

	WorkflowApply:  {categoryConfiguration, typeChange},
	SettingsUpdate: {categoryConfiguration, typeChange},

	ExperimentCreate:   {categoryConfiguration, typeCreation},
	ExperimentUpdate:   {categoryConfiguration, typeChange},
	ExperimentSchedule: {categoryConfiguration, typeChange},
	ExperimentDelete:   {categoryConfiguration, typeDeletion},
	ExperimentStart:    {categoryHost, typeStart},
	ExperimentStop:     {categoryHost, typeEnd},
	AppTrigger:         {categoryProcess, typeStart},
	AppTriggerCancel:   {categoryProcess, typeEnd},

	ScorchStart:        {categoryProcess, typeStart},
	ScorchCancel:       {categoryProcess, typeEnd},
	ScorchClear:        {categoryProcess, typeChange},
	ScorchTerminalExit: {categoryProcess, typeEnd},

	VMStart:     {categoryHost, typeStart},
	VMStop:      {categoryHost, typeEnd},
	VMShutdown:  {categoryHost, typeEnd},
	VMRestart:   {categoryHost, typeChange},
	VMResetDisk: {categoryHost, typeChange},
	VMRedeploy:  {categoryHost, typeChange},
	VMKill:      {categoryHost, typeEnd},

	VMUpdate:     {categoryConfiguration, typeChange},
	VMDiscChange: {categoryHost, typeChange},
	VMDiscEject:  {categoryHost, typeChange},

	CaptureStart:        {categoryNetwork, typeStart},
	CaptureStop:         {categoryNetwork, typeEnd},
	CaptureStream:       {categoryNetwork, typeAccess},
	CaptureWebShark:     {categoryNetwork, typeAccess},
	CaptureWebSharkRead: {categoryNetwork, typeAccess},
	CaptureTokenCreate:  {categoryIAM, typeCreation},
	CaptureTokenRevoke:  {categoryIAM, typeDeletion},
	NetflowStart:        {categoryNetwork, typeStart},
	NetflowStop:         {categoryNetwork, typeEnd},
	NetflowDownload:     {categoryFile, typeAccess},

	FileDownload: {categoryFile, typeAccess},
	FileUpload:   {categoryFile, typeCreation},
	FileDelete:   {categoryFile, typeDeletion},

	ConsoleCreate:     {categoryProcess, typeStart},
	ConsoleAttach:     {categoryProcess, typeAccess},
	ConsoleTerminate:  {categoryProcess, typeEnd},
	ConsoleEnd:        {categoryProcess, typeEnd},
	VNCAttach:         {categorySession, typeStart},
	PortForwardAttach: {categoryNetwork, typeConnection},

	DiskSnapshot: {categoryFile, typeCreation},
	DiskClone:    {categoryFile, typeCreation},
	DiskRename:   {categoryFile, typeChange},
	DiskDelete:   {categoryFile, typeDeletion},
	DiskCommit:   {categoryFile, typeChange},
	DiskRebase:   {categoryFile, typeChange},
	DiskResize:   {categoryFile, typeChange},
	DiskUpload:   {categoryFile, typeCreation},
	DiskDownload: {categoryFile, typeAccess},

	VMSnapshot:       {categoryFile, typeCreation},
	VMRestore:        {categoryFile, typeChange},
	VMCommit:         {categoryFile, typeCreation},
	VMMemorySnapshot: {categoryFile, typeCreation},
	VMMount:          {categorySession, typeStart},
	VMUnmount:        {categorySession, typeEnd},
	VMFileUpload:     {categoryFile, typeCreation},
	VMFileCopy:       {categoryFile, typeCreation},

	LogDownload: {categoryFile, typeAccess},
}

// ecs is the action's ECS event.category and event.type.
func (a Action) ecs() (string, string) {
	k, ok := kinds[a]
	if !ok {
		return "", typeInfo
	}

	return k.category, k.typ
}

// Fields name what an action acted on. Zero values are left out.
type Fields struct {
	// User is the acting user (user.name) for a request whose context names
	// none, such as one a capture grant authorizes.
	User string

	// URLPath replaces the request's path (url.path) when that carries a
	// secret, such as a capture token.
	URLPath string

	TargetUser string // user.target.name, the user an action on users acted on
	Config     string // phenix.config.name, as Kind/name
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

	// DryRun marks an action only checked, not carried out (phenix.dry_run):
	// its event.type is info, so it never reads as a change.
	DryRun bool

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

// BackgroundFailure logs that an action taken for user outside any request,
// such as a background job's work, failed with err.
func BackgroundFailure(user string, a Action, msg string, f Fields, err error) {
	f.User = user

	emit(nil, event{level: slog.LevelWarn, action: a, denied: false, outcome: OutcomeFailure, msg: msg, err: err}, f)
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
	if f.DryRun {
		types = []string{typeInfo}
	}

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

	if f.DryRun {
		attrs = append(attrs, "phenix.dry_run", true)
	}

	attrs = append(attrs, actor(r, f.User, f.URLPath)...)
	attrs = append(attrs, subject(f)...)

	if e.err != nil {
		attrs = append(attrs, "error.message", e.err.Error())
	}

	plog.Audit(e.level, e.msg, attrs...)
}

// actor is the fields naming who acted, and from where: the request's user,
// or user when given, and the request's client and path, or path when given.
// user.name is present unless the request came over the Unix socket, which
// has no user or client address (phenix.auth.method is unix-socket).
func actor(r *http.Request, user, path string) []any {
	if r == nil {
		return []any{"user.name", user}
	}

	var (
		ctx    = r.Context()
		socket = middleware.FromUnixSocket(ctx)
		attrs  []any
	)

	if user == "" && !socket {
		user = middleware.UserFromContext(ctx)
	}

	if socket {
		attrs = append(attrs, "phenix.auth.method", "unix-socket")
	} else {
		attrs = append(attrs, "user.name", user)
	}

	if role := middleware.RoleFromContext(ctx); role.Spec != nil && role.Spec.Name != "" {
		attrs = append(attrs, "user.roles", []string{role.Spec.Name})
	}

	if !socket {
		attrs = append(attrs, "source.ip", remoteIP(r.RemoteAddr))
	}

	// unverified: any client can send it, of any length
	if first, _, _ := strings.Cut(r.Header.Get("X-Forwarded-For"), ","); strings.TrimSpace(first) != "" {
		attrs = append(attrs, "network.forwarded_ip", bounded(strings.TrimSpace(first), maxForwardedIP))
	}

	// the path only: a query can carry a token
	if path == "" {
		path = r.URL.Path
	}

	return append(attrs, "http.request.method", r.Method, "url.path", bounded(path, maxPath))
}

// bounded is s cut, on a rune boundary, to at most n bytes, so that a value
// a client chose cannot make an audit event large.
func bounded(s string, n int) string {
	if len(s) <= n {
		return s
	}

	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}

	return s[:n]
}

// subject is the fields naming what the action acted on.
func subject(f Fields) []any {
	var attrs []any

	if f.TargetUser != "" {
		attrs = append(attrs, "user.target.name", f.TargetUser)
	}

	if f.Config != "" {
		attrs = append(attrs, "phenix.config.name", f.Config)
	}

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
