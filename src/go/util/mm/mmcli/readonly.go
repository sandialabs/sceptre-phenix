package mmcli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/sandia-minimega/minimega/v2/pkg/miniclient"

	"phenix/util/common"
	"phenix/util/plog"
)

// The bounds of the minimega.read-connections setting (see SetReadOnlyLimit).
const (
	DefaultReadOnlyLimit = 16
	MinReadOnlyLimit     = 3
	MaxReadOnlyLimit     = 64
)

const (
	// readOnlyHeadReserve is how many of the read-only connections only reads
	// of the head node may use, so a full mesh cannot hold them all up.
	readOnlyHeadReserve = 2

	// readOnlyScreenshotMargin is how many connections screenshots leave to
	// other reads: up to limit-readOnlyScreenshotMargin run at once, at least 1.
	readOnlyScreenshotMargin = 4

	// readOnlyIdleTimeout is how long a read-only connection stays open with
	// nothing to do. None is kept open longer.
	readOnlyIdleTimeout = 5 * time.Minute
)

// ReadLine names the wait line of a RunReadOnly command (see connPool). The
// caller selects it; the pool does not check it against the command.
type ReadLine int

const (
	// HeadRead is a read that runs only on the head node.
	HeadRead ReadLine = iota
	// MeshRead is a read that can go to the mesh peers: a command in a
	// namespace (minimega sends it to the peers of the namespace) or a
	// `mesh send`.
	MeshRead
	// ScreenshotRead is a MeshRead that takes a VM screenshot.
	ScreenshotRead
	readLines
)

// errUnknownReadLine is the error of RunReadOnly for a ReadLine that is not
// HeadRead, MeshRead or ScreenshotRead.
var errUnknownReadLine = errors.New("unknown read-only wait line")

// readOnlyPool holds the connections of RunReadOnly.
//
//nolint:gochecknoglobals // process-wide pool, as the shared connection is
var readOnlyPool = newConnPool(DefaultReadOnlyLimit, time.Now)

// ReadOnlyLimit is how many RunReadOnly commands minimega gets at once now.
// A caller that fans out reads bounds its fan-out by it.
func ReadOnlyLimit() int {
	return readOnlyPool.currentLimit()
}

// SetReadOnlyLimit sets how many RunReadOnly commands minimega gets at once,
// the minimega.read-connections setting, and returns the value used: n
// clamped to MinReadOnlyLimit..MaxReadOnlyLimit, with a warning when it is
// outside. A raised limit lets waiting commands in at once. A lowered one
// closes the idle connections above it and lets the commands in flight finish.
func SetReadOnlyLimit(n int) int {
	used := min(max(n, MinReadOnlyLimit), MaxReadOnlyLimit)

	if used != n {
		plog.Warn(
			plog.TypeSystem, "minimega.read-connections is out of range, so phenix uses the nearest allowed value",
			"given", n, "used", used, "min", MinReadOnlyLimit, "max", MaxReadOnlyLimit,
		)
	}

	readOnlyPool.setLimit(used)

	return used
}

// pooledConn is an open connection of a connPool and when it last finished a
// command.
type pooledConn struct {
	conn      *miniclient.Conn
	idleSince time.Time
}

// connPool lends connections to minimega, dialed when needed, to up to limit
// commands at once. A HeadRead may use any free connection. A MeshRead or
// ScreenshotRead may use up to limit-readOnlyHeadReserve, and screenshots
// (see screenshotLimit) fewer. Each ReadLine is a queue of its own, first
// come first served. An idle connection closes after idleTimeout.
type connPool struct {
	mu      sync.Mutex
	limit   int
	inUse   [readLines]int
	idle    []pooledConn // the most recently used last
	waiting [readLines][]chan struct{}
	// open counts the connections in use, idle or being dialed
	open int
	// dialed is set once the pool dialed a connection
	dialed bool
	// redial is set when a connection broke, until the next dial
	redial bool

	now         func() time.Time
	idleTimeout time.Duration

	reaper sync.Once
	stop   chan struct{}
	closed sync.Once
}

func newConnPool(limit int, now func() time.Time) *connPool {
	return &connPool{ //nolint:exhaustruct // the zero values are an empty pool
		limit:       limit,
		now:         now,
		idleTimeout: readOnlyIdleTimeout,
		stop:        make(chan struct{}),
	}
}

// screenshotLimit is how many screenshots may run at once under limit.
func screenshotLimit(limit int) int {
	if limit > readOnlyScreenshotMargin {
		return limit - readOnlyScreenshotMargin
	}

	return 1
}

func (p *connPool) currentLimit() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.limit
}

