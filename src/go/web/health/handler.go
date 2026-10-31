package health

import (
	"encoding/json"
	"net/http"
	"strings"

	"phenix/version"
	"phenix/web/weberror"
)

// ServiceID is the serviceId of every response.
const ServiceID = "phenix"

// OutputOmitted is the note of a response whose output fields were removed.
const OutputOmitted = "output fields omitted: request not authenticated"

// Handler answers GET and HEAD requests with the registry's health: 200 when
// it passes or warns, 503 when it fails. ?exclude=<check> (repeatable, or
// comma-separated) leaves checks out.
type Handler struct {
	Registry    *Registry
	Description string
	// Authenticated reports whether the request may see output fields.
	Authenticated func(*http.Request) bool
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var exclude []string

	for _, value := range r.URL.Query()["exclude"] {
		for name := range strings.SplitSeq(value, ",") {
			if name = strings.TrimSpace(name); name != "" {
				exclude = append(exclude, name)
			}
		}
	}

	resp := h.Registry.Evaluate(r.Context(), exclude...)

	resp.Version = version.Tag
	resp.ReleaseID = version.Commit
	resp.ServiceID = ServiceID
	resp.Description = h.Description

	if h.Authenticated == nil || !h.Authenticated(r) {
		resp.Redact()
		resp.Notes = append(resp.Notes, OutputOmitted)
	}

	status := http.StatusOK
	if resp.Status == Fail {
		status = http.StatusServiceUnavailable
	}

	w.Header().Set("Content-Type", MediaType)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	if r.Method == http.MethodHead {
		w.WriteHeader(status)

		return
	}

	body, err := json.Marshal(resp)
	if err != nil {
		weberror.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	w.WriteHeader(status)
	_, _ = w.Write(body)
}
