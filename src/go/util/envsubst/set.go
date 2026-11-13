package envsubst

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"
)

// ShellFilterPrefix starts the names of the shell variables that a [Set] does
// not use, unless a flag names one. They hold the settings of phenix itself.
const ShellFilterPrefix = "PHENIX_"

var (
	// ErrReserved is wrapped by the error for a flag or an env file entry
	// that sets the reserved variable.
	ErrReserved = errors.New("reserved variable")

	// ErrNotInShell is wrapped by the error for a flag that names a variable
	// the shell does not have.
	ErrNotInShell = errors.New("variable is not set in the shell")

	// ErrBadFlag is wrapped by the error for a flag that is not NAME=value or
	// NAME.
	ErrBadFlag = errors.New("invalid variable")
)

// SourceKind is the kind of place that a variable value comes from.
type SourceKind int

// The sources of a [Set], from the highest precedence to the lowest.
const (
	SourceFlag SourceKind = iota + 1
	SourceEnvFile
	SourceShell
)

// Source is where a variable value comes from. Path is the env file of a
// [SourceEnvFile] value.
type Source struct {
	Kind SourceKind
	Path string
}

func (s Source) String() string {
	switch s.Kind {
	case SourceFlag:
		return "flag"
	case SourceEnvFile:
		return "env file " + s.Path
	case SourceShell:
		return "shell"
	default:
		return "unknown"
	}
}

// Var is a variable of a [Set].
type Var struct {
	Name, Value string
	Source      Source
}

// Options are the sources of a [Set].
type Options struct {
	// Flags are the -e values, NAME=value or NAME, in order.
	Flags []string
	// EnvFiles are the env files to read, in order.
	EnvFiles []string
	// DefaultEnvFile is read when EnvFiles is empty and the file exists.
	DefaultEnvFile string
	// Environ is the shell environment, as os.Environ gives it.
	Environ []string
	// Reserved is a variable that no source can set and that stays as written.
	Reserved string
}

// EnvFile is an env file that a [Set] read, with its number of entries.
type EnvFile struct {
	Path  string
	Count int
}

// Set holds the variables of a command: flags over env files over the shell.
type Set struct {
	vars     map[string]Var
	flags    []string
	files    []EnvFile
	filtered map[string]bool
	reserved string
}

// NewSet reads the sources of o. The error wraps [ErrBadFlag], [ErrReserved]
// or [ErrNotInShell] for a flag, is a [*ParseError] or wraps [ErrReserved] for
// an env file line, and wraps the file system error for a file it cannot read.
func NewSet(o Options) (*Set, error) {
	set := &Set{
		vars:     map[string]Var{},
		flags:    nil,
		files:    nil,
		filtered: map[string]bool{},
		reserved: o.Reserved,
	}

	shell := map[string]string{}

	for _, pair := range o.Environ {
		name, value, _ := strings.Cut(pair, "=")
		shell[name] = value

		switch {
		case name == o.Reserved:
		case strings.HasPrefix(name, ShellFilterPrefix):
			set.filtered[name] = true
		default:
			set.vars[name] = Var{Name: name, Value: value, Source: Source{Kind: SourceShell, Path: ""}}
		}
	}

	if err := set.readEnvFiles(o); err != nil {
		return nil, err
	}

	for _, flag := range o.Flags {
		if err := set.addFlag(flag, shell); err != nil {
			return nil, err
		}
	}

	return set, nil
}

// EnvFiles returns the env files that the set read, in order.
func (s *Set) EnvFiles() []EnvFile {
	return slices.Clone(s.files)
}

func (s *Set) readEnvFiles(o Options) error {
	paths := o.EnvFiles

	if len(paths) == 0 && o.DefaultEnvFile != "" {
		if _, err := os.Stat(o.DefaultEnvFile); !errors.Is(err, fs.ErrNotExist) {
			paths = []string{o.DefaultEnvFile}
		}
	}

	for _, path := range paths {
		if err := s.readEnvFile(path); err != nil {
			return err
		}
	}

	return nil
}

func (s *Set) readEnvFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("reading env file: %w", err)
	}

	defer func() { _ = file.Close() }()

	entries, err := ParseEnvFile(file)
	if err != nil {
		var parseErr *ParseError
		if errors.As(err, &parseErr) {
			parseErr.Path = path

			return parseErr
		}

		return fmt.Errorf("%s: %w", path, err)
	}

	for _, entry := range entries {
		if entry.Name == s.reserved {
			return fmt.Errorf("%s:%d: %w %s", path, entry.Line, ErrReserved, entry.Name)
		}

		s.vars[entry.Name] = Var{Name: entry.Name, Value: entry.Value, Source: Source{Kind: SourceEnvFile, Path: path}}
	}

	s.files = append(s.files, EnvFile{Path: path, Count: len(entries)})

	return nil
}

