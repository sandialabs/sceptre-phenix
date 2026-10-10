package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"phenix/util/common"
)

// Exit statuses of the commands that call the Builder API. Any other failure
// of phenix ends it with 1 as well.
const (
	// exitFindings: the server answered, and what it found is a failure: a
	// draft with errors that block publishing, or a failed preflight check.
	exitFindings = 1
	// exitRefused: the request could not be made or was refused: no
	// connection, authentication, permission, not found or invalid input.
	exitRefused = 2
)

// What the Builder commands send and read.
const (
	// builderTokenHeader carries the API token as "Bearer <token>", the
	// header phenix reads, not Authorization.
	builderTokenHeader = "X-Phenix-Auth-Token"

	// builderAPIPath is the path of the API below the server's URL.
	builderAPIPath = "/api/v1"

	// builderSocketBase is the URL requests over the unix socket are sent
	// to: the host is never resolved, since every connection dials the
	// socket.
	builderSocketBase = "http://unix"

	// builderRequestTimeout bounds each request, its answer included.
	builderRequestTimeout = 5 * time.Minute

	// builderDialTimeout bounds making a connection and its TLS handshake.
	builderDialTimeout = 30 * time.Second

	// builderMaxAnswerBytes bounds an answer: the largest a Builder package
	// or a template listing may reasonably be.
	builderMaxAnswerBytes = 256 << 20

	// builderMaxErrorBytes bounds what is read of an answer other than 2xx:
	// enough for a refusal that lists many issues, and little of a page that
	// a proxy or another server answers with.
	builderMaxErrorBytes = 64 << 10

	// builderMaxErrorText bounds the text shown of an error answer that is
	// not JSON, which is cut to its first line as well.
	builderMaxErrorText = 1 << 10

	// builderURLEnv and builderTokenEnv are the environment variables --url
	// and --token default to.
	builderURLEnv   = "PHENIX_URL"
	builderTokenEnv = "PHENIX_TOKEN"

	// builderURLFlag and builderTokenFlag are the flags every command that
	// calls the Builder API takes.
	builderURLFlag   = "url"
	builderTokenFlag = "token"

	// formatTable is the output format that prints tables for people.
	formatTable = "table"

	// builderYAMLIndent is the indent of the YAML the commands write.
	builderYAMLIndent = 2
)

// Severities of a Builder issue.
const (
	builderSeverityError   = "error"
	builderSeverityWarning = "warning"
)

// exitError is an error that ends phenix with its own exit status (see
// [Execute]). Its message is the wrapped error's.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string {
	return e.err.Error()
}

// Unwrap returns the wrapped error.
func (e *exitError) Unwrap() error {
	return e.err
}

// refused returns err as an error that ends phenix with [exitRefused],
// unless it already names an exit status. Nil stays nil.
func refused(err error) error {
	var exit *exitError

	if err == nil || errors.As(err, &exit) {
		return err
	}

	return &exitError{code: exitRefused, err: err}
}

// exitCode is the exit status phenix ends with after a command returned
// err: the status an [exitError] names, else 1.
func exitCode(err error) int {
	var exit *exitError

	if errors.As(err, &exit) {
		return exit.code
	}

	return 1
}

// builderIssue is one finding of the Builder, as its routes report it and as
// the commands print it in JSON and YAML. Only Severity and Message are
// always set.
type builderIssue struct {
	Code      string `json:"code,omitempty"`
	Severity  string `json:"severity"`
	Message   string `json:"message"`
	Path      string `json:"path,omitempty"`
	NodeID    string `json:"nodeId,omitempty"`
	EdgeID    string `json:"edgeId,omitempty"`
	NetworkID string `json:"networkId,omitempty"`
	Field     string `json:"field,omitempty"`
}

// newBuilderIssue returns an issue that names no element.
func newBuilderIssue(severity, code, message string) builderIssue {
	return builderIssue{
		Code: code, Severity: severity, Message: message, Path: "", NodeID: "", EdgeID: "", NetworkID: "", Field: "",
	}
}

