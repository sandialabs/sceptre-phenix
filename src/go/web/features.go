package web

import (
	"encoding/json"
	"net/http"

	"phenix/web/util"
)

// GET /version
func GetFeatures(w http.ResponseWriter, r *http.Request) {
	features := o.features
	if features == nil {
		features = make([]string, 0)
	}

	body, _ := json.Marshal(util.WithRoot("features", features))
	w.Write(body)
}
