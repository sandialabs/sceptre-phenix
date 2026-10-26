// Package metrics holds the Prometheus metrics phenix ui serves on
// /api/v1/metrics. Every label takes values from a set fixed in code (route
// templates, HTTP methods, status classes, minimega command names, job kinds,
// cache and flight group names), never an experiment, VM, user or file name,
// so the number of series stays bounded however phenix is used.
package metrics

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// Namespace starts the name of every phenix metric.
const Namespace = "phenix"

// Label values for an outcome, and for a value outside a label's fixed set.
const (
	Other = "other"
	OK    = "ok"
	Error = "error"
)

// Outcomes of a minimega command, for MinimegaCommand.
const (
	// CommandError is a command minimega answered with an error.
	CommandError = Error
	// CommandTimeout is a command that ran past its timeout and was abandoned.
	CommandTimeout = "timeout"
	// CommandLost is a command whose connection to minimega failed or was lost.
	CommandLost = "lost"
	// CommandNotSent is a command that gave up waiting for the shared connection.
	CommandNotSent = "not_sent"
)

// Outcomes of a flight.Group call, for Flight.
const (
	FlightRan    = "ran"
	FlightJoined = "joined"
)

// latencyBuckets, in seconds, reach from a cached answer to a slow mesh-wide
// minimega command or a large download.
//
//nolint:gochecknoglobals // constant buckets
var latencyBuckets = []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60, 300}

//nolint:gochecknoglobals // process-wide metrics
var (
	httpRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: Namespace, Subsystem: "http", Name: "requests_total",
		Help: "HTTP requests answered, by route template, method and status class.",
	}, []string{"route", "method", "code"})
	httpDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: Namespace, Subsystem: "http", Name: "request_duration_seconds",
		Help:    "How long HTTP requests took to answer, by route template and method; websockets are left out.",
		Buckets: latencyBuckets,
	}, []string{"route", "method"})
	httpInFlight = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: Namespace, Subsystem: "http", Name: "requests_in_flight",
		Help: "HTTP requests being answered, open websockets included.",
	})

	mmCommands = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: Namespace, Subsystem: "minimega", Name: "commands_total",
		Help: "minimega commands, by command name and outcome (ok, error, timeout, lost, not_sent).",
	}, []string{"command", "outcome"})
	mmDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: Namespace, Subsystem: "minimega", Name: "command_duration_seconds",
		Help:    "How long minimega took to answer the commands sent to it, by command name.",
		Buckets: latencyBuckets,
	}, []string{"command"})
	mmWait = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: Namespace, Subsystem: "minimega", Name: "connection_wait_seconds",
		Help:    "How long commands waited for the shared minimega connection.",
		Buckets: latencyBuckets,
	})

	flightCalls = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: Namespace, Subsystem: "flight", Name: "calls_total",
		Help: "Calls of shared minimega reads, by group, that ran the read or joined one already running.",
	}, []string{"group", "outcome"})

	cacheLookups = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: Namespace, Subsystem: "cache", Name: "lookups_total",
		Help: "Cache lookups, by cache, that found a reusable value (hit) or not (miss).",
	}, []string{"cache", "result"})

	jobsFinished = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: Namespace, Subsystem: "jobs", Name: "finished_total",
		Help: "Background jobs finished, by kind and final status.",
	}, []string{"kind", "status"})

	storeDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: Namespace, Subsystem: "store", Name: "operation_duration_seconds",
		Help:    "How long BoltDB store operations took, by operation and result.",
		Buckets: latencyBuckets,
	}, []string{"operation", "result"})

	// Registry holds every phenix metric, and the Go runtime and process
	// collectors.
	Registry = newRegistry()
)

func newRegistry() *prometheus.Registry {
	r := prometheus.NewRegistry()

	r.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		httpRequests, httpDuration, httpInFlight,
		mmCommands, mmDuration, mmWait,
		flightCalls, cacheLookups, jobsFinished, storeDuration,
	)

	return r
}

// HTTPStarted counts a request being answered, until the returned function
// is called.
func HTTPStarted() func() {
	httpInFlight.Inc()

	return httpInFlight.Dec
}

// HTTPRequest records a request answered with status, for the route template
// it matched ("unmatched" when none did). A request whose connection was
// hijacked, such as a websocket, is counted but its duration is not.
func HTTPRequest(route, method string, status int, took time.Duration, hijacked bool) {
	method = Method(method)

	httpRequests.WithLabelValues(route, method, StatusClass(status)).Inc()

	if !hijacked {
		httpDuration.WithLabelValues(route, method).Observe(took.Seconds())
	}
}

// Method is the method label of an HTTP method: the method itself when it is
// a standard one, and "other" otherwise.
func Method(method string) string {
	switch method {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "CONNECT", "TRACE":
		return method
	}

	return Other
}

// StatusClass is the code label of an HTTP status: its class, such as "2xx",
// or "other" outside 100 to 599.
func StatusClass(status int) string {
	const (
		minStatus = 100
		maxStatus = 599
		class     = 100
	)

	if status < minStatus || status > maxStatus {
		return Other
	}

	return strconv.Itoa(status/class) + "xx"
}

// MinimegaWaited records how long a command waited for the shared minimega
// connection.
func MinimegaWaited(took time.Duration) {
	mmWait.Observe(took.Seconds())
}

// MinimegaCommand records a minimega command, named by a value from a fixed
// set (see mmcli), with its outcome: OK or one of the Command constants. took
// is how long minimega took to answer; it is not recorded for a command that
// was never sent.
func MinimegaCommand(command, outcome string, took time.Duration) {
	mmCommands.WithLabelValues(command, outcome).Inc()

	if outcome != CommandNotSent {
		mmDuration.WithLabelValues(command).Observe(took.Seconds())
	}
}

// Flight records a call of the named flight group that ran its function
// (FlightRan) or joined a run in progress (FlightJoined).
func Flight(group, outcome string) {
	if group == "" {
		group = Other
	}

	flightCalls.WithLabelValues(group, outcome).Inc()
}

// CacheLookup records a lookup in the named cache.
func CacheLookup(cache string, hit bool) {
	result := "miss"
	if hit {
		result = "hit"
	}

	cacheLookups.WithLabelValues(cache, result).Inc()
}

// JobFinished records a background job of kind ending with status.
func JobFinished(kind, status string) {
	jobsFinished.WithLabelValues(kind, status).Inc()
}

// StoreOperation records a store operation, such as "get", that took took and
// failed when err is not nil.
func StoreOperation(operation string, took time.Duration, err error) {
	result := OK
	if err != nil {
		result = Error
	}

	storeDuration.WithLabelValues(operation, result).Observe(took.Seconds())
}