// element names what the issue is about, for a table: the node, connection
// or network it names, else its path.
func (i builderIssue) element() string {
	switch {
	case i.NodeID != "":
		return "node " + i.NodeID
	case i.EdgeID != "":
		return "connection " + i.EdgeID
	case i.NetworkID != "":
		return "network " + i.NetworkID
	}

	return i.Path
}

// builderIssues reads issues as a route lists them: each a text, which
// becomes the message of an issue of the given severity, or an issue object,
// which gets that severity when it states none.
func builderIssues(entries []json.RawMessage, severity string) []builderIssue {
	issues := make([]builderIssue, 0, len(entries))

	for _, entry := range entries {
		var (
			text  string
			issue builderIssue
		)

		switch {
		case json.Unmarshal(entry, &text) == nil:
			issue = newBuilderIssue(severity, "", text)
		case json.Unmarshal(entry, &issue) == nil:
			if issue.Severity == "" {
				issue.Severity = severity
			}
		default:
			issue = newBuilderIssue(severity, "", string(entry))
		}

		issues = append(issues, issue)
	}

	return issues
}

// builderAPIError is an answer of the phenix server other than 2xx and 3xx,
// as its error body describes it. A body that is not JSON, such as the text
// the authentication of the server answers with, is the message.
type builderAPIError struct {
	Status  int
	Message string
	Cause   string
	Code    string
	Issues  []builderIssue
	// Hint says what to do about it, when the client can tell.
	Hint string
}

// newBuilderAPIError decodes the error body of an answer with the given
// status.
func newBuilderAPIError(status int, body []byte) *builderAPIError {
	var envelope struct {
		Message string            `json:"message"`
		Cause   string            `json:"cause"`
		Code    string            `json:"code"`
		Issues  []json.RawMessage `json:"issues"`
		Errors  []json.RawMessage `json:"errors"`
	}

	apiErr := &builderAPIError{Status: status, Message: "", Cause: "", Code: "", Issues: nil, Hint: ""}

	if err := json.Unmarshal(body, &envelope); err != nil {
		apiErr.Message = builderErrorText(body)

		return apiErr
	}

	apiErr.Message, apiErr.Cause, apiErr.Code = envelope.Message, envelope.Cause, envelope.Code
	apiErr.Issues = slices.Concat(
		builderIssues(envelope.Errors, builderSeverityError),
		builderIssues(envelope.Issues, builderSeverityError),
	)

	return apiErr
}

// builderErrorText is what is shown of an error body that is not JSON, such
// as the HTML page of a proxy: its first line that is not blank, cut to at
// most [builderMaxErrorText] bytes, and "..." when anything is left out.
func builderErrorText(body []byte) string {
	text := strings.TrimSpace(string(body))
	line, rest, _ := strings.Cut(text, "\n")
	line = strings.TrimSpace(line)

	if len(line) > builderMaxErrorText {
		cut := builderMaxErrorText
		for cut > 0 && !utf8.RuneStart(line[cut]) {
			cut--
		}

		line, rest = line[:cut], line[cut:]
	}

	if strings.TrimSpace(rest) != "" {
		line += " ..."
	}

	return line
}

// Error returns the status, the server's message and cause, each issue on a
// line of its own, and the hint.
func (e *builderAPIError) Error() string {
	var msg strings.Builder

	fmt.Fprintf(&msg, "phenix server answered %d %s", e.Status, http.StatusText(e.Status))

	if reason := e.reason(); reason != "" {
		msg.WriteString(": " + reason)
	}

	for _, issue := range e.Issues {
		msg.WriteString("\n  " + issueLine(issue))
	}

	if e.Hint != "" {
		msg.WriteString("\n" + e.Hint)
	}

	return msg.String()
}

// reason is the server's message, followed by its cause when the cause
// says more: the publish refusal of a document, for one, gives a general
// message and the reason in its cause.
func (e *builderAPIError) reason() string {
	switch {
	case e.Cause == "" || e.Cause == e.Message:
		return e.Message
	case e.Message == "":
		return e.Cause
	}

	return e.Message + ": " + e.Cause
}

