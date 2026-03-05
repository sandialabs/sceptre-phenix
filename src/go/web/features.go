package web

import (
	"encoding/json"
	"net/http"

	"phenix/web/util"
)

// GetFeatures handles GET requests for /features.
func GetFeatures(w http.ResponseWriter, r *http.Request) {
	features := make([]string, 0, len(o.features))

	for f := range o.features {
		features = append(features, f)
	}

	body, _ := json.Marshal(util.WithRoot("features", features))
	_, _ = w.Write(body)
}
