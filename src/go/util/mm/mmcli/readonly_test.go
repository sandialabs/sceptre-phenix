package mmcli

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"
	"github.com/sandia-minimega/minimega/v2/pkg/miniclient"
	"go.uber.org/goleak"

	"phenix/util/mm/mmtest"
	"phenix/util/plog/plogtest"
	"phenix/util/polltest"
)

// Read-only commands the tests send; the fake minimega gives each its behavior.
const (
	readBlock      = "file list /block"
	readDisconnect = "file list /disconnect"
	readHang       = "file list /hang"
	readOK         = "version"
	unchecked      = "vlans add a 5"
)

// TestMain fails the run when a goroutine is still alive after the tests.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(closingPool{m: mmtest.StopAfter(m)})
}

// closingPool runs the tests, then stops the idle reaper of the process-wide
// pool, before goleak looks for goroutines.
type closingPool struct {
	m mmtest.StoppingM
}

func (c closingPool) Run() int {
	code := c.m.Run()

	readOnlyPool.close()

	return code
}

// fakeClock is the clock of a test pool, which only Advance moves.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.now = c.now.Add(d)
}

// testPool is a pool of limit connections on clock, closed at the test's end.
func testPool(t *testing.T, limit int, clock *fakeClock) *connPool {
	t.Helper()

	p := newConnPool(limit, clock.Now)
	t.Cleanup(p.close)

	return p
}

// idleConns returns the idle connections of p.
func idleConns(p *connPool) []*miniclient.Conn {
	p.mu.Lock()
	defer p.mu.Unlock()

	conns := make([]*miniclient.Conn, 0, len(p.idle))
	for _, c := range p.idle {
		conns = append(conns, c.conn)
	}

	return conns
}

// waitingIn returns how many commands wait in line in p.
func waitingIn(p *connPool, line ReadLine) int {
	p.mu.Lock()
	defer p.mu.Unlock()

	return len(p.waiting[line])
}

// inFlight answers readBlock commands once release is closed, counting how
// many it holds at once by namespace ("" for none).
type inFlight struct {
	release chan struct{}
	unblock func() // closes release once
	mu      sync.Mutex
	now     map[string]int
	most    map[string]int
}

func newInFlight() *inFlight {
	release := make(chan struct{})

	return &inFlight{
		release: release, unblock: sync.OnceFunc(func() { close(release) }),
		mu: sync.Mutex{}, now: map[string]int{}, most: map[string]int{},
	}
}

func (f *inFlight) count(ns string, delta int) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.now[ns] += delta
	f.most[ns] = max(f.most[ns], f.now[ns])
}

// mostAt returns the most commands of namespace ns held at once.
func (f *inFlight) mostAt(ns string) int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.most[ns]
}

// held returns how many commands f holds now.
func (f *inFlight) held() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	var total int
	for _, n := range f.now {
		total += n
	}

	return total
}

func (f *inFlight) handle(cmd mmtest.Command) []*minicli.Response {
	switch cmd.Base {
	case readBlock, screenshotBlock:
		f.count(cmd.Namespace, 1)
		<-f.release
		f.count(cmd.Namespace, -1)
	case readDisconnect:
		return mmtest.Disconnect()
	case readHang:
		<-f.release
	}

	return reply("ok")
}

// screenshotBlock is a screenshot that the fake holds as it holds readBlock.
const screenshotBlock = "vm screenshot vm1 file /dev/null 200"

// useReadOnlyFake points the package at a fake minimega that answers with f.
func useReadOnlyFake(t *testing.T, f *inFlight) func() []mmtest.Command {
	t.Helper()

	received := useFakeMinimega(t, f.handle)

	t.Cleanup(f.unblock)

	return received
}

// meshCommand and screenshot are commands for MeshRead and ScreenshotRead.
func meshCommand(name string) *Command {
	return &Command{Command: name, Namespace: "exp"}
}

