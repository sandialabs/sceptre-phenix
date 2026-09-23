package forward

import (
	"net/http"
	"os"
	"strconv"

	log "github.com/activeshadow/libminimega/minilog"
	"github.com/gorilla/mux"
)

// GetTunneler - GET /downloads/tunneler/{name}.
func GetTunneler(w http.ResponseWriter, r *http.Request) {
	log.Debug("GetTunneler HTTP handler called")

	var (
		vars = mux.Vars(r)
		name = vars["name"]
	)

	file, err := os.Open("downloads/tunneler/" + name) //nolint:gosec // Path traversal via taint analysis
	if err != nil {
		log.Error("opening tunneler file (%s) for download: %v", name, err)
		http.Error(w, "error opening file", http.StatusBadRequest)

		return
	}

	defer file.Close()

	// Names such as "." or ".." open a directory rather than a tunneler binary.
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		log.Error("tunneler download (%s) is not a regular file", name)
		http.Error(w, "error opening file", http.StatusBadRequest)

		return
	}

	w.Header().Set("Content-Disposition", "attachment; filename="+strconv.Quote(name))
	w.Header().Set("Content-Type", "application/octet-stream")

	http.ServeContent(w, r, "", info.ModTime(), file)
}
