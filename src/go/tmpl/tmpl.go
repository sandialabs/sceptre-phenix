package tmpl

import (
	"embed"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
)

//go:embed templates
var templatesFS embed.FS

// escapeQuoted escapes s for a double-quoted minimega argument with the
// escapes minicli reads back (\\, \", \n, \r, \t), keeping the command on one line.
func escapeQuoted(s string) string {
	return strings.NewReplacer(
		`\`, `\\`,
		`"`, `\"`,
		"\n", `\n`,
		"\r", `\r`,
		"\t", `\t`,
	).Replace(s)
}

// templateFuncs returns the functions that GenerateFromTemplate gives to each template.
func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"addInt": func(a, b int) int {
			return a + b
		},
		"derefBool": func(b *bool) bool {
			if b == nil {
				return false
			}

			return *b
		},
		"cidrToMask": func(a string) string {
			_, ipv4Net, err := net.ParseCIDR(a)
			if err != nil {
				return "0.0.0.0"
			}

			// CIDR to four byte mask
			mask := ipv4Net.Mask

			return fmt.Sprintf("%d.%d.%d.%d", mask[0], mask[1], mask[2], mask[3])
		},
		"toBool": func(val any) bool {
			switch v := val.(type) {
			case string:
				b, err := strconv.ParseBool(v)
				if err != nil {
					return false
				}

				return b
			case int:
				return v != 0
			case bool:
				return v
			default:
				return false
			}
		},
		"stringsJoin":  strings.Join,
		"escapeQuoted": escapeQuoted,
	}
}

// GenerateFromTemplate executes the template with the given name using the
// given data. The result is written to the given writer. The templates used are
// located in the `phenix/tmpl/templates' directory. Each template will have a
// function with the signature `add(int, int)` available to it via a
// `template.FuncMap`. It returns any errors encountered while executing the
// template.
func GenerateFromTemplate(name string, data any, w io.Writer) error {
	funcs := templateFuncs()

	tplContent, err := templatesFS.ReadFile(path.Join("templates", name))
	if err != nil {
		return fmt.Errorf("reading template %s: %w", name, err)
	}

	tmpl := template.Must(template.New(name).Funcs(funcs).Parse(string(tplContent)))

	if err := tmpl.Execute(w, data); err != nil {
		return fmt.Errorf("executing %s template: %w", name, err)
	}

	return nil
}

// CreateFileFromTemplate executes the template with the given name using the
// given data. The result is written to the given file, with mode 0600. It
// renders to a temporary file in the same folder and then renames it over the
// file, so an error keeps the old file. Internally it calls
// `GenerateFromTemplate`. It returns any errors encountered.
func CreateFileFromTemplate(name string, data any, filename string) error {
	dir := filepath.Dir(filename)

	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("creating template path: %w", err)
	}

	f, err := os.CreateTemp(dir, ".tmpl-*.tmp")
	if err != nil {
		return fmt.Errorf("creating template file: %w", err)
	}

	err = GenerateFromTemplate(name, data, f)

	if cerr := f.Close(); cerr != nil && err == nil {
		err = fmt.Errorf("closing template file: %w", cerr)
	}

	if err == nil {
		if rerr := os.Rename(f.Name(), filename); rerr != nil { //nolint:gosec // G703 the temporary file it created
			err = fmt.Errorf("replacing template file: %w", rerr)
		}
	}

	if err != nil {
		_ = os.Remove(f.Name()) //nolint:gosec // G703 the temporary file it created

		return err
	}

	return nil
}

// RestoreAsset writes the embedded template asset name to the same relative path in dir,
// and makes the folders of that path first.
func RestoreAsset(dir, name string) error {
	content, err := templatesFS.ReadFile(path.Join("templates", name))
	if err != nil {
		return err
	}

	dst := filepath.Join(dir, filepath.FromSlash(name))

	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return fmt.Errorf("creating folder for asset %s: %w", name, err)
	}

	return os.WriteFile(dst, content, 0o600)
}