func screenshot() *Command {
	return meshCommand(screenshotBlock)
}

// start runs c on p in line in the background, sending its error to errs.
func start(p *connPool, line ReadLine, c *Command, errs chan<- error) {
	go func() { errs <- ErrorResponse(p.run(context.Background(), line, c, nil)) }()
}

// waitAll waits for n errors and fails the test for each that is not nil.
func waitAll(t *testing.T, errs <-chan error, n int) {
	t.Helper()

	for range n {
		select {
		case err := <-errs:
			if err != nil {
				t.Errorf("read-only command error = %v, want nil", err)
			}
		case <-time.After(maxWait):
			t.Fatal("read-only commands did not finish")
		}
	}
}

// SetReadOnlyLimit keeps the limit in MinReadOnlyLimit..MaxReadOnlyLimit and
// warns, naming the given and the used value, for a value outside.
//
//nolint:paralleltest // changes the process-wide limit
func TestSetReadOnlyLimitClamps(t *testing.T) {
	t.Cleanup(func() { readOnlyPool.setLimit(DefaultReadOnlyLimit) })

	for _, tc := range []struct{ given, want int }{
		{2, MinReadOnlyLimit}, {MinReadOnlyLimit, MinReadOnlyLimit}, {16, 16},
		{MaxReadOnlyLimit, MaxReadOnlyLimit}, {100, MaxReadOnlyLimit},
	} {
		logs := plogtest.Capture(t)

		if got := SetReadOnlyLimit(tc.given); got != tc.want {
			t.Errorf("SetReadOnlyLimit(%d) = %d, want %d", tc.given, got, tc.want)
		}

		if got := ReadOnlyLimit(); got != tc.want {
			t.Errorf("ReadOnlyLimit() after SetReadOnlyLimit(%d) = %d, want %d", tc.given, got, tc.want)
		}

		warnings := logs.Take(t, func(record map[string]any) bool { return record["level"] == "WARN" })

		got := make([][2]any, 0, len(warnings))
		for _, w := range warnings {
			got = append(got, [2]any{w["given"], w["used"]})
		}

		var want [][2]any
		if tc.given != tc.want {
			want = [][2]any{{float64(tc.given), float64(tc.want)}}
		}

		if diff := cmp.Diff(want, got, cmpopts.EquateEmpty()); diff != "" {
			t.Errorf("SetReadOnlyLimit(%d) warnings [given, used] mismatch (-want +got):\n%s", tc.given, diff)
		}
	}
}

// A pool runs at most limit commands at once, and the rest wait for a turn.
func TestConnPoolBoundsCommandsInFlight(t *testing.T) {
	fake := newInFlight()
	received := useReadOnlyFake(t, fake)
	p := testPool(t, 3, new(fakeClock))

	const commands = 6

	errs := make(chan error, commands)

	for range commands {
		start(p, HeadRead, command(readBlock), errs)
	}

	polltest.Until(t, "the pool's commands reach minimega", func() bool { return fake.held() == 3 })
	polltest.Until(t, "the others wait", func() bool { return waitingIn(p, HeadRead) == commands-3 })

	if got := mmtest.Count(received(), readBlock); got != 3 {
		t.Errorf("a pool of 3 sent %d of %d commands at once, want 3", got, commands)
	}

	fake.unblock()
	waitAll(t, errs, commands)

	if got := fake.mostAt(""); got != 3 {
		t.Errorf("a pool of 3 had %d commands in flight at most, want 3", got)
	}
}

