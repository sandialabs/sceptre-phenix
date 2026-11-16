package web

import (
	"encoding/json"
	"net/http"

	"phenix/web/util"
)

// GetFeatures handles GET requests for /features.
func GetFeatures(w http.ResponseWriter, r *http.Request) {
	body, _ := json.Marshal(util.WithRoot("features", o.featureNames()))

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}
