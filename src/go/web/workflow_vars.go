package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"

	"phenix/store"
	"phenix/types"
	"phenix/util/envsubst"
	"phenix/util/plog"
	"phenix/web/weberror"
)

const (
	// mimeMultipart is the media type of a request with a config part and a
	// variables part.
	mimeMultipart = "multipart/form-data"

	// configPart and variablesPart are the form names of the two parts.
	configPart    = "config"
	variablesPart = "variables"

	// maxVariablesPart is the largest variables part a request can send: 1 MiB.
	maxVariablesPart = 1 << 20

	// maxFilledTextMiB is the largest text after [FillVariables] and the branch
	// name fill it in, in MiB: as much as the largest builder document
	// (api/builder.MaxDocumentBytes). The largest topology in the docs is 19 KB.
	maxFilledTextMiB = 5
	maxFilledText    = maxFilledTextMiB << 20

	// maxVariableWarnings is the most warnings, one for each file, variable and
	// kind, that a request reports. The log and the response show no more.
	maxVariableWarnings = 50

	// maxLineBreakProblems is the most line break problems that the error of
	// [FillVariables] names. It counts the others.
	maxLineBreakProblems = 10

	// maxConfigLabel is the longest name of a request config in a warning.
	maxConfigLabel = 100

	// workflowConfigFile names the workflow config of an apply request in a
	// warning or an error.
	workflowConfigFile = "the workflow config"

	// requestConfigFile names the config of a configs request in an error,
	// before its kind and name are known.
	requestConfigFile = "the config"
)

var (
	// errVariableLineBreak is wrapped by the error of [FillVariables] when a
	// value or a default that a text uses holds a line break.
	errVariableLineBreak = errors.New("variable holds a line break")

	// errFilledTooLarge is wrapped by the error of [FillVariables] when the
	// filled text is larger than [maxFilledText].
	errFilledTooLarge = errors.New("filled text is too large")

	// errBadVariables is wrapped by the error of [parseRequestVariables].
	errBadVariables = errors.New("invalid variables part")
)

// VariableWarningKind is the lenient step that a [VariableWarning] reports.
type VariableWarningKind int

// The lenient steps of [FillVariables].
const (
	// FromServerEnv is a placeholder of an included file that got the value
	// of the server environment.
	FromServerEnv VariableWarningKind = iota + 1
	// KeptAsWritten is a placeholder of a request config that stays as written.
	KeptAsWritten
	// FilledEmpty is a placeholder of an included file that got an empty string.
	FilledEmpty
)

// VariableWarning is one lenient step of [FillVariables]: the file, the line
// and the name of the placeholder, and what the server did.
type VariableWarning struct {
	File string
	Line int
	Name string
	Kind VariableWarningKind
	// More counts the other lines of the same file with the same step for the
	// same variable. It is not part of the identity of a warning.
	More int
}

// String returns the warning as a sentence. It names no value.
func (w VariableWarning) String() string {
	at := fmt.Sprintf("%s line %d: ", w.File, w.Line)

	switch {
	case w.More == 1:
		at = fmt.Sprintf("%s line %d (and 1 other line): ", w.File, w.Line)
	case w.More > 1:
		at = fmt.Sprintf("%s line %d (and %d other lines): ", w.File, w.Line, w.More)
	}

	switch w.Kind {
	case FromServerEnv:
		return at + "the request has no variable " + w.Name + "; phenix used the value from the server environment"
	case KeptAsWritten:
		return at + "the request has no variable " + w.Name +
			" and the placeholder has no default; phenix kept the text ${" + w.Name + "}"
	case FilledEmpty:
		return at + "no variable " + w.Name + " in the request or in the server environment, " +
			"and the placeholder has no default; phenix used an empty string"
	default:
		return at + "unknown warning for the variable " + w.Name
	}
}

// FilledText is the result of [FillVariables]: the text, the sorted names of
// the request variables that it used, and a warning for each lenient step.
type FilledText struct {
	Text     []byte
	Used     []string
	Warnings []VariableWarning
}