// Mesh reads use at most limit-2 connections, and a head node read still runs
// while they are all taken.
func TestConnPoolReservesConnectionsForTheHeadNode(t *testing.T) {
	fake := newInFlight()
	useReadOnlyFake(t, fake)
	p := testPool(t, 5, new(fakeClock))

	errs := make(chan error, 10)

	for range 5 {
		start(p, MeshRead, meshCommand(readBlock), errs)
	}

	polltest.Until(t, "the mesh reads that may run reach minimega", func() bool { return fake.held() == 3 })
	polltest.Until(t, "the other mesh reads wait", func() bool { return waitingIn(p, MeshRead) == 2 })

	if err := ErrorResponse(p.run(context.Background(), HeadRead, command(readOK), nil)); err != nil {
		t.Errorf("a head node read beside 3 mesh reads in a pool of 5 error = %v, want nil", err)
	}

	for range 2 {
		start(p, HeadRead, command(readBlock), errs)
	}

	polltest.Until(t, "the head node reads take the last 2 connections", func() bool { return fake.held() == 5 })

	if got := waitingIn(p, MeshRead); got != 2 {
		t.Errorf("mesh reads waiting with the pool full = %d, want 2", got)
	}

	fake.unblock()
	waitAll(t, errs, 7)

	if got := fake.mostAt("exp"); got != 3 {
		t.Errorf("a pool of 5 ran %d mesh reads at once, want 3", got)
	}
}

// Each class waits in its own line, first come first served: a waiting mesh
// read does not hold up a head node read, and mesh reads run in the order
// they came.
func TestConnPoolLinesAreFirstComeFirstServed(t *testing.T) {
	fake := newInFlight()
	received := useReadOnlyFake(t, fake)
	p := testPool(t, 3, new(fakeClock))

	errs := make(chan error, 4)

	// the one mesh read a pool of 3 runs at once
	start(p, MeshRead, meshCommand(readBlock), errs)
	polltest.Until(t, "the first mesh read reaches minimega", func() bool { return fake.held() == 1 })

	order := []string{"file list /a", "file list /b", "file list /c"}

	for i, name := range order {
		start(p, MeshRead, meshCommand(name), errs)
		polltest.Until(t, "the mesh read waits in line", func() bool { return waitingIn(p, MeshRead) == i+1 })
	}

	if err := ErrorResponse(p.run(context.Background(), HeadRead, command(readOK), nil)); err != nil {
		t.Errorf("a head node read beside a mesh line error = %v, want nil", err)
	}

	fake.unblock()
	waitAll(t, errs, 4)

	var got []string

	for _, cmd := range received() {
		if slices.Contains(order, cmd.Base) {
			got = append(got, cmd.Base)
		}
	}

	if diff := cmp.Diff(order, got); diff != "" {
		t.Errorf("mesh reads sent order mismatch (-want +got):\n%s", diff)
	}
}

// At most limit-4 screenshots run at once (1 for a limit of 4 or less), and
// other mesh reads still run beside them.
func TestConnPoolLimitsScreenshots(t *testing.T) {
	for _, tc := range []struct{ limit, shots int }{{3, 1}, {7, 3}} {
		t.Run(strconv.Itoa(tc.limit), func(t *testing.T) {
			fake := newInFlight()
			useReadOnlyFake(t, fake)
			p := testPool(t, tc.limit, new(fakeClock))

			errs := make(chan error, 10)

			for range tc.shots + 2 {
				start(p, ScreenshotRead, screenshot(), errs)
			}

			polltest.Until(t, "the screenshots that may run reach minimega", func() bool { return fake.held() == tc.shots })
			polltest.Until(t, "the other screenshots wait", func() bool { return waitingIn(p, ScreenshotRead) == 2 })

			if tc.limit-2 > tc.shots {
				if err := ErrorResponse(p.run(context.Background(), MeshRead, meshCommand(readOK), nil)); err != nil {
					t.Errorf("a mesh read beside %d screenshots error = %v, want nil", tc.shots, err)
				}
			}

			fake.unblock()
			waitAll(t, errs, tc.shots+2)

			if got := fake.mostAt("exp"); got != tc.shots {
				t.Errorf("a pool of %d ran %d screenshots at once, want %d", tc.limit, got, tc.shots)
			}
		})
	}
}

