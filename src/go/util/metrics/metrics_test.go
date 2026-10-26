package metrics_test

import (
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"pgregory.net/rapid"

	"phenix/util/metrics"
	"phenix/util/metrics/metricstest"
)

func TestStatusClass(t *testing.T) {
	t.Parallel()

	tests := []struct {
		status int
		want   string
	}{
		{http.StatusSwitchingProtocols, "1xx"},
		{http.StatusOK, "2xx"},
		{http.StatusNoContent, "2xx"},
		{http.StatusMovedPermanently, "3xx"},
		{http.StatusNotFound, "4xx"},
		{http.StatusServiceUnavailable, "5xx"},
		{599, "5xx"},
		{0, metrics.Other},
		{99, metrics.Other},
		{600, metrics.Other},
	}

	for _, tt := range tests {
		if got := metrics.StatusClass(tt.status); got != tt.want {
			t.Errorf("StatusClass(%d) = %q, want %q", tt.status, got, tt.want)
		}
	}
}

func TestMethod(t *testing.T) {
	t.Parallel()

	tests := []struct {
		method, want string
	}{
		{"GET", "GET"},
		{"OPTIONS", "OPTIONS"},
		{"get", metrics.Other},
		{"PROPFIND", metrics.Other},
		{"", metrics.Other},
	}

	for _, tt := range tests {
		if got := metrics.Method(tt.method); got != tt.want {
			t.Errorf("Method(%q) = %q, want %q", tt.method, got, tt.want)
		}
	}
}

// Whatever a client sends, the method and status labels come from small fixed sets.
func TestHTTPLabelsAreBounded(t *testing.T) {
	t.Parallel()

	classes := []string{"1xx", "2xx", "3xx", "4xx", "5xx", metrics.Other}
	methods := []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "CONNECT", "TRACE", metrics.Other}

	rapid.Check(t, func(t *rapid.T) {
		status := rapid.Int().Draw(t, "status")
		method := rapid.String().Draw(t, "method")

		if got := metrics.StatusClass(status); !slices.Contains(classes, got) {
			t.Fatalf("StatusClass(%d) = %q, want one of %v", status, got, classes)
		}

		if got := metrics.Method(method); !slices.Contains(methods, got) {
			t.Fatalf("Method(%q) = %q, want one of %v", method, got, methods)
		}
	})
}

// The recorders move the metrics they name.
func TestRecordersMoveMetrics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		record func()
		metric string
		labels map[string]string
		// still names a metric, with its labels, that must not move
		still       string
		stillLabels map[string]string
	}{
		{
			name:   "HTTP request",
			record: func() { metrics.HTTPRequest("/api/v1/test-route", "GET", http.StatusOK, time.Millisecond, false) },
			metric: "phenix_http_request_duration_seconds",
			labels: map[string]string{"route": "/api/v1/test-route", "method": "GET"},
		},
		{
			name:        "hijacked HTTP request",
			record:      func() { metrics.HTTPRequest("/api/v1/test-ws", "GET", http.StatusSwitchingProtocols, 0, true) },
			metric:      "phenix_http_requests_total",
			labels:      map[string]string{"route": "/api/v1/test-ws", "method": "GET", "code": "1xx"},
			still:       "phenix_http_request_duration_seconds",
			stillLabels: map[string]string{"route": "/api/v1/test-ws", "method": "GET"},
		},
		{
			name:   "minimega command",
			record: func() { metrics.MinimegaCommand("vm test", metrics.CommandTimeout, time.Second) },
			metric: "phenix_minimega_commands_total",
			labels: map[string]string{"command": "vm test", "outcome": "timeout"},
		},
		{
			name:   "flight",
			record: func() { metrics.Flight("test-group", metrics.FlightJoined) },
			metric: "phenix_flight_calls_total",
			labels: map[string]string{"group": "test-group", "outcome": "joined"},
		},
		{
			name:   "cache",
			record: func() { metrics.CacheLookup("test-cache", true) },
			metric: "phenix_cache_lookups_total",
			labels: map[string]string{"cache": "test-cache", "result": "hit"},
		},
		{
			name:   "job",
			record: func() { metrics.JobFinished("test-kind", "failed") },
			metric: "phenix_jobs_finished_total",
			labels: map[string]string{"kind": "test-kind", "status": "failed"},
		},
		{
			name:   "store",
			record: func() { metrics.StoreOperation("test-op", time.Millisecond, errors.New("broken")) },
			metric: "phenix_store_operation_duration_seconds",
			labels: map[string]string{"operation": "test-op", "result": "error"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			before := metricstest.Value(t, tt.metric, tt.labels)

			var stillBefore float64
			if tt.still != "" {
				stillBefore = metricstest.Value(t, tt.still, tt.stillLabels)
			}

			tt.record()

			if got := metricstest.Value(t, tt.metric, tt.labels) - before; got != 1 {
				t.Errorf("%s %v moved by %v, want 1", tt.metric, tt.labels, got)
			}

			if tt.still != "" {
				if got := metricstest.Value(t, tt.still, tt.stillLabels) - stillBefore; got != 0 {
					t.Errorf("%s %v moved by %v, want 0", tt.still, tt.stillLabels, got)
				}
			}
		})
	}
}

