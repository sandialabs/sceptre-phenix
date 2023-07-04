package forward

import (
	"net/http"
	"os"
	"time"

	log "github.com/activeshadow/libminimega/minilog"
	"github.com/gorilla/mux"
)

// GET /downloads/tunneler/{name}
func GetTunneler(w http.ResponseWriter, r *http.Request) {
	log.Debug("GetTunneler HTTP handler called")

	var (
		vars = mux.Vars(r)
		name = vars["name"]
	)

	file, err := os.Open("downloads/tunneler/" + name)
	if err != nil {
		log.Error("opening tunneler file (%s) for download: %v", name, err)
		http.Error(w, "error opening file", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Disposition", "attachment; filename="+name)
	w.Header().Set("Content-Type", "application/octet-stream")

	http.ServeContent(w, r, "", time.Now(), file)
}