// A pool dials a connection only when a command needs one, keeps it for the
// next command, and closes it once it has been idle for idleTimeout.
func TestConnPoolOpensOnDemandAndClosesIdle(t *testing.T) {
	fake := newInFlight()
	useReadOnlyFake(t, fake)

	clock := new(fakeClock)
	p := testPool(t, 3, clock)

	if got := len(idleConns(p)); got != 0 {
		t.Fatalf("a new pool has %d connections, want 0", got)
	}

	errs := make(chan error, 3)

	for range 3 {
		start(p, HeadRead, command(readBlock), errs)
	}

	polltest.Until(t, "3 commands reach minimega", func() bool { return fake.held() == 3 })
	fake.unblock()
	waitAll(t, errs, 3)

	if got := len(idleConns(p)); got != 3 {
		t.Fatalf("a pool of 3 after 3 commands at once has %d idle connections, want 3", got)
	}

	clock.Advance(readOnlyIdleTimeout - time.Second)
	p.reapIdle()

	if got := len(idleConns(p)); got != 3 {
		t.Errorf("idle connections just before %v = %d, want 3", readOnlyIdleTimeout, got)
	}

	// one connection is used again, so it is no longer idle that long
	if err := ErrorResponse(p.run(context.Background(), HeadRead, command(readOK), nil)); err != nil {
		t.Fatalf("p.run(%s) error = %v", readOK, err)
	}

	clock.Advance(time.Second)
	p.reapIdle()

	if got := len(idleConns(p)); got != 1 {
		t.Errorf("idle connections after %v = %d, want the 1 used since", readOnlyIdleTimeout, got)
	}
}

// A raised limit lets waiting commands in at once. A lowered one cuts off no
// command in flight, starts no new one above it, and closes the idle
// connections above it.
func TestConnPoolLimitChangesLive(t *testing.T) {
	fake := newInFlight()
	received := useReadOnlyFake(t, fake)
	p := testPool(t, 3, new(fakeClock))

	errs := make(chan error, 8)

	for range 5 {
		start(p, HeadRead, command(readBlock), errs)
	}

	polltest.Until(t, "3 commands reach minimega", func() bool { return fake.held() == 3 })

	p.setLimit(5)

	polltest.Until(t, "the raised limit lets in the waiting commands", func() bool { return fake.held() == 5 })

	p.setLimit(3)

	for range 2 {
		start(p, HeadRead, command(readBlock), errs)
	}

	polltest.Until(t, "the new commands wait", func() bool { return waitingIn(p, HeadRead) == 2 })

	if got := mmtest.Count(received(), readBlock); got != 5 {
		t.Errorf("commands sent after lowering the limit below those in flight = %d, want 5", got)
	}

	fake.unblock()
	waitAll(t, errs, 7)

	if got := fake.mostAt(""); got != 5 {
		t.Errorf("commands in flight at most = %d, want 5", got)
	}

	if got := len(idleConns(p)); got > 3 {
		t.Errorf("idle connections with a limit of 3 = %d, want 3 or fewer", got)
	}

	// 5 idle connections, then a limit of 3 closes the 2 used least recently
	more := newInFlight()
	useReadOnlyFake(t, more)
	p.setLimit(5)

	for range 5 {
		start(p, HeadRead, command(readBlock), errs)
	}

	polltest.Until(t, "5 commands reach minimega", func() bool { return more.held() == 5 })
	more.unblock()
	waitAll(t, errs, 5)

	before := idleConns(p)
	if len(before) != 5 {
		t.Fatalf("idle connections after 5 commands at once with a limit of 5 = %d, want 5", len(before))
	}

	p.setLimit(3)

	// the connections are compared by identity
	if got := idleConns(p); !slices.Equal(got, before[2:]) {
		t.Errorf("idle connections after lowering the limit to 3 are not the 3 used last (got %d)", len(got))
	}

	for i, c := range before[:2] {
		if err := c.Close(); err == nil {
			t.Errorf("idle connection %d above the lowered limit is open, want it closed", i)
		}
	}

	if got := openConns(p); got != 3 {
		t.Errorf("open connections after lowering the limit to 3 = %d, want 3", got)
	}
}

