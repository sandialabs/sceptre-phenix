package experiment

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"phenix/util/mm"
	"phenix/util/plog"
)

const (
	netflowChannelBufferSize = 100
	netflowMinFieldCount     = 8
)

type Netflow struct {
	mu sync.RWMutex

	Bridge string
	Conn   *net.UDPConn

	callbacks map[string]chan map[string]any

	logMu   sync.Mutex
	lastLog map[string]time.Time
}

func NewNetflow(bridge string, conn *net.UDPConn) *Netflow {
	return &Netflow{
		mu:     sync.RWMutex{},
		Bridge: bridge,
		Conn:   conn,

		callbacks: make(map[string]chan map[string]any),
		logMu:     sync.Mutex{},
		lastLog:   make(map[string]time.Time),
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
	defer n.mu.Unlock()

	for _, cb := range n.callbacks {
		close(cb)
	}

	n.callbacks = nil
	n.lastLog = nil
	_ = n.Conn.Close()
}

var (
	netflows  = make(map[string]*Netflow) //nolint:gochecknoglobals // package level registry
	netflowMu sync.RWMutex                //nolint:gochecknoglobals // package level registry

	ErrNetflowNotStarted     = errors.New("netflow not started for experiment")
	ErrNetflowAlreadyStarted = errors.New("netflow already started for experiment")
	ErrNetflowPhenixBridge   = errors.New("cannot capture netflow on default phenix bridge")
)

func init() { //nolint:gochecknoinits // package level setup
	// Delete netflow captures when experiments are stopped.
	RegisterHook("stop", func(_, name string) {
		netflowMu.Lock()
		defer netflowMu.Unlock()

		if flow, ok := netflows[name]; ok {
			// We don't need to worry about instructing minimega to delete the netflow
			// capture since that will happen as part of the minimega namespace for
			// this experiment being cleared.
			flow.Close()

			delete(netflows, name)
		}
	})
}

func GetNetflow(exp string) *Netflow {
	netflowMu.RLock()
	defer netflowMu.RUnlock()

	if flow, ok := netflows[exp]; ok {
		return flow
	}

	return nil
}

func StartNetflow(exp string) error {
	netflowMu.Lock()
	defer netflowMu.Unlock()

	if _, ok := netflows[exp]; ok {
		return ErrNetflowAlreadyStarted
	}

	spec, err := Get(exp)
	if err != nil {
		return ErrExperimentNotFound
	}

	if !spec.Running() {
		return ErrExperimentNotRunning
	}

	if spec.Spec.DefaultBridge() == "phenix" {
		return ErrNetflowPhenixBridge
	}

	cluster, _ := ClusterNodes(exp)

	conn, err := net.ListenUDP("udp4", nil)
	if err != nil {
		return fmt.Errorf("creating UDP listener: %w", err)
	}

	addr := strings.Split(conn.LocalAddr().String(), ":")
	cmds := []string{
		"capture netflow mode ascii",
		fmt.Sprintf(
			"capture netflow bridge %s udp %s:%s",
			spec.Spec.DefaultBridge(),
			mm.Headnode(),
			addr[1],
		),
	}

	for _, cmd := range cmds {
		for _, node := range cluster {
			err = mm.MeshSend(exp, node, cmd)
			if err != nil {
				_ = conn.Close()

				return fmt.Errorf("starting netflow capture on node %s: %w", node, err)
			}
		}
	}

	flow := NewNetflow(spec.Spec.DefaultBridge(), conn)
	netflows[exp] = flow

	go func() {
		reader := bufio.NewReader(conn)

		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				if errors.Is(err, io.EOF) {
					return
				}
				if errors.Is(err, net.ErrClosed) || errors.Is(err, os.ErrClosed) ||
					strings.Contains(err.Error(), "use of closed network connection") {
					return
				}
				plog.Warn(plog.TypeSystem, "reading netflow capture", "exp", exp, "err", err)
				return
			}

			line = strings.TrimSpace(line)
			body, ok := parseNetflowLine(line)
			if !ok {
				plog.Debug(plog.TypeSystem, "dropping malformed netflow line", "exp", exp, "line", line)
				continue
			}

			flow.Publish(body)
		}
	}()

	return nil
}

func parseNetflowLine(line string) (map[string]any, bool) {
	fields := strings.Fields(line)
	if len(fields) < netflowMinFieldCount || fields[4] != "->" {
		return nil, false
	}

	proto, err := strconv.Atoi(fields[2])
	if err != nil || proto < 0 || proto > 255 {
		return nil, false
	}

	srcHost, srcPortText, err := net.SplitHostPort(fields[3])
	if err != nil || net.ParseIP(srcHost) == nil {
		return nil, false
	}
	srcPort, err := strconv.Atoi(srcPortText)
	if err != nil || srcPort < 0 || srcPort > 65535 {
		return nil, false
	}

	dstHost, dstPortText, err := net.SplitHostPort(fields[5])
	if err != nil || net.ParseIP(dstHost) == nil {
		return nil, false
	}
	dstPort, err := strconv.Atoi(dstPortText)
	if err != nil || dstPort < 0 || dstPort > 65535 {
		return nil, false
	}

	packets, err := strconv.Atoi(fields[6])
	if err != nil || packets < 0 {
		return nil, false
	}
	bytes, err := strconv.Atoi(fields[7])
	if err != nil || bytes < 0 {
		return nil, false
	}

	return map[string]any{
		"proto":   proto,
		"src":     srcHost,
		"sport":   srcPort,
		"dst":     dstHost,
		"dport":   dstPort,
		"packets": packets,
		"bytes":   bytes,
	}, true
}

func StopNetflow(exp string) error {
	netflowMu.Lock()
	defer netflowMu.Unlock()

	flow, ok := netflows[exp]
	if !ok {
		return ErrNetflowNotStarted
	}

	cluster, _ := ClusterNodes(exp)

	cmd := "capture netflow delete bridge " + flow.Bridge

	for _, node := range cluster {
		err := mm.MeshSend(exp, node, cmd)
		if err != nil {
			return fmt.Errorf("deleting netflow capture on node %s: %w", node, err)
		}
	}

	flow.Close()
	delete(netflows, exp)

	return nil
}
