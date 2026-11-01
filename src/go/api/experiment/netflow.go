package experiment

import (
	"errors"
	"fmt"
	"math"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"phenix/util/file"
	"phenix/util/mm"
	"phenix/util/plog"
)

const (
	netflowChannelBufferSize = 100
	netflowMinFieldCount     = 8
	netflowMaxDatagramSize   = 65535
	netflowReadBuffer        = 4 << 20
)

type Netflow struct {
	mu sync.RWMutex

	Bridge string
	Conn   *net.UDPConn

	callbacks map[string]chan map[string]any

	logMu   sync.Mutex
	lastLog map[string]time.Time

	// recorder, set only at construction, records the capture; nil when it is
	// not recorded.
	recorder *netflowRecorder
}

func newNetflow(bridge string, conn *net.UDPConn, rec *netflowRecorder) *Netflow {
	return &Netflow{
		mu:     sync.RWMutex{},
		Bridge: bridge,
		Conn:   conn,

		callbacks: make(map[string]chan map[string]any),
		logMu:     sync.Mutex{},
		lastLog:   make(map[string]time.Time),
		recorder:  rec,
	}
}

func (n *Netflow) NewChannel(id string) chan map[string]any {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.callbacks == nil {
		return nil
	}

	if _, ok := n.callbacks[id]; ok {
		return nil
	}

	cb := make(chan map[string]any, netflowChannelBufferSize)

	n.callbacks[id] = cb

	return cb
}

func (n *Netflow) DeleteChannel(id string) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if cb, ok := n.callbacks[id]; ok {
		close(cb)

		for range cb { //nolint:revive // draining channel
			// draining channel so it doesn't block anything
		}
	}

	delete(n.callbacks, id)

	n.logMu.Lock()
	delete(n.lastLog, id)
	n.logMu.Unlock()
}

func (n *Netflow) Publish(body map[string]any) {
	n.mu.RLock()
	defer n.mu.RUnlock()

	for id, cb := range n.callbacks {
		// Use a non-blocking send to prevent a slow consumer from blocking the
		// publisher. If the channel's buffer is full (or it's unbuffered and no
		// receiver is ready), the message will be dropped for that specific
		// consumer.
		select {
		case cb <- body:
			// Message sent successfully
		default:
			n.logMu.Lock()
			if time.Since(n.lastLog[id]) > 5*time.Second {
				// Log that we are dropping a packet for this consumer.
				// This helps identify slow or stuck consumers.
				plog.Warn(
					plog.TypeSystem,
					"netflow: dropping packet for consumer, buffer is full",
					"consumer",
					id,
				)
				n.lastLog[id] = time.Now()
			}
			n.logMu.Unlock()
		}
	}
}

func (n *Netflow) Close() {
	n.mu.Lock()

	for _, cb := range n.callbacks {
		close(cb)
	}

	n.callbacks = nil
	n.lastLog = nil
	_ = n.Conn.Close()

	n.mu.Unlock()

	if n.recorder != nil {
		n.recorder.close(time.Now())
	}
}

type netflowState int

const (
	netflowStarting netflowState = iota
	netflowRunning
	netflowStopping
)

// netflowEntry is an experiment's netflow capture. Its flow is set once the
// capture is running.
type netflowEntry struct {
	flow  *Netflow
	state netflowState
}

// netflowMu guards netflows, and only it: the mesh commands that start and
// stop captures run without it, so one experiment's capture never waits on
// another's.
var (
	netflows  = make(map[string]*netflowEntry) //nolint:gochecknoglobals // package level registry
	netflowMu sync.RWMutex                     //nolint:gochecknoglobals // package level registry

	ErrNetflowNotStarted     = errors.New("netflow not started for experiment")
	ErrNetflowAlreadyStarted = errors.New("netflow already started for experiment")
	ErrNetflowPhenixBridge   = errors.New("cannot capture netflow on default phenix bridge")
)

func init() { //nolint:gochecknoinits // package level setup
	// Delete netflow captures when experiments are stopped.
	RegisterHook("stop", func(_, name string) {
		netflowMu.Lock()
		entry, ok := netflows[name]
		delete(netflows, name)
		netflowMu.Unlock()

		// We don't need to worry about instructing minimega to delete the netflow
		// capture since that will happen as part of the minimega namespace for
		// this experiment being cleared. A capture still starting sees its entry
		// gone and closes itself.
		if ok && entry.flow != nil {
			entry.flow.Close()
		}
	})
}