// openConns returns how many connections p counts as open.
func openConns(p *connPool) int {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.open
}

// A command that fails on an idle connection before any answer, as after a
// minimega restart, is sent once more on a new connection, which the pool
// keeps in place of the broken one.
//
//nolint:paralleltest // uses the fake minimega
func TestConnPoolResendsOnABrokenIdleConnection(t *testing.T) {
	const readStale = "file list /stale"

	for _, tc := range []struct {
		name string
		// local closes the idle connection on phenix's side; else minimega closes it
		local     bool
		wantSends int
	}{
		{name: "closed by phenix", local: true, wantSends: 1},
		{name: "closed by minimega", local: false, wantSends: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stales atomic.Int32

			received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
				if cmd.Base == readStale && stales.Add(1) == 1 && !tc.local {
					return mmtest.Disconnect()
				}

				return reply("ok")
			})

			p := testPool(t, 3, new(fakeClock))

			if err := ErrorResponse(p.run(context.Background(), HeadRead, command(readOK), nil)); err != nil {
				t.Fatalf("p.run(%s) error = %v", readOK, err)
			}

			idle := idleConns(p)
			if len(idle) != 1 {
				t.Fatalf("idle connections after one command = %d, want 1", len(idle))
			}

			if tc.local {
				_ = idle[0].Close()
			}

			before := Redials()

			got, err := SingleResponse(p.run(context.Background(), HeadRead, command(readStale), nil))
			if err != nil || got != "ok" {
				t.Errorf("SingleResponse(p.run(%s)) on a broken idle connection = %q, %v; want %q, nil", readStale, got, err, "ok")
			}

			if sends := mmtest.Count(received(), readStale); sends != tc.wantSends {
				t.Errorf("%s sent %d times, want %d", readStale, sends, tc.wantSends)
			}

			after := idleConns(p)
			if len(after) != 1 || after[0] == idle[0] {
				t.Errorf("idle connections after the resend = %d (the broken one kept: %t), want 1 new one",
					len(after), slices.Contains(after, idle[0]))
			}

			if got := openConns(p); got != 1 {
				t.Errorf("open connections after the resend = %d, want 1", got)
			}

			if Redials() == before {
				t.Error("Redials() did not change when the pool replaced a broken idle connection")
			}
		})
	}
}

// A command on a connection the pool has just dialed is not sent again when
// that connection breaks.
func TestConnPoolDoesNotResendOnANewConnection(t *testing.T) {
	received := useReadOnlyFake(t, newInFlight())
	p := testPool(t, 3, new(fakeClock))

	if err := ErrorResponse(p.run(context.Background(), HeadRead, command(readDisconnect), nil)); err == nil {
		t.Errorf("p.run(%s) on a new connection error = nil, want a lost connection", readDisconnect)
	}

	if sends := mmtest.Count(received(), readDisconnect); sends != 1 {
		t.Errorf("%s on a new connection sent %d times, want 1", readDisconnect, sends)
	}

	if got := openConns(p); got != 0 {
		t.Errorf("open connections after a lost new connection = %d, want 0", got)
	}
}

