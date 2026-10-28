// Command gendocs writes the phenix CLI reference: a Markdown page for the root command and one
// for each top-level command, and their entries in the mkdocs nav. Run it with `make cli-docs`.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"phenix/cmd"
)

func main() {
	out := flag.String("out", "", "directory to write the CLI reference pages to")
	mkdocs := flag.String("mkdocs", "", "mkdocs.yml whose CLI nav to update")

	flag.Parse()

	if err := run(*out, *mkdocs); err != nil {
		fmt.Fprintln(os.Stderr, "gendocs:", err)
		os.Exit(1)
	}
}

func run(out, mkdocs string) error {
	if out == "" || mkdocs == "" {
		return errors.New("both -out and -mkdocs are required")
	}

	pages, err := render(cmd.Root())
	if err != nil {
		return err
	}

	if err := writePages(out, pages); err != nil {
		return err
	}

	return updateNav(mkdocs, filepath.Base(out), pages)
}
