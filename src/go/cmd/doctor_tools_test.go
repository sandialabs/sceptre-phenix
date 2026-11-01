package cmd

import (
	"os/exec"
)

// lookPathIn finds only the programs named in found.
func lookPathIn(found map[string]string) func(string) (string, error) {
	return func(name string) (string, error) {
		if path, ok := found[name]; ok {
			return path, nil
		}

		return "", exec.ErrNotFound
	}
}