// GetNetflow returns the experiment's running netflow capture, or nil.
func GetNetflow(exp string) *Netflow {
	netflowMu.RLock()
	defer netflowMu.RUnlock()

	if entry, ok := netflows[exp]; ok && entry.state == netflowRunning {
		return entry.flow
	}

	return nil
}

// forgetNetflow removes entry from the registry, unless it was replaced.
func forgetNetflow(exp string, entry *netflowEntry) {
	netflowMu.Lock()
	defer netflowMu.Unlock()

	if netflows[exp] == entry {
		delete(netflows, exp)
	}
}

// StartNetflow starts the experiment's netflow capture, recording up to
// maxSize bytes of it; 0 does not record it.
func StartNetflow(exp string, maxSize int64) error {
	// reserve the experiment's capture before any I/O
	netflowMu.Lock()

	if _, ok := netflows[exp]; ok {
		netflowMu.Unlock()

		return ErrNetflowAlreadyStarted
	}

	entry := &netflowEntry{flow: nil, state: netflowStarting}
	netflows[exp] = entry

	netflowMu.Unlock()

	flow, err := startNetflow(exp, maxSize)
	if err != nil {
		forgetNetflow(exp, entry)

		return err
	}

	netflowMu.Lock()

	// the experiment stopped while the capture was starting
	if netflows[exp] != entry {
		netflowMu.Unlock()

		if flow.recorder != nil {
			flow.recorder.discard()
		}

		flow.Close()

		return ErrExperimentNotRunning
	}

	entry.flow = flow
	entry.state = netflowRunning

	netflowMu.Unlock()

	go readNetflow(exp, flow)

	return nil
}

// startNetflow asks every cluster node to send the experiment's netflow to a
// new UDP listener, recording it first when maxSize is positive.
func startNetflow(exp string, maxSize int64) (*Netflow, error) {
	spec, err := Get(exp)
	if err != nil {
		return nil, ErrExperimentNotFound
	}

	if !spec.Running() {
		return nil, ErrExperimentNotRunning
	}

	if spec.Spec.DefaultBridge() == "phenix" {
		return nil, ErrNetflowPhenixBridge
	}

	cluster, _ := ClusterNodes(exp)

	conn, err := net.ListenUDP("udp4", nil)
	if err != nil {
		return nil, fmt.Errorf("creating UDP listener: %w", err)
	}

	if err := conn.SetReadBuffer(netflowReadBuffer); err != nil {
		plog.Warn(plog.TypeSystem, "enlarging netflow receive buffer", "exp", exp, "err", err)
	}

	// live viewing works on, unrecorded, when the capture cannot be recorded
	var rec *netflowRecorder

	if maxSize > 0 {
		rec, err = newNetflowRecorder(exp, netflowDir(spec.FilesDir()), maxSize, time.Now())
		if err != nil {
			plog.Error(plog.TypeSystem, "recording netflow capture", "exp", exp, "err", err)
		} else {
			file.InvalidateExperimentFiles(exp)
		}
	}

	failed := func() {
		_ = conn.Close()

		if rec != nil {
			rec.discard()
		}
	}

	addr := strings.Split(conn.LocalAddr().String(), ":")
	// The mode command is already broadcast by minimega. Sending it through
	// every cluster node can cause overlapping mesh broadcasts while a new
	// capture is being configured.
	if err := mm.MeshSend(exp, mm.Headnode(), "capture netflow mode ascii"); err != nil {
		failed()
		return nil, fmt.Errorf("setting netflow capture mode: %w", err)
	}

	cmd := fmt.Sprintf(
		"capture netflow bridge %s udp %s:%s",
		spec.Spec.DefaultBridge(),
		mm.Headnode(),
		addr[1],
	)
	for _, node := range cluster {
		err = mm.MeshSend(exp, node, cmd)
		if err != nil {
			failed()
			return nil, fmt.Errorf("starting netflow capture on node %s: %w", node, err)
		}
	}

	return newNetflow(spec.Spec.DefaultBridge(), conn, rec), nil
}

