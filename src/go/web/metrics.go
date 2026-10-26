package web

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"phenix/util/metrics"
	"phenix/util/plog"
	"phenix/web/broker"
	"phenix/web/middleware"
)

// The route labels of requests no route template matched: UI pages, which
// the router answers with index.html, and everything else.
const (
	routeUI        = "ui"
	routeUnmatched = "unmatched"
)

// metricsHandler writes the metrics in the Prometheus text format, leaving
// compression to CompressResponses.
//
//nolint:gochecknoglobals // one handler for the process's registry
var metricsHandler = promhttp.HandlerFor(metrics.Registry, promhttp.HandlerOpts{
	DisableCompression: true,
})

// registerServerMetrics adds the websocket broker's and the job registry's
// numbers to the metrics, once per process.
//
//nolint:gochecknoglobals // once per process
var registerServerMetrics = sync.OnceFunc(func() {
	gauge := func(subsystem, name, help string, value func() float64) prometheus.Collector {
		return prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Namespace: metrics.Namespace, Subsystem: subsystem, Name: name, Help: help,
		}, value)
	}

	counter := func(subsystem, name, help string, value func() float64) prometheus.Collector {
		return prometheus.NewCounterFunc(prometheus.CounterOpts{
			Namespace: metrics.Namespace, Subsystem: subsystem, Name: name, Help: help,
		}, value)
	}

	metrics.Registry.MustRegister(
		gauge("websocket", "clients", "Websocket clients connected to the broker.", func() float64 {
			return float64(broker.CurrentStats().Clients)
		}),
		gauge("websocket", "queue_length", "Broadcasts waiting for the websocket broker.", func() float64 {
			return float64(broker.CurrentStats().Queued)
		}),
		gauge("websocket", "queue_capacity", "How many broadcasts the websocket broker's queue holds.", func() float64 {
			return float64(broker.CurrentStats().Capacity)
		}),
		counter("websocket", "slow_clients_dropped_total", "Websocket clients dropped for being too slow.", func() float64 {
			clients, _ := broker.Dropped()

			return float64(clients)
		}),
		counter("websocket", "log_lines_dropped_total", "Log lines dropped from the live log stream.", func() float64 {
			_, lines := broker.Dropped()

			return float64(lines)
		}),
		gauge("jobs", "running", "Background jobs running.", func() float64 {
			running, _ := jobs.Active()

			return float64(running)
		}),
		gauge("jobs", "queued", "Background jobs waiting to run.", func() float64 {
			_, queued := jobs.Active()

			return float64(queued)
		}),
	)
})

// GetMetrics serves the server's Prometheus metrics to a role with the
// metrics get permission.
func GetMetrics(w http.ResponseWriter, r *http.Request) {
	role := middleware.RoleFromContext(r.Context())

	if !role.Allowed("metrics", "get") {
		plog.Warn(plog.TypeSecurity, "getting metrics not allowed", "user", middleware.UserFromContext(r.Context()))
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	metricsHandler.ServeHTTP(w, r)
}

// routeKey is the context key of where recordRoute notes the route template a
// request matched.
type routeKey struct{}

// observedRouter is a router that records every request it answers in the
// HTTP metrics, by the route template recordRoute notes, or "unmatched".
type observedRouter struct {
	*mux.Router
}

func (rt observedRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	done := metrics.HTTPStarted()
	defer done()

	var (
		route = new(string)
		sw    = &statusWriter{ResponseWriter: w, code: 0, hijacked: false}
		start = time.Now()
	)

	rt.Router.ServeHTTP(sw, r.WithContext(context.WithValue(r.Context(), routeKey{}, route)))

	if *route == "" {
		*route = routeUnmatched
	}

	metrics.HTTPRequest(*route, r.Method, sw.status(), time.Since(start), sw.hijacked)
}

// recordRoute notes the template of the route the request matched, such as
// /api/v1/experiments/{name}, for observedRouter.
func recordRoute(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if current := mux.CurrentRoute(r); current != nil {
			if template, err := current.GetPathTemplate(); err == nil {
				setRoute(r, template)
			}
		}

		next.ServeHTTP(w, r)
	})
}

// setRoute notes route as the label of the request for observedRouter.
func setRoute(r *http.Request, route string) {
	if p, ok := r.Context().Value(routeKey{}).(*string); ok {
		*p = route
	}
}

// statusWriter notes the status a handler answers with, and whether it took
// over the connection.
type statusWriter struct {
	http.ResponseWriter

	code     int
	hijacked bool
}

func (sw *statusWriter) WriteHeader(code int) {
	// informational answers other than a websocket's precede the final one
	if sw.code == 0 && (code >= http.StatusOK || code == http.StatusSwitchingProtocols) {
		sw.code = code
	}

	sw.ResponseWriter.WriteHeader(code)
}

func (sw *statusWriter) Write(b []byte) (int, error) {
	if sw.code == 0 {
		sw.code = http.StatusOK
	}

	return sw.ResponseWriter.Write(b) //nolint:wrapcheck // passthrough
}

// ReadFrom keeps the underlying writer's ReadFrom, so [http.ServeContent]
// still sends a file with sendfile.
func (sw *statusWriter) ReadFrom(src io.Reader) (int64, error) {
	if sw.code == 0 {
		sw.code = http.StatusOK
	}

	if rf, ok := sw.ResponseWriter.(io.ReaderFrom); ok {
		return rf.ReadFrom(src) //nolint:wrapcheck // passthrough
	}

	return io.Copy(writerOnly{sw.ResponseWriter}, src) //nolint:wrapcheck // passthrough
}

func (sw *statusWriter) Flush() {
	if f, ok := sw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (sw *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := sw.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errNotHijacker
	}

	sw.hijacked = true

	if sw.code == 0 {
		sw.code = http.StatusSwitchingProtocols
	}

	return hj.Hijack() //nolint:wrapcheck // passthrough
}

// Unwrap lets [http.ResponseController] reach the underlying writer.
func (sw *statusWriter) Unwrap() http.ResponseWriter {
	return sw.ResponseWriter
}

// status is the status the handler answered with: 200 when it wrote nothing,
// as net/http then sends.
func (sw *statusWriter) status() int {
	if sw.code == 0 {
		return http.StatusOK
	}

	return sw.code
}