// Redials counts a dial when the pool dialed before and has no open
// connection, as minimega may have restarted while none was open. A first
// dial, or one beside open connections, is no redial.
func TestConnPoolCountsADialWithNoOpenConnection(t *testing.T) {
	fake := newInFlight()
	useReadOnlyFake(t, fake)

	clock := new(fakeClock)
	p := testPool(t, 3, clock)
	before := Redials()

	if err := ErrorResponse(p.run(context.Background(), HeadRead, command(readOK), nil)); err != nil {
		t.Fatalf("p.run(%s) error = %v", readOK, err)
	}

	errs := make(chan error, 1)

	// the idle connection is in use, so the next command dials beside it
	start(p, HeadRead, command(readBlock), errs)
	polltest.Until(t, "the command reaches minimega", func() bool { return fake.held() == 1 })

	if err := ErrorResponse(p.run(context.Background(), HeadRead, command(readOK), nil)); err != nil {
		t.Fatalf("p.run(%s) beside a command in flight error = %v", readOK, err)
	}

	fake.unblock()
	waitAll(t, errs, 1)

	if got := Redials() - before; got != 0 {
		t.Errorf("Redials() changed by %d for a first dial and a dial beside an open connection, want 0", got)
	}

	clock.Advance(readOnlyIdleTimeout)
	p.reapIdle()

	if got := openConns(p); got != 0 {
		t.Fatalf("open connections after the idle ones closed = %d, want 0", got)
	}

	if err := ErrorResponse(p.run(context.Background(), HeadRead, command(readOK), nil)); err != nil {
		t.Fatalf("p.run(%s) after the idle connections closed error = %v", readOK, err)
	}

	if got := Redials() - before; got != 1 {
		t.Errorf("Redials() changed by %d for a dial with no open connection, want 1", got)
	}
}

// A command waiting for a busy pool gives up unsent once its context is done,
// or past DefaultWaitTimeout, and never takes the shared connection instead.
//
//nolint:paralleltest // shortens the default deadlines
func TestConnPoolWaitsForABusyPool(t *testing.T) {
	saved := DefaultWaitTimeout
	t.Cleanup(func() { DefaultWaitTimeout = saved })

	fake := newInFlight()
	received := useReadOnlyFake(t, fake)
	p := testPool(t, 3, new(fakeClock))

	errs := make(chan error, 3)

	for range 3 {
		start(p, HeadRead, command(readBlock), errs)
	}

	polltest.Until(t, "the pool is full", func() bool { return fake.held() == 3 })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() { done <- ErrorResponse(p.run(ctx, MeshRead, meshCommand(readOK), nil)) }()

	polltest.Until(t, "the mesh read waits", func() bool { return waitingIn(p, MeshRead) == 1 })
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, ErrNotSent) || !errors.Is(err, context.Canceled) {
			t.Errorf("p.run(canceled ctx, %s) with a busy pool = %v, want %v and %v", readOK, err, ErrNotSent, context.Canceled)
		}
	case <-time.After(maxWait):
		t.Fatalf("p.run(canceled ctx, %s) kept waiting after its context was canceled", readOK)
	}

	DefaultWaitTimeout = testTimeout

	err := ErrorResponse(p.run(context.Background(), HeadRead, command(readOK), nil))
	if !errors.Is(err, ErrNotSent) || !errors.Is(err, ErrTimeout) || errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("p.run(background ctx, %s) past DefaultWaitTimeout = %v, want %v and %v alone",
			readOK, err, ErrNotSent, ErrTimeout)
	}

	if got := mmtest.Count(received(), readOK); got != 0 {
		t.Errorf("p.run(%s) with a busy pool sent %d commands, want 0", readOK, got)
	}

	if got := waitingIn(p, MeshRead) + waitingIn(p, HeadRead); got != 0 {
		t.Errorf("commands still waiting after giving up = %d, want 0", got)
	}

	// the shared connection is free all along
	if err := ErrorResponse(Run(command(readOK))); err != nil {
		t.Errorf("Run(%s) beside a busy pool error = %v", readOK, err)
	}

	fake.unblock()
	waitAll(t, errs, 3)
}