// readNetflow publishes the capture's records until its connection closes.
func readNetflow(exp string, flow *Netflow) {
	// minimega sends each netflow packet as one datagram of newline-terminated
	// records. Reading per datagram bounds memory and keeps a malformed
	// datagram from bleeding into the next one.
	buf := make([]byte, netflowMaxDatagramSize)

	for {
		n, err := flow.Conn.Read(buf)
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				plog.Warn(plog.TypeSystem, "reading netflow capture", "exp", exp, "err", err)
			}

			return
		}

		var lines []string

		for line := range strings.SplitSeq(string(buf[:n]), "\n") {
			if strings.TrimSpace(line) != "" {
				lines = append(lines, line)
			}
		}

		if flow.recorder != nil {
			flow.recorder.record(lines)
		}

		for _, line := range lines {
			line = strings.TrimSpace(line)

			body, ok := parseNetflowLine(line)
			if !ok {
				plog.Debug(plog.TypeSystem, "dropping malformed netflow line", "exp", exp, "line", line)
				continue
			}

			flow.Publish(body)
		}
	}
}

func parseNetflowLine(line string) (map[string]any, bool) {
	fields := strings.Fields(line)
	if len(fields) < netflowMinFieldCount {
		return nil, false
	}

	f, ok := parseNetflowFields(fields)
	if !ok || f.packets > math.MaxInt || f.bytes > math.MaxInt {
		return nil, false
	}

	return map[string]any{
		"proto":   int(f.proto),
		"src":     f.srcHost,
		"sport":   int(f.sport),
		"dst":     f.dstHost,
		"dport":   int(f.dport),
		"packets": int(f.packets),
		"bytes":   int(f.bytes),
	}, true
}

// netflowFields are fields 2 to 7 of a minimega ascii netflow line: the
// protocol, both endpoints, and the packet and byte counts.
type netflowFields struct {
	proto            uint8
	srcHost, dstHost string
	src, dst         netip.Addr
	sport, dport     uint16
	packets, bytes   uint64
}

// parseNetflowFields parses fields 2 to 7 of a line's fields, which number at
// least netflowMinFieldCount.
func parseNetflowFields(fields []string) (netflowFields, bool) {
	var (
		f   netflowFields
		ok  bool
		err error
	)

	if fields[4] != "<->" {
		return f, false
	}

	proto, err := strconv.ParseUint(fields[2], 10, 8)
	if err != nil {
		return f, false
	}

	f.proto = uint8(proto)

	if f.srcHost, f.src, f.sport, ok = parseNetflowEndpoint(fields[3]); !ok {
		return f, false
	}

	if f.dstHost, f.dst, f.dport, ok = parseNetflowEndpoint(fields[5]); !ok {
		return f, false
	}

	if f.packets, err = strconv.ParseUint(fields[6], 10, 64); err != nil {
		return f, false
	}

	if f.bytes, err = strconv.ParseUint(fields[7], 10, 64); err != nil {
		return f, false
	}

	return f, true
}

// parseNetflowEndpoint parses an IP address and port, refusing a zone.
func parseNetflowEndpoint(text string) (string, netip.Addr, uint16, bool) {
	host, portText, ok := splitAddr(text)
	if !ok {
		return "", netip.Addr{}, 0, false
	}

	addr, err := netip.ParseAddr(host)
	if err != nil || addr.Zone() != "" {
		return "", netip.Addr{}, 0, false
	}

	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil {
		return "", netip.Addr{}, 0, false
	}

	return host, addr, uint16(port), true
}

// splitAddr splits a netflow address into host and port. It accepts the
// bracketed form ("[2001:db8::1]:53") and, falling back to the last colon,
// an unbracketed IPv6 host ("fe80::1:22") so the host keeps its colons.
func splitAddr(addr string) (string, string, bool) {
	if host, port, err := net.SplitHostPort(addr); err == nil {
		return host, port, true
	}

	i := strings.LastIndex(addr, ":")
	if i < 0 {
		return "", "", false
	}

	return addr[:i], addr[i+1:], true
}

func StopNetflow(exp string) error {
	netflowMu.Lock()

	entry, ok := netflows[exp]
	if !ok || entry.state != netflowRunning {
		netflowMu.Unlock()

		return ErrNetflowNotStarted
	}

	entry.state = netflowStopping

	netflowMu.Unlock()

	cluster, _ := ClusterNodes(exp)

	cmd := "capture netflow delete bridge " + entry.flow.Bridge

	for _, node := range cluster {
		err := mm.MeshSend(exp, node, cmd)
		if err != nil {
			netflowMu.Lock()
			if netflows[exp] == entry {
				entry.state = netflowRunning
			}
			netflowMu.Unlock()

			return fmt.Errorf("deleting netflow capture on node %s: %w", node, err)
		}
	}

	forgetNetflow(exp, entry)

	// the stop hook may have closed it already; closing twice is safe
	entry.flow.Close()

	return nil
}