// addFlag sets the variable of one -e value. A value with no "=" names a
// shell variable, which the filter does not hide here.
func (s *Set) addFlag(flag string, shell map[string]string) error {
	name, value, assigned := strings.Cut(flag, "=")

	switch {
	case !ValidName(name):
		return fmt.Errorf("%w %q: use NAME=value or NAME", ErrBadFlag, name)
	case name == s.reserved:
		return fmt.Errorf("%w %s", ErrReserved, name)
	}

	if !assigned {
		var ok bool

		if value, ok = shell[name]; !ok {
			return fmt.Errorf("%w: %s", ErrNotInShell, name)
		}
	}

	if !slices.Contains(s.flags, name) {
		s.flags = append(s.flags, name)
	}

	s.vars[name] = Var{Name: name, Value: value, Source: Source{Kind: SourceFlag, Path: ""}}

	return nil
}

// File is a text to substitute, with the path that problems name.
type File struct {
	Path string
	Text []byte
}

// Warning is a default that starts with "-" or "?": the probable use of
// Docker Compose syntax, which phenix does not have.
type Warning struct {
	Path          string
	Line          int
	Name, Default string
}

// Result is what [Set.Substitute] gives: the files with their variables
// filled in, the variables that the files use, by name, and the warnings.
type Result struct {
	Files    []File
	Used     []Var
	Warnings []Warning
}

// ProblemKind is the kind of a [Problem].
type ProblemKind int

// The kinds of a [Problem].
const (
	Undefined ProblemKind = iota + 1
	ValueLineBreak
	DefaultLineBreak
	UnusedFlag
)

// Problem is one reason that [Set.Substitute] refuses its files. Filtered
// marks an undefined variable that the shell has under the filtered prefix.
type Problem struct {
	Kind     ProblemKind
	Name     string
	Path     string
	Line     int
	Source   Source
	Filtered bool
}

func (p Problem) String() string {
	at := fmt.Sprintf("%s:%d: ", p.Path, p.Line)

	switch p.Kind {
	case Undefined:
		return at + "the variable " + p.Name + " is not set and has no default"
	case ValueLineBreak:
		return fmt.Sprintf("%sthe value of %s (from the %s) holds a line break", at, p.Name, p.Source)
	case DefaultLineBreak:
		return at + "the default of " + p.Name + " holds a line break"
	case UnusedFlag:
		return "no file uses the variable " + p.Name + ", which a flag sets"
	default:
		return at + "unknown problem with " + p.Name
	}
}

// Error is the error of [Set.Substitute]. It holds every problem of the
// files, in file and line order, and then the flags that no file uses.
type Error struct {
	Problems []Problem
}

func (e *Error) Error() string {
	lines := make([]string, 0, len(e.Problems))

	for _, problem := range e.Problems {
		lines = append(lines, problem.String())
	}

	return strings.Join(lines, "; ")
}

// Substitute fills in the variables of every file in one pass. It changes no
// file when any file has a problem: the error is then an [*Error].
func (s *Set) Substitute(files []File) (Result, error) {
	pass := substitution{set: s, used: map[string]bool{}, problems: nil, warnings: nil}
	out := make([]File, 0, len(files))

	for _, file := range files {
		out = append(out, File{Path: file.Path, Text: pass.file(file)})
	}

	for _, name := range s.flags {
		if !pass.used[name] {
			pass.problems = append(pass.problems, Problem{
				Kind: UnusedFlag, Name: name, Path: "", Line: 0, Source: s.vars[name].Source, Filtered: false,
			})
		}
	}

	if len(pass.problems) > 0 {
		return Result{}, &Error{Problems: pass.problems}
	}

	names := make([]string, 0, len(pass.used))
	for name := range pass.used {
		names = append(names, name)
	}

	slices.Sort(names)

	used := make([]Var, 0, len(names))
	for _, name := range names {
		used = append(used, s.vars[name])
	}

	return Result{Files: out, Used: used, Warnings: pass.warnings}, nil
}

// substitution is the state of one [Set.Substitute] call.
type substitution struct {
	set      *Set
	used     map[string]bool
	problems []Problem
	warnings []Warning
}

func (p *substitution) file(file File) []byte {
	return Replace(file.Text, func(tok Token) ([]byte, bool) {
		if tok.Name == p.set.reserved {
			return nil, false
		}

		if tok.Escaped {
			return tok.Literal(), true
		}

		return p.token(file.Path, tok), true
	})
}

// token returns the text for a placeholder, and records its problem, if any.
func (p *substitution) token(path string, tok Token) []byte {
	var none Source

	problem := func(kind ProblemKind, source Source) {
		p.problems = append(p.problems, Problem{
			Kind: kind, Name: tok.Name, Path: path, Line: tok.Line, Source: source,
			Filtered: kind == Undefined && p.set.filtered[tok.Name],
		})
	}

	if tok.HasDefault {
		if strings.ContainsAny(tok.Default, "\r\n") {
			problem(DefaultLineBreak, none)
		}

		if strings.HasPrefix(tok.Default, "-") || strings.HasPrefix(tok.Default, "?") {
			p.warnings = append(p.warnings, Warning{Path: path, Line: tok.Line, Name: tok.Name, Default: tok.Default})
		}
	}

	found, ok := p.set.vars[tok.Name]

	switch {
	case ok && strings.ContainsAny(found.Value, "\r\n"):
		p.used[tok.Name] = true

		problem(ValueLineBreak, found.Source)
	case ok:
		p.used[tok.Name] = true

		return []byte(found.Value)
	case !tok.HasDefault:
		problem(Undefined, none)
	}

	return []byte(tok.Default)
}