// issueLine is an issue as one line of text: its element, its code and its
// message.
func issueLine(issue builderIssue) string {
	line := issue.Message

	if issue.Code != "" {
		line = "[" + issue.Code + "] " + line
	}

	if element := issue.element(); element != "" {
		line = element + ": " + line
	}

	return line
}

// builderClient calls the Builder REST API of a phenix server: at a URL, as
// the user whose API token it sends, or over the unix socket of phenix ui on
// this host, where every request acts as global-admin. It never prints the
// token.
type builderClient struct {
	http  *http.Client
	base  string
	token string
	// socket is the unix socket every request dials, "" for a URL.
	socket string
}

// newBuilderClient returns a client of the server at server, an http or
// https URL, sending token when it is not empty; or, when server is empty,
// of the phenix ui listening on the unix socket socket.
func newBuilderClient(server, token, socket string) (*builderClient, error) {
	var dialer net.Dialer

	dialer.Timeout = builderDialTimeout

	if server == "" {
		if socket == "" {
			return nil, fmt.Errorf("no phenix server to ask: give --url or %s, or --unix-socket", builderURLEnv)
		}

		if err := checkBuilderSocket(socket); err != nil {
			return nil, err
		}

		transport := &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return dialer.DialContext(ctx, "unix", socket)
			},
		}

		return &builderClient{
			http:   newBuilderHTTPClient(transport),
			base:   builderSocketBase,
			token:  "",
			socket: socket,
		}, nil
	}

	parsed, err := url.Parse(server)

	switch {
	case err != nil, parsed.Scheme != "http" && parsed.Scheme != "https", parsed.Host == "":
		return nil, fmt.Errorf("--url %q is not an http or https URL, such as https://phenix.example", server)
	case parsed.User != nil:
		return nil, errors.New("--url must not hold a user name or password: send an API token with --token")
	case parsed.RawQuery != "" || parsed.Fragment != "":
		return nil, fmt.Errorf("--url %q must not hold a query or a fragment", server)
	}

	transport := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         dialer.DialContext,
		TLSHandshakeTimeout: builderDialTimeout,
		ForceAttemptHTTP2:   true,
	}

	return &builderClient{
		http:   newBuilderHTTPClient(transport),
		base:   strings.TrimRight(parsed.String(), "/"),
		token:  token,
		socket: "",
	}, nil
}

// checkBuilderSocket refuses the unix socket at path unless phenix ui of this
// host may be what listens there (see [builderSocketProblem]). A path with
// nothing at it gets the error of a socket that cannot be dialed.
func checkBuilderSocket(path string) error {
	info, err := os.Lstat(path)

	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf(
			"%w: there is no unix socket at %s; start phenix ui on this host, or give the URL of a phenix server with --url or %s",
			errServerUnreachable, path, builderURLEnv,
		)
	case errors.Is(err, fs.ErrPermission):
		return fmt.Errorf("%w: %w; %s", errServerUnreachable, err, socketPermissionHint)
	case err != nil:
		return fmt.Errorf("%w: %w", errServerUnreachable, err)
	}

	return builderSocketProblem(path, info, os.Getuid())
}