// FillVariables fills in the ${NAME} placeholders of text, the file named file,
// with vars, the request variables. With a nil env, a placeholder that vars
// does not set gets its default or stays as written; else env comes next, and
// then the default or an empty string. ${BRANCH_NAME} stays as written, but the
// text is refused when it is larger than 5 MiB after branch also fills it in.
func FillVariables(text []byte, file, branch string, vars map[string]string, env envsubst.Lookup) (FilledText, error) {
	var (
		used     = map[string]bool{}
		warnings []VariableWarning
		problems lineBreaks
	)

	warn := func(tok envsubst.Token, kind VariableWarningKind) {
		warnings = append(warnings, VariableWarning{File: file, Line: tok.Line, Name: tok.Name, Kind: kind, More: 0})
	}

	fillToken := func(tok envsubst.Token) (string, bool) {
		switch {
		case tok.Name == store.BranchNameVar:
			return "", false
		case tok.Escaped:
			return tok.Raw[1:], true
		}

		if tok.HasDefault && envsubst.HasLineBreak(tok.Default) {
			problems.addf("%s line %d: the default of %s holds a line break", file, tok.Line, tok.Name)
		}

		if value, ok := vars[tok.Name]; ok {
			used[tok.Name] = true

			if envsubst.HasLineBreak(value) {
				problems.addf("%s line %d: the value of the request variable %s holds a line break", file, tok.Line, tok.Name)
			}

			return value, true
		}

		if env != nil {
			if value, ok := env(tok.Name); ok {
				warn(tok, FromServerEnv)

				return value, true
			}
		}

		switch {
		case tok.HasDefault:
			return tok.Default, true
		case env == nil:
			warn(tok, KeptAsWritten)

			return "", false
		default:
			warn(tok, FilledEmpty)

			return "", true
		}
	}

	var (
		tokens = envsubst.Tokens(text)
		fills  = make([]string, len(tokens))
		filled = make([]bool, len(tokens))
		size   = len(text)
	)

	for i, tok := range tokens {
		if fills[i], filled[i] = fillToken(tok); filled[i] {
			size += len(fills[i]) - len(tok.Raw)
		}
	}

	// The sizes are counted before each text is built. Only the ${BRANCH_NAME}
	// placeholders, which branch replaces later, make out larger than the final
	// text, and those of text add at most len(text). So this bound refuses no
	// final text of 5 MiB or less unless a value is part of such a placeholder.
	if size > maxFilledText+len(text) {
		return FilledText{}, filledTooLarge(file)
	}

	out := make([]byte, 0, size)
	last := 0

	for i, tok := range tokens {
		if filled[i] {
			out = append(append(out, text[last:tok.Start]...), fills[i]...)
			last = tok.End
		}
	}

	out = append(out, text[last:]...)

	if envsubst.ExpandOnlySize(out, store.BranchNameVar, branch) > maxFilledText {
		return FilledText{}, filledTooLarge(file)
	}

	if err := problems.err(); err != nil {
		return FilledText{}, err
	}

	return FilledText{Text: out, Used: slices.Sorted(maps.Keys(used)), Warnings: warnings}, nil
}

// filledTooLarge is the error of [FillVariables] for the file named file when
// its filled text is larger than [maxFilledText].
func filledTooLarge(file string) error {
	return fmt.Errorf("%w: %s is larger than %d MiB after the variables are filled in", errFilledTooLarge, file, maxFilledTextMiB)
}

// lineBreaks holds the first [maxLineBreakProblems] line break problems of a
// text, and counts the others.
type lineBreaks struct {
	first []string
	more  int
}

// addf adds a problem, which format and args describe.
func (l *lineBreaks) addf(format string, args ...any) {
	if len(l.first) < maxLineBreakProblems {
		l.first = append(l.first, fmt.Sprintf(format, args...))

		return
	}

	l.more++
}

// err returns the error that names the problems, or nil when there are none.
func (l *lineBreaks) err() error {
	if len(l.first) == 0 {
		return nil
	}

	text := strings.Join(l.first, "; ")
	if l.more > 0 {
		text += fmt.Sprintf("; and %d more", l.more)
	}

	return fmt.Errorf("%w: %s", errVariableLineBreak, text)
}

// isMultipartRequest reports whether the request Content-Type typ is
// multipart/form-data: a config part with a variables part.
func isMultipartRequest(typ string) bool {
	mediaType, _, _ := mime.ParseMediaType(typ)

	return mediaType == mimeMultipart
}

// multipartConfig is a request in the multipart format: the config text, its
// Content-Type, and the request variables.
type multipartConfig struct {
	text        []byte
	contentType string
	vars        map[string]string
}

