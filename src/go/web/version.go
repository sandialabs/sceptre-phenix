package web

import (
	"encoding/json"
	"net/http"

	"phenix/version"
)

type Version struct {
	Commit    string `json:"commit"`
	Tag       string `json:"tag"`
	BuildDate string `json:"buildDate"`
}

// GetVersion handles GET requests for /version.
func GetVersion(w http.ResponseWriter, r *http.Request) {
	body, _ := json.Marshal(
		Version{Commit: version.Commit, Tag: version.Tag, BuildDate: version.Date},
	)
	_, _ = w.Write(body)
}
