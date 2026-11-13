package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"phenix/util/envsubst"
)

// configVarsHelp is the paragraph about variables in the help of a command
// that reads config files.
const configVarsHelp = `

  The command fills in the ${NAME} and ${NAME:default} placeholders of each
  file before it reads the file. A value comes from -e, else from an env
  file of --env-file, else from the shell. The shell gives no PHENIX_
  variable unless -e NAME names it. A placeholder with no value and no
  default is an error, and so is a -e variable that no file uses: the
  command then changes nothing. Write ${NAME:} for an optional variable and
  $${NAME} for the literal text ${NAME}.`

var (
	// errConfigDirectory is wrapped by the error of a config command for an
	// argument that is a directory.
	errConfigDirectory = errors.New("give the configuration files")

	// errNotConfigFile is the error of a config command for a file with
	// another extension.
	errNotConfigFile = errors.New("not a .json, .yaml or .yml file")
)

// configInput is a config file that a config command reads, with its
// variables filled in, or the error that stopped the read. builder marks the
// refusal of a Builder file.
type configInput struct {
	path    string
	text    []byte
	err     error
	builder bool
}

// configVarsFromFlags reads the variable sources that the flags of cmd give.
func configVarsFromFlags(cmd *cobra.Command) (*envsubst.Set, error) {
	return newVarSet(varOptionsFromFlags(cmd))
}

// readConfigInputs reads the config files at paths: each one is a .json,
// .yaml or .yml file, and a Builder file is refused. A directory is the error
// of the call. It fills in the variables of every file in one pass, and
// returns the error of [substituteVars] when a variable has a problem.
func readConfigInputs(paths []string, vars *envsubst.Set) ([]configInput, error) {
	var (
		inputs = make([]configInput, 0, len(paths))
		files  []envsubst.File
		at     []int
	)

	for _, path := range paths {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			return nil, fmt.Errorf("%s is a directory: %w, for example %s", path, errConfigDirectory, filepath.Join(path, "*"))
		}
	}

	for _, path := range paths {
		in := readConfigInput(path)
		if in.err == nil {
			at = append(at, len(inputs))
			files = append(files, envsubst.File{Path: path, Text: in.text})
		}

		inputs = append(inputs, in)
	}

	// With no file to read, the error of each argument is the cause to
	// report, and not a -e variable that no file uses.
	if len(files) == 0 {
		return inputs, nil
	}

	files, err := substituteVars(vars, files)
	if err != nil {
		return nil, err
	}

	for i, index := range at {
		inputs[index].text = files[i].Text
	}

	return inputs, nil
}

// readConfigInput reads the config file at path as it is written. Its error
// is that of a file with another extension, of the read, or of a Builder file.
func readConfigInput(path string) configInput {
	switch filepath.Ext(path) {
	case ".json", ".yaml", ".yml":
	default:
		return configInput{path: path, text: nil, err: errNotConfigFile, builder: false}
	}

	text, err := os.ReadFile(path)
	if err != nil {
		return configInput{path: path, text: nil, err: fmt.Errorf("cannot read config: %w", err), builder: false}
	}

	if kind := builderFileKind(text); kind != "" {
		return configInput{path: path, text: nil, err: builderFileRefusal(kind, path), builder: true}
	}

	return configInput{path: path, text: text, err: nil, builder: false}
}
