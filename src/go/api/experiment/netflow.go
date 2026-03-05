package experiment

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"phenix/util/mm"
	"phenix/util/plog"
)

const netflowChannelBufferSize = 100

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
		scanner := bufio.NewScanner(conn)

		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())

			body := make(map[string]any)

			body["proto"], _ = strconv.Atoi(fields[2])

			src := strings.Split(fields[3], ":")
			dst := strings.Split(fields[5], ":")

			body["src"] = src[0]
			body["sport"], _ = strconv.Atoi(src[1])

			body["dst"] = dst[0]
			body["dport"], _ = strconv.Atoi(dst[1])

			body["packets"], _ = strconv.Atoi(fields[6])
			body["bytes"], _ = strconv.Atoi(fields[7])

			flow.Publish(body)
		}
	}()

	return nil
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
