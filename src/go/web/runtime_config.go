package web

import (
	"bytes"
	"fmt"
	"html"
	"io"
	"net/http"
	"strings"
)

const runtimeConfigMarker = `<meta name="phenix-runtime-config" />`

func clientAuthMode(jwtKey string) string {
	switch {
	case jwtKey == "", strings.HasPrefix(jwtKey, "dev|"):
		return "disabled"
	case jwtKey == "proxy-jwt":
		return "proxy"
	default:
		return "enabled"
	}
}

func renderRuntimeIndex(index []byte, options serverOptions) ([]byte, error) {
	if bytes.Count(index, []byte(runtimeConfigMarker)) != 1 {
		return nil, fmt.Errorf("expected one runtime configuration marker in UI index")
	}

	basePath := html.EscapeString(options.basePath)
	config := fmt.Sprintf(
		`<base href="%s"><meta name="phenix-base-path" content="%s"><meta name="phenix-auth-mode" content="%s">`,
		basePath,
		basePath,
		clientAuthMode(options.jwtKey),
	)

	return bytes.Replace(index, []byte(runtimeConfigMarker), []byte(config), 1), nil
}

func serveRuntimeIndex(
	w http.ResponseWriter,
	r *http.Request,
	assets http.FileSystem,
	options serverOptions,
) error {
	file, err := assets.Open("index.html")
	if err != nil {
		return fmt.Errorf("opening index.html: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("statting index.html: %w", err)
	}

	index, err := io.ReadAll(file)
	if err != nil {
		return fmt.Errorf("reading index.html: %w", err)
	}

	index, err = renderRuntimeIndex(index, options)
	if err != nil {
		return fmt.Errorf("rendering index.html: %w", err)
	}

	// The injected configuration can change when phenix restarts with new flags.
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, "index.html", info.ModTime(), bytes.NewReader(index))

	return nil
}
