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

// GenerateFromTemplate executes the template with the given name using the
// given data. The result is written to the given writer. The templates used are
// located in the `phenix/tmpl/templates' directory. Each template will have a
// function with the signature `add(int, int)` available to it via a
// `template.FuncMap`. It returns any errors encountered while executing the
// template.
func GenerateFromTemplate(name string, data any, w io.Writer) error {
	funcs := template.FuncMap{
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
		"stringsJoin": strings.Join,
		"escapeNewline": func(s string) string {
			return strings.ReplaceAll(s, "\n", "\\n")
		},
	}

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
// given data. The result is written to the given file. Internally it calls
// `GenerateFromTemplate`. It returns any errors encountered while executing the
// template.
func CreateFileFromTemplate(name string, data any, filename string) (err error) {
	dir := filepath.Dir(filename)

	if err = os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("creating template path: %w", err)
	}

	var f *os.File

	f, err = os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("creating template file: %w", err)
	}

	defer func() {
		cerr := f.Close()
		if cerr != nil && err == nil {
			err = fmt.Errorf("closing template file: %w", cerr)
		}
	}()

	return GenerateFromTemplate(name, data, f)
}

// RestoreAsset restores an asset that is part of the embedded templates.
func RestoreAsset(dir, name string) error {
	content, err := templatesFS.ReadFile(path.Join("templates", name))
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name), content, 0o600)
}
