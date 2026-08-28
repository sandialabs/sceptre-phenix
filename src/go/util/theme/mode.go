package theme

import (
	"fmt"
	"strings"
)

type Mode string

const (
	System Mode = "system"
	Light  Mode = "light"
	Dark   Mode = "dark"
)

func Parse(value string) (Mode, error) {
	mode := Mode(strings.ToLower(strings.TrimSpace(value)))

	switch mode {
	case System, Light, Dark:
		return mode, nil
	default:
		return "", fmt.Errorf("theme must be one of system, light, or dark, received %q", value)
	}
}

func Values() []string {
	return []string{string(System), string(Light), string(Dark)}
}
