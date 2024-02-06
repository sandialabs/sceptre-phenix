package experiment

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"

	"phenix/util/mm"
)

type Netflow struct {
	sync.RWMutex

	Bridge string
	Conn   *net.UDPConn

	callbacks map[string]chan map[string]any
}

func NewNetflow(bridge string, conn *net.UDPConn) *Netflow {
	return &Netflow{
		Bridge: bridge,
		Conn:   conn,

		callbacks: make(map[string]chan map[string]any),
	}
}

func (this *Netflow) NewChannel(id string) chan map[string]any {
	this.Lock()
	defer this.Unlock()

	if _, ok := this.callbacks[id]; ok {
		return nil
	}

	cb := make(chan map[string]any)

	this.callbacks[id] = cb

	return cb
}

func (this *Netflow) DeleteChannel(id string) {
	this.Lock()
	defer this.Unlock()

	if cb, ok := this.callbacks[id]; ok {
		close(cb)

		for range cb {
			// draining channel so it doesn't block anything
		}
	}

	delete(this.callbacks, id)
}

func (this *Netflow) Publish(body map[string]any) {
	this.RLock()
	defer this.RUnlock()

	for _, cb := range this.callbacks {
		cb <- body
	}
}

func (this *Netflow) Close() {
	this.Lock()
	defer this.Unlock()

	for _, cb := range this.callbacks {
		close(cb)
	}

	this.callbacks = nil
	this.Conn.Close()
}

var (
	netflows  = make(map[string]*Netflow)
	netflowMu sync.RWMutex

	ErrNetflowNotStarted     = errors.New("netflow not started for experiment")
	ErrNetflowAlreadyStarted = errors.New("netflow already started for experiment")
	ErrNetflowPhenixBridge   = errors.New("cannot capture netflow on default phenix bridge")
)

func init() {
	// Delete netflow captures when experiments are stopped.
	RegisterHook("stop", func(stage, name string) {
		netflowMu.RLock()
		defer netflowMu.RUnlock()

		if flow, ok := netflows[name]; ok {
			// We don't need to worry about instructing minimega to delete the netflow
			// capture since that will happen as part of the minimega namespace for
			// this experiment being cleared.

			flow.Conn.Close()
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
		fmt.Sprintf("capture netflow bridge %s udp %s:%s", spec.Spec.DefaultBridge(), mm.Headnode(), addr[1]),
	}

	for _, cmd := range cmds {
		for _, node := range cluster {
			if err := mm.MeshSend(exp, node, cmd); err != nil {
				conn.Close()
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

	cmd := fmt.Sprintf("capture netflow delete bridge %s", flow.Bridge)

	for _, node := range cluster {
		if err := mm.MeshSend(exp, node, cmd); err != nil {
			return fmt.Errorf("deleting netflow capture on node %s: %w", node, err)
		}
	}

	flow.Close()
	delete(netflows, exp)

	return nil
}