// builderSocketProblem says why the file at path, which info describes, is
// not a socket the commands send requests to when the user uid runs them, or
// returns nil. The requests act as global-admin and the answers are trusted,
// so the file must be a socket, not a link or another kind of file, owned by
// that user or by root, and not writable by users outside its owner and its
// group: any user may create a socket in a directory such as /tmp, and a
// socket every user may write to lets any of them connect as global-admin.
// phenix ui --unix-socket-gid gives a group of users the socket, with mode
// 0770, which passes.
func builderSocketProblem(path string, info fs.FileInfo, uid int) error {
	const otherWrite = 0o002

	if info.Mode().Type() != fs.ModeSocket {
		return fmt.Errorf(
			"%s is not a unix socket (it is %s); give the socket phenix ui listens on with --unix-socket, "+
				"or the URL of a phenix server with --url or %s",
			path, fileTypeName(info.Mode()), builderURLEnv,
		)
	}

	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("the owner of unix socket %s cannot be read, so phenix does not send requests to it", path)
	}

	if owner := int64(stat.Uid); owner != int64(uid) && owner != 0 {
		return fmt.Errorf(
			"unix socket %s is owned by user ID %d, which is neither you nor root, so phenix ui may not be what "+
				"listens on it: give the socket phenix ui listens on with --unix-socket, or the URL of a phenix "+
				"server with --url or %s",
			path, owner, builderURLEnv,
		)
	}

	if mode := info.Mode().Perm(); mode&otherWrite != 0 {
		return fmt.Errorf(
			"unix socket %s can be written by every user of this host (mode %04o), so any of them could act "+
				"as global-admin through it: limit its mode, such as with phenix ui --unix-socket-gid, or give "+
				"the URL of a phenix server with --url or %s",
			path, mode, builderURLEnv,
		)
	}

	return nil
}

// fileTypeName names the type of file of mode, for an error.
func fileTypeName(mode fs.FileMode) string {
	switch {
	case mode&fs.ModeSymlink != 0:
		return "a symbolic link"
	case mode.IsDir():
		return "a directory"
	case mode.IsRegular():
		return "a regular file"
	}

	return "another kind of file"
}

// cleartextTokenWarning says that the token travels unencrypted when it is
// sent to an http URL of another host, and is "" otherwise: with no token,
// for https, and for localhost or a loopback address, which never leave this
// host. Labs often serve phenix over http on a private network, so this is a
// warning, not a refusal. It never holds the token.
func cleartextTokenWarning(server, token string) string {
	parsed, err := url.Parse(server)
	if token == "" || err != nil || parsed.Scheme != "http" {
		return ""
	}

	host := parsed.Hostname()

	if strings.EqualFold(host, "localhost") {
		return ""
	}

	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return ""
	}

	return fmt.Sprintf(
		"the token travels unencrypted to %s, since --url is http, not https: anyone on the network path can read it",
		host,
	)
}

