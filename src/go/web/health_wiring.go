package web

import (
	"context"
	"errors"
	"net/http"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/activeshadow/libminimega/minicli"

	"phenix/store"
	"phenix/util/common"
	"phenix/util/mm"
	"phenix/util/mm/mmcli"
	"phenix/web/broker"
	"phenix/web/health"
	"phenix/web/middleware"
)

// startedAt is when the server started, for the uptime health check.
var startedAt = time.Now() //nolint:gochecknoglobals // set once by Start

// markStarted records the server's start for the uptime check.
func markStarted() {
	startedAt = time.Now()
}

// healthHandlers answer /healthz and /readyz. Each keeps its checks' cached
// results for the router's lifetime.
type healthHandlers struct {
	live, ready http.Handler
}

// newHealthHandlers builds the health endpoints from the server options:
//
//   - /healthz (liveness) reports uptime and whether the BoltDB store, a file
//     of this process's own, can be read.
//   - /readyz (readiness) reports everything phenix needs to run experiments.
//
// Only authenticated requests (or every request, with auth disabled) see the
// checks' output fields.
func newHealthHandlers() healthHandlers {
	var (
		authenticated = middleware.Authenticator(o.jwtKey, o.proxyAuthHeader)
		uptime        = health.Uptime(startedAt)
		storeCheck    = health.Store("bolt", listRoles)
		webshark      = o.featured("webshark")
	)

	ready := health.NewRegistry(
		uptime,
		health.GoRuntime(),
		storeCheck,
		health.Minimega(runMinimega),
		health.QemuImg(exec.LookPath, health.QemuImgVersion),
		health.WebShark(o.websharkDir, func() bool { return webshark }),
		health.Disk(diskPaths, health.Statfs),
		health.Broker(brokerStats),
	)

	return healthHandlers{
		live: health.Handler{
			Registry:      health.NewRegistry(uptime, storeCheck),
			Description:   "phēnix liveness",
			Authenticated: authenticated,
		},
		ready: health.Handler{
			Registry:      ready,
			Description:   "phēnix readiness",
			Authenticated: authenticated,
		},
	}
}

func listRoles() error {
	_, err := store.List("Role")

	return err //nolint:wrapcheck // the check says what it was doing
}

// runMinimega sends a command to minimega, outside any namespace, on a
// connection of its own bounded by ctx's deadline, so a health probe never
// waits behind other commands on the shared connection, and sends nothing
// once ctx is done.
func runMinimega(ctx context.Context, command string) ([]*minicli.Response, error) {
	cmd := mmcli.NewCommand()
	cmd.Command = command

	if deadline, ok := ctx.Deadline(); ok {
		cmd.Timeout = time.Until(deadline)
	}

	if cmd.Timeout <= 0 {
		return nil, context.DeadlineExceeded
	}

	var (
		resps []*minicli.Response
		err   error
	)

	// every response is read, so the connection is released
	for resp := range mmcli.RunContext(ctx, cmd) {
		for _, r := range resp.Resp {
			if r.Error != "" {
				if err == nil {
					err = errors.New(r.Error) //nolint:err113 // minimega's error text
				}

				continue
			}

			resps = append(resps, r)
		}
	}

	return resps, err
}

// diskPaths are the minimega files directory and the phenix base directory.
// Until minimega has given its files directory, the first is the fallback
// GetMMFullPath uses, so the disk check never waits on minimega itself.
func diskPaths() []health.DiskPath {
	filesDir := filepath.Join(common.PhenixBase, "images")

	select {
	case <-mm.FilesDirectoryGiven():
		filesDir = mm.GetMMFullPath("")
	default:
	}

	return []health.DiskPath{
		{ID: "files-dir", Path: filesDir},
		{ID: "phenix-base", Path: common.PhenixBase},
	}
}

func brokerStats() health.BrokerStats {
	s := broker.CurrentStats()

	return health.BrokerStats{Clients: int(s.Clients), Queued: s.Queued, Capacity: s.Capacity}
}
