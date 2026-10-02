package forward

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gorilla/mux"

	"phenix/util/plog"
	"phenix/web/util"
)

// tunnelerDir holds the phenix-tunneler builds offered for download, relative
// to the directory phenix runs in.
const tunnelerDir = "downloads/tunneler"

// ListTunnelers - GET /downloads/tunneler.
//
// Lists the builds that are actually installed, so the Tunneler page offers
// only downloads that exist. Without a downloads directory none are.
//
// A build may be a link to a file elsewhere, which deployments use to point at
// builds they install separately; a broken link or one to a directory is left
// out.
func ListTunnelers(w http.ResponseWriter, _ *http.Request) {
	entries, err := os.ReadDir(tunnelerDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		plog.Error(plog.TypeSystem, "listing tunneler downloads", "err", err)
		http.Error(w, "unable to list tunneler downloads", http.StatusInternalServerError)

		return
	}

	names := []string{}

	for _, entry := range entries {
		// only what GetTunneler serves
		if !strings.HasPrefix(entry.Name(), ".") && isBuild(filepath.Join(tunnelerDir, entry.Name())) {
			names = append(names, entry.Name())
		}
	}

	sort.Strings(names)

	body, _ := json.Marshal(map[string][]string{"files": names})

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

// isBuild reports whether path is, or links to, a regular file.
func isBuild(path string) bool {
	info, err := os.Stat(path)

	return err == nil && info.Mode().IsRegular()
}

// GetTunneler - GET /downloads/tunneler/{name}.
func GetTunneler(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]

	// a name directly in the downloads directory, and not a hidden file
	if name == "" || filepath.Base(name) != name || name[0] == '.' {
		http.Error(w, "invalid tunneler name", http.StatusBadRequest)

		return
	}

	path := filepath.Join(tunnelerDir, name)

	info, err := os.Stat(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		plog.Error(plog.TypeSystem, "reading tunneler for download", "name", name, "err", err)
		http.Error(w, "unable to read tunneler "+name, http.StatusInternalServerError)

		return
	}

	if err != nil || !info.Mode().IsRegular() {
		http.Error(w, "tunneler "+name+" is not installed on this server", http.StatusNotFound)

		return
	}

	file, err := os.Open(path)
	if err != nil {
		plog.Error(plog.TypeSystem, "opening tunneler for download", "name", name, "err", err)
		http.Error(w, "unable to open tunneler "+name, http.StatusInternalServerError)

		return
	}

	defer file.Close()

	w.Header().Set("Content-Disposition", util.Attachment(name))
	w.Header().Set("Content-Type", "application/octet-stream")

	http.ServeContent(w, r, name, info.ModTime(), file)
}