// A connection that is lost or whose command times out is closed, not kept,
// and the next command dials a new one; the shared connection is left as it
// was.
//
//nolint:paralleltest // shortens the default deadlines
func TestConnPoolReplacesABrokenConnection(t *testing.T) {
	saved := DefaultCommandTimeout
	t.Cleanup(func() { DefaultCommandTimeout = saved })

	fake := newInFlight()
	useReadOnlyFake(t, fake)
	p := testPool(t, 3, new(fakeClock))

	if err := ErrorResponse(Run(command(readOK))); err != nil {
		t.Fatalf("Run(%s) error = %v", readOK, err)
	}

	mu.Lock()
	sharedBefore := mm
	mu.Unlock()

	if err := ErrorResponse(p.run(context.Background(), HeadRead, command(readDisconnect), nil)); err == nil {
		t.Errorf("p.run(%s) error = nil, want a lost connection", readDisconnect)
	}

	if got := len(idleConns(p)); got != 0 {
		t.Errorf("idle connections after a lost one = %d, want 0", got)
	}

	before := Redials()

	if err := ErrorResponse(p.run(context.Background(), HeadRead, command(readOK), nil)); err != nil {
		t.Errorf("p.run(%s) after a lost connection error = %v", readOK, err)
	}

	if Redials() == before {
		t.Error("Redials() did not change when the pool dialed again after a lost connection")
	}

	DefaultCommandTimeout = testTimeout

	if err := ErrorResponse(p.run(context.Background(), HeadRead, command(readHang), nil)); !errors.Is(err, ErrTimeout) {
		t.Errorf("p.run(%s) past DefaultCommandTimeout = %v, want %v", readHang, err, ErrTimeout)
	}

	DefaultCommandTimeout = saved

	for _, c := range idleConns(p) {
		if c.Error() != nil {
			t.Errorf("an idle connection after a timed out command has error %v, want a working one", c.Error())
		}
	}

	if err := ErrorResponse(p.run(context.Background(), HeadRead, command(readOK), nil)); err != nil {
		t.Errorf("p.run(%s) after a timed out command error = %v", readOK, err)
	}

	mu.Lock()
	sharedAfter, dead := mm, mmDead
	mu.Unlock()

	if sharedAfter != sharedBefore || dead {
		t.Error("a broken read-only connection replaced or marked dead the shared connection, want it left as it was")
	}
}

// A lost shared connection leaves the read-only connections as they were.
func TestConnPoolKeepsWorkingWhenTheSharedConnectionDies(t *testing.T) {
	fake := newInFlight()
	useReadOnlyFake(t, fake)
	p := testPool(t, 3, new(fakeClock))

	errs := make(chan error, 3)

	for range 3 {
		start(p, HeadRead, command(readBlock), errs)
	}

	polltest.Until(t, "3 commands reach minimega", func() bool { return fake.held() == 3 })
	fake.unblock()
	waitAll(t, errs, 3)

	before := idleConns(p)

	if err := ErrorResponse(Run(command(readDisconnect))); err == nil {
		t.Errorf("Run(%s) error = nil, want a lost connection", readDisconnect)
	}

	for range 3 {
		if got, err := SingleResponse(p.run(context.Background(), HeadRead, command(readOK), nil)); err != nil || got != "ok" {
			t.Errorf("SingleResponse(p.run(%s)) after a lost shared connection = %q, %v; want %q", readOK, got, err, "ok")
		}
	}

	for _, conn := range idleConns(p) {
		if !slices.Contains(before, conn) {
			t.Errorf("read-only connection %p after a lost shared connection is new, want only those before", conn)
		}
	}
}

// RunReadOnly sends any command on its own connections, not on the shared
// one, and logs no warning: it does not check the command.
func TestRunReadOnlySendsAnyCommand(t *testing.T) {
	unhold, received := holdShared(t)
	logs := plogtest.Capture(t)

	if err := ErrorResponse(RunReadOnly(context.Background(), HeadRead, command(unchecked))); err != nil {
		t.Errorf("RunReadOnly(%s) while the shared connection is held error = %v, want nil", unchecked, err)
	}

	if got := mmtest.Count(received(), unchecked); got != 1 {
		t.Errorf("RunReadOnly(%s) sent it %d times, want 1", unchecked, got)
	}

	unhold()

	warnings := logs.Records(t, func(record map[string]any) bool { return record["level"] == "WARN" })
	if len(warnings) != 0 {
		t.Errorf("RunReadOnly(%s) logged warnings %v, want none", unchecked, warnings)
	}
}