// readMultipartConfig reads a multipart/form-data request with exactly one
// config part, of type application/json or application/x-yaml, and exactly
// one variables part, a JSON object of strings. Any other part is refused.
// The error is a 400 *weberror.WebError.
func readMultipartConfig(r *http.Request) (multipartConfig, error) {
	reader, err := r.MultipartReader()
	if err != nil {
		return multipartConfig{}, weberror.NewWebError(err, "unable to read the multipart request")
	}

	var (
		cfg                  multipartConfig
		haveConfig, haveVars bool
	)

	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return multipartConfig{}, weberror.NewWebError(err, "unable to read the multipart request")
		}

		switch name := part.FormName(); name {
		case configPart:
			if haveConfig {
				return multipartConfig{}, weberror.NewWebError(nil, "the request has more than one config part")
			}

			cfg.contentType = part.Header.Get("Content-Type")
			if cfg.contentType != mimeJSON && cfg.contentType != mimeYAML {
				return multipartConfig{}, weberror.NewWebError(
					nil, "the config part must use application/json or application/x-yaml, not %q", cfg.contentType,
				)
			}

			if cfg.text, err = io.ReadAll(part); err != nil {
				return multipartConfig{}, weberror.NewWebError(err, "unable to read the config part")
			}

			haveConfig = true
		case variablesPart:
			if haveVars {
				return multipartConfig{}, weberror.NewWebError(nil, "the request has more than one variables part")
			}

			data, err := io.ReadAll(io.LimitReader(part, maxVariablesPart+1))
			if err != nil {
				return multipartConfig{}, weberror.NewWebError(err, "unable to read the variables part")
			}

			if len(data) > maxVariablesPart {
				return multipartConfig{}, weberror.NewWebError(nil, "the variables part is larger than 1 MiB")
			}

			if cfg.vars, err = parseRequestVariables(data); err != nil {
				return multipartConfig{}, weberror.NewWebError(err, "invalid variables part")
			}

			haveVars = true
		default:
			return multipartConfig{}, weberror.NewWebError(
				nil, "the request has the unknown part %q; send only the config and variables parts", name,
			)
		}
	}

	switch {
	case !haveConfig:
		return multipartConfig{}, weberror.NewWebError(nil, "the request has no config part")
	case !haveVars:
		return multipartConfig{}, weberror.NewWebError(nil, "the request has no variables part")
	}

	return cfg, nil
}

// parseRequestVariables decodes data, a JSON object whose keys are variable
// names and whose values are strings. It refuses a duplicate key, a name that
// is not [envsubst.ValidName], and BRANCH_NAME. The error wraps
// [errBadVariables] and names no value.
func parseRequestVariables(data []byte) (map[string]string, error) {
	dec := json.NewDecoder(bytes.NewReader(data))

	notObject := fmt.Errorf("%w: the variables must be a JSON object of strings", errBadVariables)

	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, notObject
	}

	vars := map[string]string{}

	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return nil, notObject
		}

		name, _ := key.(string)

		tok, err := dec.Token()
		if err != nil {
			return nil, notObject
		}

		value, ok := tok.(string)

		switch _, seen := vars[name]; {
		case !ok:
			return nil, fmt.Errorf("%w: the value of the variable %q is not a JSON string", errBadVariables, name)
		case seen:
			return nil, fmt.Errorf("%w: the variable %q is set more than once", errBadVariables, name)
		case !envsubst.ValidName(name):
			return nil, fmt.Errorf("%w: %q is not a valid variable name", errBadVariables, name)
		case name == store.BranchNameVar:
			return nil, fmt.Errorf(
				"%w: %s cannot be a variable: the branch in the path sets the branch name", errBadVariables, name,
			)
		}

		vars[name] = value
	}

	if _, err := dec.Token(); err != nil {
		return nil, notObject
	}

	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: the variables part holds more than one JSON value", errBadVariables)
	}

	return vars, nil
}

// workflowVars is what the variables of one workflow request did: the
// request variables, nil for a plain request, the names that a text used,
// and the warnings, each once. It is not shared between requests.
type workflowVars struct {
	branch   string
	values   map[string]string
	used     map[string]bool
	warnings []VariableWarning
	seen     map[VariableWarning]bool
	groups   map[VariableWarning]int
	omitted  map[VariableWarning]bool
}

// newWorkflowVars returns the variables of a request for branch. values is
// nil for a plain request.
func newWorkflowVars(branch string, values map[string]string) *workflowVars {
	return &workflowVars{
		branch: branch, values: values, used: map[string]bool{}, warnings: nil,
		seen: map[VariableWarning]bool{}, groups: map[VariableWarning]int{}, omitted: map[VariableWarning]bool{},
	}
}

