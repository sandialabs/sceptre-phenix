package cmd

import (
	"errors"
	"fmt"
	"os"

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

// errNoConfigFiles is the error of an argument that holds no config file.
var errNoConfigFiles = errors.New("no JSON or YAML files found")

// configInput is a config file that a config command reads, with its
// variables filled in, or the error that stopped the read. For an argument
// that cannot be read, path is the argument. builder marks the refusal of a
// Builder file.
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

// readConfigInputs reads the JSON and YAML files at args, files or
// directories. It skips a Builder file in a directory and refuses one in
// args. It fills in the variables of every file in one pass, and returns the
// error of [substituteVars] when a variable has a problem.
func readConfigInputs(args []string, vars *envsubst.Set) ([]configInput, error) {
	var (
		inputs []configInput
		files  []envsubst.File
		at     []int
	)

	for _, arg := range args {
		paths, err := configFilesAt(arg)
		if err != nil {
			inputs = append(inputs, configInput{path: arg, text: nil, err: err, builder: false})

			continue
		}

		if len(paths) == 0 {
			inputs = append(inputs, configInput{path: arg, text: nil, err: errNoConfigFiles, builder: false})
		}

		for _, path := range paths {
			if kind := builderFileKind(path); kind != "" {
				if path == arg {
					inputs = append(inputs, configInput{path: path, text: nil, err: builderFileRefusal(kind, path), builder: true})
				} else {
					skipBuilderFile(kind, path)
				}

				continue
			}

			text, err := os.ReadFile(path)
			if err != nil {
				inputs = append(inputs, configInput{
					path: path, text: nil, err: fmt.Errorf("cannot read config: %w", err), builder: false,
				})

				continue
			}

			at = append(at, len(inputs))
			inputs = append(inputs, configInput{path: path, text: text, err: nil, builder: false})
			files = append(files, envsubst.File{Path: path, Text: text})
		}
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