// The pool uses the line its caller gives, and never one from the namespace
// or the text of the command.
func TestConnPoolUsesTheGivenLine(t *testing.T) {
	fake := newInFlight()
	useReadOnlyFake(t, fake)

	// with 3 connections, mesh reads may use only 1
	p := testPool(t, 3, new(fakeClock))
	errs := make(chan error, 2)

	start(p, MeshRead, meshCommand(readBlock), errs)
	polltest.Until(t, "the mesh read reaches minimega", func() bool { return fake.held() == 1 })

	start(p, MeshRead, command(readBlock), errs)
	polltest.Until(t, "a command with no namespace waits in the MeshRead line", func() bool {
		return waitingIn(p, MeshRead) == 1
	})

	if err := ErrorResponse(p.run(context.Background(), HeadRead, meshCommand(readOK), nil)); err != nil {
		t.Errorf("p.run(HeadRead, namespaced %s) with the mesh reads full error = %v, want nil", readOK, err)
	}

	fake.unblock()
	waitAll(t, errs, 2)
}

// RunReadOnly gives an error response for an unknown line and sends nothing.
func TestRunReadOnlyRejectsAnUnknownLine(t *testing.T) {
	received := useFakeMinimega(t, func(mmtest.Command) []*minicli.Response { return reply("ok") })

	for _, line := range []ReadLine{-1, readLines, 42} {
		err := ErrorResponse(RunReadOnly(context.Background(), line, command(readOK)))
		if !errors.Is(err, errUnknownReadLine) {
			t.Errorf("RunReadOnly(line %d, %s) error = %v, want %v", line, readOK, err, errUnknownReadLine)
		}
	}

	if got := mmtest.Count(received(), readOK); got != 0 {
		t.Errorf("RunReadOnly with unknown lines sent %s %d times, want 0", readOK, got)
	}
}

// RunReadOnlyStarted calls started before the command is sent, and the
// tabular helpers read the rows from the pool, not waiting for the shared
// connection.
func TestRunReadOnlyStartedAndTabular(t *testing.T) {
	var started atomic.Bool

	release := make(chan struct{})
	unhold := sync.OnceFunc(func() { close(release) })

	received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
		switch {
		case cmd.Base == "hold":
			<-release

			return reply("ok")
		case !started.Load():
			t.Errorf("%q was sent before its started func was called", cmd.Base)
		}

		return []*minicli.Response{mmtest.Tabular("head", []string{"name"}, []string{"vm1"})}
	})

	held := make(chan error, 1)

	go func() { held <- ErrorResponse(Run(command("hold"))) }()

	// LIFO: the hold is let go before it is waited for
	t.Cleanup(func() { <-held })
	t.Cleanup(unhold)

	mmtest.Await(t, "hold")

	rows, err := RunTabularReadOnlyChecked(context.Background(), HeadRead, command("vm info"), func() { started.Store(true) })
	if err != nil {
		t.Fatalf("RunTabularReadOnlyChecked(vm info) error = %v", err)
	}

	want := []map[string]string{{"host": "head", "name": "vm1"}}
	if diff := cmp.Diff(want, rows); diff != "" {
		t.Errorf("RunTabularReadOnlyChecked(vm info) mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(want, RunTabularReadOnly(context.Background(), HeadRead, command("vm info"))); diff != "" {
		t.Errorf("RunTabularReadOnly(vm info) mismatch (-want +got):\n%s", diff)
	}

	if got := mmtest.Count(received(), "vm info"); got != 2 {
		t.Errorf("the read-only tabular helpers sent %d vm info, want 2", got)
	}
}