// newBuilderHTTPClient returns the HTTP client of a [builderClient], which
// sends its requests through transport and follows no redirect: following
// one would send the token header to whatever server the redirect names, and
// would send a POST again as a GET. [builderClient.do] refuses a redirect
// instead.
func newBuilderHTTPClient(transport http.RoundTripper) *http.Client {
	return &http.Client{
		Transport: transport,
		Timeout:   builderRequestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// get sends a GET of path, below the API's path, and decodes the JSON answer
// into answer.
func (c *builderClient) get(ctx context.Context, path string, answer any) error {
	body, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}

	return decodeBuilderAnswer(path, body, answer)
}

// post sends request as JSON to path, below the API's path, and decodes the
// JSON answer into answer, unless answer is nil.
func (c *builderClient) post(ctx context.Context, path string, request, answer any) error {
	body, err := c.do(ctx, http.MethodPost, path, request)
	if err != nil || answer == nil {
		return err
	}

	return decodeBuilderAnswer(path, body, answer)
}

// do sends a request to path, below the API's path, with request as its JSON
// body unless it is nil, and returns the body of a 2xx answer. A redirect is
// refused (see [redirectRefusal]); any other answer is a *[builderAPIError];
// a request that got no answer wraps errServerUnreachable.
func (c *builderClient) do(ctx context.Context, method, path string, request any) ([]byte, error) {
	var body io.Reader = http.NoBody

	if request != nil {
		data, err := json.Marshal(request)
		if err != nil {
			return nil, fmt.Errorf("encoding the request: %w", err)
		}

		body = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.base+builderAPIPath+path, body)
	if err != nil {
		return nil, fmt.Errorf("building the request: %w", err)
	}

	req.Header.Set("Accept", mimeJSON)

	if request != nil {
		req.Header.Set("Content-Type", mimeJSON)
	}

	if c.token != "" {
		req.Header.Set(builderTokenHeader, "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, c.unreachable(err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode >= http.StatusMultipleChoices && resp.StatusCode < http.StatusBadRequest:
		return nil, redirectRefusal(method, path, resp)
	case resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices:
		// What could be read of it is all there is to show.
		text, _ := io.ReadAll(io.LimitReader(resp.Body, builderMaxErrorBytes))

		return nil, c.apiError(resp.StatusCode, text)
	}

	answer, err := io.ReadAll(io.LimitReader(resp.Body, builderMaxAnswerBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: reading the answer: %w", errServerUnreachable, err)
	}

	if len(answer) > builderMaxAnswerBytes {
		return nil, fmt.Errorf("the answer of the phenix server to %s %s is larger than %d bytes", method, path, builderMaxAnswerBytes)
	}

	return answer, nil
}

// redirectRefusal is the error of a redirect the server answered a request
// with, naming where it points, without its password if it holds one. The
// client follows no redirect, so the token goes to no other server.
func redirectRefusal(method, path string, resp *http.Response) error {
	target := "no location"
	if location, err := resp.Location(); err == nil {
		target = location.Redacted()
	}

	return fmt.Errorf(
		"the phenix server answered %s %s with %d %s, a redirect to %s, which phenix does not follow, "+
			"so that the token goes to no other server: give the URL the server answers at with --url or %s",
		method, path, resp.StatusCode, http.StatusText(resp.StatusCode), target, builderURLEnv,
	)
}

// apiError is the error of an answer other than 2xx, with what to do about
// a refused authentication.
func (c *builderClient) apiError(status int, body []byte) *builderAPIError {
	apiErr := newBuilderAPIError(status, body)

	switch {
	case c.socket != "":
	case status == http.StatusUnauthorized:
		apiErr.Hint = "The server refused the token: check --token or " + builderTokenEnv +
			". A user creates tokens in the Users tab of the phenix web UI."
	case status == http.StatusForbidden && c.token == "":
		apiErr.Hint = "No token was sent: give --token or " + builderTokenEnv + " when the server has sign-in on."
	}

	return apiErr
}

// unreachable is the error of a request that got no answer. It wraps
// errServerUnreachable, and says what to do when the unix socket cannot be
// dialed.
func (c *builderClient) unreachable(err error) error {
	switch {
	case c.socket == "":
		return fmt.Errorf("%w: %w", errServerUnreachable, err)
	case errors.Is(err, fs.ErrPermission):
		return fmt.Errorf("%w: %w; %s", errServerUnreachable, err, socketPermissionHint)
	}

	return fmt.Errorf(
		"%w: %w; start phenix ui on this host, or give the URL of a phenix server with --url or %s",
		errServerUnreachable, err, builderURLEnv,
	)
}

// decodeBuilderAnswer decodes the JSON answer to a request of path.
func decodeBuilderAnswer(path string, body []byte, answer any) error {
	if err := json.Unmarshal(body, answer); err != nil {
		return fmt.Errorf("the answer of the phenix server to %s is not what this phenix reads: %w", path, err)
	}

	return nil
}

// addBuilderServerFlags adds the flags of the server a command asks. They
// are the command's own, not inherited, so its help shows them: the help of
// phenix builder hides inherited flags.
func addBuilderServerFlags(cmd *cobra.Command) {
	cmd.Flags().String(
		builderURLFlag, "",
		"URL of the phenix server (default: $"+builderURLEnv+"; without either, the unix socket of phenix ui on this host)",
	)
	cmd.Flags().String(
		builderTokenFlag, "",
		"API token of the user the requests act as (default: $"+builderTokenEnv+"); needs --url",
	)
}

// builderClientFor returns the client of the server cmd's flags, their
// environment variables or the global --unix-socket name. A token without a
// URL is refused rather than left unsent: over the socket, every request acts
// as global-admin, not as the token's user. A token sent unencrypted to
// another host is a warning on standard error (see [cleartextTokenWarning]).
func builderClientFor(cmd *cobra.Command) (*builderClient, error) {
	server := builderSetting(cmd, builderURLFlag, builderURLEnv)
	token := builderSetting(cmd, builderTokenFlag, builderTokenEnv)

	socket := ""

	if server == "" {
		if token != "" {
			return nil, refused(fmt.Errorf(
				"--token needs --url; without --url the commands use the local socket as global-admin "+
					"(%s counts as --token, and %s as --url)",
				builderTokenEnv, builderURLEnv,
			))
		}

		socket = common.UnixSocket
	}

	client, err := newBuilderClient(server, token, socket)
	if err != nil {
		return nil, refused(err)
	}

	if warning := cleartextTokenWarning(server, token); warning != "" {
		fmt.Fprintln(cmd.ErrOrStderr(), "warning: "+warning)
	}

	return client, nil
}

// builderSetting is the value of the flag, else of the environment variable.
// A flag given an empty value counts as not given, so that a flag fed from
// an unset variable, such as --token "$CI_TOKEN", leaves the environment's
// value in place.
func builderSetting(cmd *cobra.Command, flag, env string) string {
	if value := cmd.Flags().Lookup(flag); value != nil && value.Value.String() != "" {
		return value.Value.String()
	}

	return os.Getenv(env)
}

// builderOutputFormat returns the -o flag's value, when it is table, json or
// yaml.
func builderOutputFormat(cmd *cobra.Command) (string, error) {
	format := MustGetString(cmd.Flags(), "output")
	formats := []string{formatTable, FormatJSON, FormatYAML}

	if slices.Contains(formats, format) {
		return format, nil
	}

	return "", refused(fmt.Errorf("-o %q is not one of %s", format, strings.Join(formats, ", ")))
}

// writeBuilderOutput writes report as format asks: indented JSON, YAML with
// the JSON's keys in the same order, or, for the table format, what table
// writes.
func writeBuilderOutput(out io.Writer, format string, report any, table func(io.Writer)) error {
	var text []byte

	switch format {
	case formatTable:
		table(out)

		return nil
	case FormatYAML:
		data, err := json.Marshal(report)
		if err != nil {
			return fmt.Errorf("encoding the output: %w", err)
		}

		if text, err = builderYAML(data); err != nil {
			return err
		}
	default:
		var buffer bytes.Buffer

		encoder := json.NewEncoder(&buffer)
		encoder.SetEscapeHTML(false)
		encoder.SetIndent("", "  ")

		if err := encoder.Encode(report); err != nil {
			return fmt.Errorf("encoding the output: %w", err)
		}

		text = buffer.Bytes()
	}

	if _, err := out.Write(text); err != nil {
		return fmt.Errorf("writing the output: %w", err)
	}

	return nil
}

// builderNonNil is s, or an empty slice when s is nil, so that JSON shows
// an empty list rather than null.
func builderNonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}

	return s
}

// builderYAML converts JSON text to YAML holding the same values, with the
// keys of each object in the same order: block style, indented by two
// spaces, and a string quoted only where YAML would read it as something
// else. The same JSON always gives the same YAML.
func builderYAML(data []byte) ([]byte, error) {
	var document yaml.Node

	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("converting to YAML: %w", err)
	}

	blockStyle(&document)

	var buffer bytes.Buffer

	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(builderYAMLIndent)

	if err := encoder.Encode(&document); err != nil {
		return nil, fmt.Errorf("converting to YAML: %w", err)
	}

	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("converting to YAML: %w", err)
	}

	return buffer.Bytes(), nil
}

// blockStyle drops the style JSON text gave node and the nodes below it:
// flow mappings and sequences, and double-quoted strings. The encoder then
// quotes only a string that would read as another type.
func blockStyle(node *yaml.Node) {
	node.Style = 0

	for _, child := range node.Content {
		blockStyle(child)
	}
}