// canRunLocked reports whether a command of line may start now.
func (p *connPool) canRunLocked(line ReadLine) bool {
	mesh := p.inUse[MeshRead] + p.inUse[ScreenshotRead]

	switch {
	case p.inUse[HeadRead]+mesh >= p.limit:
		return false
	case line == HeadRead:
		return true
	case mesh >= p.limit-readOnlyHeadReserve:
		return false
	case line == ScreenshotRead:
		return p.inUse[ScreenshotRead] < screenshotLimit(p.limit)
	}

	return true
}

// grantLocked lets in the waiting commands that may start now, head node
// reads first, then mesh reads, then screenshots.
func (p *connPool) grantLocked() {
	for i := range len(p.waiting) {
		line := ReadLine(i)

		for len(p.waiting[line]) > 0 && p.canRunLocked(line) {
			turn := p.waiting[line][0]
			p.waiting[line] = p.waiting[line][1:]
			p.inUse[line]++

			close(turn)
		}
	}
}

// acquire waits for a turn of line until ctx is done, reporting whether it
// got one.
func (p *connPool) acquire(ctx context.Context, line ReadLine) bool {
	p.reaper.Do(func() { go p.reap() })

	p.mu.Lock()

	// a free connection is taken even once ctx is done, as shared.acquire does
	if len(p.waiting[line]) == 0 && p.canRunLocked(line) {
		p.inUse[line]++
		p.mu.Unlock()

		return true
	}

	turn := make(chan struct{})
	p.waiting[line] = append(p.waiting[line], turn)

	p.mu.Unlock()

	select {
	case <-turn:
		return true
	case <-ctx.Done():
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if i := slices.Index(p.waiting[line], turn); i >= 0 {
		p.waiting[line] = slices.Delete(p.waiting[line], i, i+1)

		return false
	}

	// granted just as ctx ended: pass the turn on
	p.inUse[line]--
	p.grantLocked()

	return false
}

// conn returns an idle connection, or dials a new one, for a turn. It
// reports whether the connection came from the idle list.
func (p *connPool) conn() (*miniclient.Conn, bool, error) {
	p.mu.Lock()

	if n := len(p.idle); n > 0 {
		c := p.idle[n-1].conn
		p.idle = p.idle[:n-1]
		p.mu.Unlock()

		return c, true, nil
	}

	p.mu.Unlock()

	c, err := p.dial()

	return c, false, err
}

// dial dials a new connection for a turn. It counts a redial (see Redials)
// when a connection broke, or when the pool dialed before and has no other
// open connection: minimega may have restarted while none was open.
func (p *connPool) dial() (*miniclient.Conn, error) {
	p.mu.Lock()
	empty := p.dialed && p.open == 0
	p.open++
	p.mu.Unlock()

	c, err := miniclient.Dial(common.MinimegaBase)

	p.mu.Lock()
	defer p.mu.Unlock()

	if err != nil {
		p.open--

		return nil, fmt.Errorf("unable to dial: %w", err)
	}

	if p.redial || empty {
		redials.Add(1)

		p.redial = false
	}

	p.dialed = true

	return c, nil
}

// discard closes c, a connection that broke before it answered.
func (p *connPool) discard(c *miniclient.Conn) {
	p.mu.Lock()
	p.open--
	p.redial = true
	p.mu.Unlock()

	_ = c.Close()
}

// release ends a turn of line. It keeps c, if not nil, open for the next
// command, unless it broke or the open connections reach the limit.
func (p *connPool) release(line ReadLine, c *miniclient.Conn, broken bool) {
	p.mu.Lock()

	p.inUse[line]--

	keep := c != nil && !broken && c.Error() == nil &&
		p.inUse[HeadRead]+p.inUse[MeshRead]+p.inUse[ScreenshotRead]+len(p.idle) < p.limit

	if keep {
		p.idle = append(p.idle, pooledConn{conn: c, idleSince: p.now()})
	}

	if c != nil && (broken || c.Error() != nil) {
		p.redial = true
	}

	if c != nil && !keep {
		p.open--
	}

	p.grantLocked()
	p.mu.Unlock()

	if c != nil && !keep {
		_ = c.Close()
	}
}

// setLimit changes the limit, closing the idle connections above it and
// letting in the waiting commands that may start now.
func (p *connPool) setLimit(limit int) {
	p.mu.Lock()

	p.limit = limit

	var excess []pooledConn

	inUse := p.inUse[HeadRead] + p.inUse[MeshRead] + p.inUse[ScreenshotRead]
	if over := inUse + len(p.idle) - limit; over > 0 {
		over = min(over, len(p.idle))
		excess = slices.Clone(p.idle[:over])
		p.idle = slices.Delete(p.idle, 0, over)
		p.open -= over
	}

	p.grantLocked()
	p.mu.Unlock()

	for _, c := range excess {
		_ = c.conn.Close()
	}
}

// reapIdle closes the connections idle for idleTimeout or longer.
func (p *connPool) reapIdle() {
	p.mu.Lock()

	now := p.now()

	var stale []pooledConn

	p.idle = slices.DeleteFunc(p.idle, func(c pooledConn) bool {
		if now.Sub(c.idleSince) >= p.idleTimeout {
			stale = append(stale, c)

			return true
		}

		return false
	})

	p.open -= len(stale)

	p.mu.Unlock()

	for _, c := range stale {
		_ = c.conn.Close()
	}
}

// reap runs reapIdle until close, a few times per idleTimeout.
func (p *connPool) reap() {
	const checksPerTimeout = 5

	ticker := time.NewTicker(p.idleTimeout / checksPerTimeout)
	defer ticker.Stop()

	for {
		select {
		case <-p.stop:
			return
		case <-ticker.C:
			p.reapIdle()
		}
	}
}

// close stops the reaper and closes the idle connections.
func (p *connPool) close() {
	p.closed.Do(func() { close(p.stop) })

	p.mu.Lock()
	idle := p.idle
	p.idle = nil
	p.open -= len(idle)
	p.mu.Unlock()

	for _, c := range idle {
		_ = c.conn.Close()
	}
}

// RunReadOnly is RunContext for a command that changes nothing in minimega,
// such as `vm info`. It runs c on a connection of its own (see
// SetReadOnlyLimit), so it neither waits behind nor holds up the commands on
// the shared connection. It waits in line for a free connection until ctx is
// done or DefaultWaitTimeout passes (ErrNotSent), and never falls back to the
// shared connection when they are all busy. ctx's deadline, if any, bounds a
// sent command in place of DefaultCommandTimeout, and a command with a
// timeout of its own runs on a private connection, as with Run.
//
// A command that fails on a kept connection before any answer, as after a
// minimega restart, is sent one more time on a new connection.
//
// Send only a command that changes nothing in minimega. There is no ordering
// against the shared connection or between reads: a caller that must see the
// effect of a write sends the read only after it drains the response channel
// of that write. Select line by this rule: MeshRead for a command with
// c.Namespace set, a `namespace <ns> ...` prefix or a `mesh send`;
// ScreenshotRead for a `vm screenshot`; HeadRead for all other commands. The
// pool does not check that the command only reads or that the line fits it.
// An unknown line gives an error response, and the pool sends nothing.
func RunReadOnly(ctx context.Context, line ReadLine, c *Command) chan *miniclient.Response {
	return readOnlyPool.run(ctx, line, c, nil)
}

// RunReadOnlyStarted is RunReadOnly, calling started (if not nil) just before
// c is sent; started must be quick and run no minimega commands.
func RunReadOnlyStarted(
	ctx context.Context,
	line ReadLine,
	c *Command,
	started func(),
) chan *miniclient.Response {
	return readOnlyPool.run(ctx, line, c, started)
}

// run does the work of RunReadOnly and RunReadOnlyStarted on p.
func (p *connPool) run(ctx context.Context, line ReadLine, c *Command, started func()) chan *miniclient.Response {
	if line < HeadRead || line >= readLines {
		return wrapErr(fmt.Errorf("%w: %d", errUnknownReadLine, line))
	}

	cmdStr := c.String()

	if err := ctx.Err(); err != nil {
		return wrapErr(gaveUp(err))
	}

	if c.Timeout > 0 {
		notify(started)

		return runPrivate(common.MinimegaBase, cmdStr, c.Timeout)
	}

	wait, cancel := context.WithTimeout(ctx, DefaultWaitTimeout)
	defer cancel()

	if !p.acquire(wait, line) {
		return wrapErr(gaveUp(ctx.Err()))
	}

	notify(started)

	active, reused, err := p.conn()
	if err != nil {
		p.release(line, nil, false)

		return wrapErr(err)
	}

	var resend resender

	// an idle connection may have died with a minimega restart: as reads change
	// nothing, the command is sent once more on a new connection
	if reused {
		resend = func() (*miniclient.Conn, chan *miniclient.Response, error) {
			p.discard(active)

			fresh, err := p.dial()
			if err != nil {
				return nil, nil, err
			}

			return fresh, fresh.Run(cmdStr), nil
		}
	}

	timeout, timeoutErr := commandTimeout(ctx)

	return guard(active, active.Run(cmdStr), func(c *miniclient.Conn, broken bool) {
		p.release(line, c, broken)
	}, resend, timeout, timeoutErr)
}
