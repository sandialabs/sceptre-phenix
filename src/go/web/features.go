package web

import (
	"encoding/json"
	"net/http"

	"phenix/web/util"
)

// GET /features
func GetFeatures(w http.ResponseWriter, r *http.Request) {
	features := make([]string, 0)

	for f := range o.features {
		features = append(features, f)
	}

	body, _ := json.Marshal(util.WithRoot("features", features))
	w.Write(body)
}