// wantLabels are the label names of each phenix metric: names of things in
// experiments (experiments, VMs, users, files, hosts) are never labels, so the
// series stay bounded.
//
//nolint:gochecknoglobals // constant table
var wantLabels = map[string][]string{
	"phenix_http_requests_total":               {"code", "method", "route"},
	"phenix_http_request_duration_seconds":     {"method", "route"},
	"phenix_http_requests_in_flight":           nil,
	"phenix_minimega_commands_total":           {"command", "outcome"},
	"phenix_minimega_command_duration_seconds": {"command"},
	"phenix_minimega_connection_wait_seconds":  nil,
	"phenix_flight_calls_total":                {"group", "outcome"},
	"phenix_cache_lookups_total":               {"cache", "result"},
	"phenix_jobs_finished_total":               {"kind", "status"},
	"phenix_store_operation_duration_seconds":  {"operation", "result"},
}

// labelValue is what a bounded label value looks like: a route template, a
// command name, or a word from a fixed set.
var labelValue = regexp.MustCompile(`^[A-Za-z0-9_./{}: -]{1,96}$`)

// The phenix metrics carry only the labels wantLabels lists, with values from
// fixed sets, and the Go runtime and process collectors are registered.
func TestRegistryLabels(t *testing.T) {
	t.Parallel()

	metrics.HTTPRequest("/api/v1/guard", "GET", http.StatusOK, 0, false)
	metrics.MinimegaCommand("vm info", metrics.OK, 0)
	metrics.MinimegaWaited(0)
	metrics.Flight("guard", metrics.FlightRan)
	metrics.CacheLookup("guard", false)
	metrics.JobFinished("guard", "succeeded")
	metrics.StoreOperation("get", 0, nil)

	families, err := metrics.Registry.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}

	got := make(map[string][]string)
	var runtime, process bool

	for _, family := range families {
		name := family.GetName()

		runtime = runtime || strings.HasPrefix(name, "go_")
		process = process || strings.HasPrefix(name, "process_")

		if !strings.HasPrefix(name, metrics.Namespace+"_") {
			continue
		}

		for _, m := range family.GetMetric() {
			names := make([]string, 0, len(m.GetLabel()))

			for _, pair := range m.GetLabel() {
				names = append(names, pair.GetName())

				if !labelValue.MatchString(pair.GetValue()) {
					t.Errorf("%s label %s = %q, want a value matching %s", name, pair.GetName(), pair.GetValue(), labelValue)
				}
			}

			got[name] = names
		}
	}

	if diff := cmp.Diff(wantLabels, got, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("label names of the phenix metrics mismatch (-want +got):\n%s", diff)
	}

	if !runtime || !process {
		t.Errorf("Registry has Go runtime metrics %t and process metrics %t, want both", runtime, process)
	}
}