// fill fills in text, the file named file, with the request variables and
// records what it used and its warnings. ${BRANCH_NAME} stays as written. env
// is nil for a request config and the server environment for an included file.
func (v *workflowVars) fill(text []byte, file string, env envsubst.Lookup) ([]byte, error) {
	filled, err := FillVariables(text, file, v.branch, v.values, env)
	if err != nil {
		return nil, err
	}

	for _, name := range filled.Used {
		v.used[name] = true
	}

	for _, warning := range filled.Warnings {
		if v.seen[warning] {
			continue
		}

		v.seen[warning] = true

		// The warnings of one step for one variable in one file share an entry.
		group := VariableWarning{File: warning.File, Line: 0, Name: warning.Name, Kind: warning.Kind, More: 0}

		switch i, ok := v.groups[group]; {
		case ok:
			v.warnings[i].More++
		case len(v.warnings) < maxVariableWarnings:
			v.groups[group] = len(v.warnings)
			v.warnings = append(v.warnings, warning)
		default:
			v.omitted[group] = true
		}
	}

	return filled.Text, nil
}

// includeReader returns the reader of the topology files that the topology
// includes by a path. For a plain request it fills in ${BRANCH_NAME} with the
// branch and every other placeholder from the server environment, as before.
func (v *workflowVars) includeReader() types.IncludeReader {
	if v.values == nil {
		return types.ExpandIncludes(store.BranchNameLookup(v.branch))
	}

	return func(path string, text []byte) ([]byte, error) {
		filled, err := v.fill(text, path, os.LookupEnv)
		if err != nil {
			return nil, err
		}

		return store.ExpandBranchName(filled, envsubst.Fixed(v.branch)), nil
	}
}

// safeConfigLabel matches the Kind/name of a config that a warning can show:
// only the characters that a config name can hold, so no line break or escape.
var safeConfigLabel = regexp.MustCompile(`^[A-Za-z]+/[A-Za-z0-9_@.-]+$`)

// nameConfig names the request config file in the warnings so far, which
// [requestConfigFile] names until the kind and name are known. A name longer
// than [maxConfigLabel], or with a character outside [safeConfigLabel], stays
// unnamed: its config fails the checks, and no log line shows the name.
func (v *workflowVars) nameConfig(name string) {
	if len(name) > maxConfigLabel || !safeConfigLabel.MatchString(name) {
		return
	}

	for i := range v.warnings {
		if v.warnings[i].File == requestConfigFile {
			v.warnings[i].File = name
		}
	}
}

// usedNames returns the sorted names of the request variables that a text
// used, or nil when there are none.
func (v *workflowVars) usedNames() []string {
	if len(v.used) == 0 {
		return nil
	}

	return slices.Sorted(maps.Keys(v.used))
}

// warningTexts returns the warnings as sentences, in order, or nil.
func (v *workflowVars) warningTexts() []string {
	if len(v.warnings) == 0 {
		return nil
	}

	texts := make([]string, 0, len(v.warnings)+1)
	for _, warning := range v.warnings {
		texts = append(texts, warning.String())
	}

	if len(v.omitted) > 0 {
		texts = append(texts, v.omittedText())
	}

	return texts
}

// omittedText says how many warnings the request left out.
func (v *workflowVars) omittedText() string {
	return fmt.Sprintf(
		"phenix left out %d more warnings: it reports at most %d for each request", len(v.omitted), maxVariableWarnings,
	)
}

// logWarnings logs each warning as a warning record with the branch, the
// file, the line and the variable. No record holds a value.
func (v *workflowVars) logWarnings() {
	for _, warning := range v.warnings {
		plog.Warn(
			plog.TypeSystem, warning.String(),
			"branch", v.branch, "file", warning.File, "line", warning.Line, "variable", warning.Name,
		)
	}

	if len(v.omitted) > 0 {
		plog.Warn(plog.TypeSystem, v.omittedText(), "branch", v.branch)
	}
}

// auditVariables adds the used names to the extra audit fields, when there
// are any. A value is never audited.
func (v *workflowVars) auditVariables(extra []any) []any {
	if names := v.usedNames(); len(names) > 0 {
		return append(extra, "phenix.workflow.variables", strings.Join(names, ","))
	}

	return extra
}

// fillError is the 400 of a request whose variables could not be filled in.
func fillError(err error, what string) error {
	return weberror.NewWebError(err, "unable to fill in the variables of %s", what)
}
