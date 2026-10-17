// Package health runs the checks behind the server's /healthz and /readyz
// endpoints and answers them in the format of the IETF draft "Health Check
// Response Format for HTTP APIs" (draft-inadarei-api-health-check-06), with
// the media type application/health+json.
//
// Every observedValue keeps one JSON type per check key across runs: a run
// that cannot observe a value leaves it out rather than reporting it as a
// string, so tools that index the responses (such as Elasticsearch through
// Metricbeat) never see a key change type. Fractional values are [Float]s,
// which keep a decimal point when whole, so such a tool maps them as floats.
package health

import (
	"bytes"
	"fmt"
	"math"
	"strconv"
)

// MediaType is the media type of a health response.
const MediaType = "application/health+json"

// Float is an observed value with a fractional part. It always marshals
// with a decimal point ("42.0", not "42"), so a tool that maps a field from
// the first value it sees, such as Elasticsearch, maps it as a floating-point
// number rather than as an integer that truncates later values.
type Float float64

// MarshalJSON writes f as a JSON number with a decimal point.
func (f Float) MarshalJSON() ([]byte, error) {
	if math.IsNaN(float64(f)) || math.IsInf(float64(f), 0) {
		return nil, fmt.Errorf("health: unsupported observed value %v", float64(f)) //nolint:err113 // not matched by callers
	}

	b := strconv.AppendFloat(nil, float64(f), 'f', -1, 64) //nolint:mnd // bits of a float64
	if !bytes.ContainsRune(b, '.') {
		b = append(b, ".0"...)
	}

	return b, nil
}

// Status is the health of a check, or of the whole service.
type Status string

const (
	// Pass is healthy.
	Pass Status = "pass"
	// Warn is healthy, with a problem worth a look.
	Warn Status = "warn"
	// Fail is unhealthy.
	Fail Status = "fail"
)

// Component types of the IETF draft.
const (
	TypeComponent = "component"
	TypeDatastore = "datastore"
	TypeSystem    = "system"
)

// rank orders statuses from best to worst.
func rank(s Status) int {
	switch s {
	case Pass:
		return 0
	case Warn:
		return 1
	case Fail:
		return 2 //nolint:mnd // the worst
	default:
		return 2 //nolint:mnd // anything unknown counts as a failure
	}
}

// Worst returns the worse of two statuses, counting an unknown status as a
// failure.
func Worst(a, b Status) Status {
	return []Status{Pass, Warn, Fail}[max(rank(a), rank(b))]
}

// Result is one observation of a check: one element of a response's
// checks["component:measurement"] array.
type Result struct {
	ComponentID   string `json:"componentId,omitempty"`
	ComponentType string `json:"componentType,omitempty"`
	ObservedValue any    `json:"observedValue,omitempty"`
	ObservedUnit  string `json:"observedUnit,omitempty"`
	Status        Status `json:"status"`
	// AffectedEndpoints are the API routes that do not work while the check
	// warns or fails. The draft has them only on warn and fail.
	AffectedEndpoints []string `json:"affectedEndpoints,omitempty"`
	// Time is when the value was observed, in RFC 3339 UTC.
	Time string `json:"time"`
	// Output is the reason for a warn or fail, or other detail. Only
	// authenticated requests see it.
	Output string `json:"output,omitempty"`
}

// Response is the body of a health endpoint.
type Response struct {
	Status      Status              `json:"status"`
	Version     string              `json:"version,omitempty"`
	ReleaseID   string              `json:"releaseId,omitempty"`
	ServiceID   string              `json:"serviceId"`
	Description string              `json:"description"`
	Notes       []string            `json:"notes,omitempty"`
	Output      string              `json:"output,omitempty"`
	Checks      map[string][]Result `json:"checks"`
}

// Redact clears the response's output and that of every check, for a request
// that is not authenticated.
func (r *Response) Redact() {
	r.Output = ""

	for key, results := range r.Checks {
		redacted := make([]Result, len(results))

		for i, result := range results {
			result.Output = ""
			redacted[i] = result
		}

		r.Checks[key] = redacted
	}
}
