package scorch

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"phenix/api/experiment"
	"phenix/api/scorch/scorchmd"
	"phenix/web/broker"
	bt "phenix/web/broker/brokertypes"
)

const (
	stageConfigure    = "configure"
	stageStart        = "start"
	stageStop         = "stop"
	stageCleanup      = "cleanup"
	stageDone         = "done"
	stageLoop         = "loop"
	defaultEdgeWeight = 2

	idxConfigure = 0
	idxStart     = 1
	idxStop      = 2
	idxCleanup   = 3
	idxDone      = 4
)

/*
Valid status codes client-side:
	unknown
	start
	success
	running
	failure
	paused
	unstable
	end
*/

type node struct {
	Name   string  `json:"name"`
	Hint   string  `json:"hint"`
	Status string  `json:"status"`
	Next   []*edge `json:"next"`

	Exp   string `json:"exp"`
	Run   int    `json:"run"`
	Stage string `json:"stage"`
	Loop  int    `json:"loop"`

	idx   int
	edges map[int]*edge
}

func (n *node) addEdge(target *node, weight int) {
	e := &edge{Index: target.idx, Weight: weight}

	n.Next = append(n.Next, e)

	if n.edges == nil {
		n.edges = make(map[int]*edge)
	}

	n.edges[target.idx] = e
}

func (n *node) updateEdge(target *node, weight int) { //nolint:unparam // weight is always 2
	if e, ok := n.edges[target.idx]; ok {
		e.Weight = weight
	}
}

type edge struct {
	Index  int `json:"index"`
	Weight int `json:"weight"`
}

type pipeline struct {
	// all nodes, ordered
	Pipeline []*node   `json:"pipeline"`
	Loop     *pipeline `json:"loop,omitempty"`
	Name     string    `json:"name,omitempty"`

	exp    string
	runID  int
	loopID int

	// stage nodes
	config  *node
	start   *node
	stop    *node
	cleanup *node
	done    *node
	loop    *node

	// component nodes
	configs  map[string]*node
	starts   map[string]*node
	stops    map[string]*node
	cleanups map[string]*node
}

func (p *pipeline) addNode(stage string, node *node) {
	node.idx = len(p.Pipeline)
	node.Exp = p.exp
	node.Run = p.runID
	node.Loop = p.loopID

	p.Pipeline = append(p.Pipeline, node)

	switch stage {
	case stageConfigure:
		p.configs[node.Name] = node
	case stageStart:
		p.starts[node.Name] = node
	case stageStop:
		p.stops[node.Name] = node
	case stageCleanup:
		p.cleanups[node.Name] = node
	}
}

func (p *pipeline) addComponentToStage(stage string, node *node) {
	node.Stage = stage

	switch stage {
	case stageConfigure:
		p.config.addEdge(node, 0)
	case stageStart:
		p.start.addEdge(node, 0)
	case stageStop:
		p.stop.addEdge(node, 0)
	case stageCleanup:
		p.cleanup.addEdge(node, 0)
	}
}

func (p *pipeline) setStageStatus(stage, status string) bool {
	switch stage {
	case stageConfigure:
		p.config.Status = status

		switch status {
		case statusSuccess, statusFailure:
			p.config.updateEdge(p.start, defaultEdgeWeight)
		}
	case stageStart:
		p.start.Status = status

		switch status {
		case statusSuccess, statusFailure:
			next := p.stop
			if p.loop != nil {
				next = p.loop
			}

			p.start.updateEdge(next, defaultEdgeWeight)
		}
	case stageStop:
		p.stop.Status = status

		switch status {
		case statusSuccess, statusFailure:
			p.stop.updateEdge(p.cleanup, defaultEdgeWeight)
		}
	case stageCleanup:
		p.cleanup.Status = status

		switch status {
		case statusSuccess, statusFailure:
			p.cleanup.updateEdge(p.done, defaultEdgeWeight)
		}
	case stageDone:
		p.done.Status = status
	case stageLoop:
		if p.loop == nil {
			return false
		}

		p.loop.Status = status

		switch status {
		case statusSuccess, statusFailure:
			p.loop.updateEdge(p.stop, defaultEdgeWeight)
		}
	default:
		return false
	}

	return true
}

//nolint:cyclop,funlen,gocyclo // complex logic
func (p *pipeline) updateNodeStatus(stage, name, status string) bool {
	switch stage {
	case stageConfigure:
		node, ok := p.configs[name]
		if !ok {
			return false
		}

		node.Status = status

		switch status {
		case statusRunning, statusUnstable:
			p.config.Status = statusRunning
			p.config.updateEdge(node, defaultEdgeWeight)
		case statusBackground:
			p.config.Status = statusRunning
			p.config.updateEdge(node, defaultEdgeWeight)

			fallthrough
		case statusSuccess:
			complete := true

			for _, v := range p.configs {
				if v.Status != statusBackground && v.Status != statusSuccess {
					complete = false

					break
				}
			}

			if complete {
				p.config.Status = statusSuccess

				for _, v := range p.configs {
					v.updateEdge(p.start, defaultEdgeWeight)
				}
			}
		case statusFailure:
			p.config.Status = statusFailure
			p.config.addEdge(p.cleanup, defaultEdgeWeight)
		}
	case stageStart:
		node, ok := p.starts[name]
		if !ok {
			return false
		}

		node.Status = status

		switch status {
		case statusRunning, statusUnstable:
			p.start.Status = statusRunning
			p.start.updateEdge(node, defaultEdgeWeight)
		case statusBackground:
			p.start.Status = statusRunning
			p.start.updateEdge(node, defaultEdgeWeight)

			fallthrough
		case statusSuccess:
			complete := true

			for _, v := range p.starts {
				if v.Status != statusBackground && v.Status != statusSuccess {
					complete = false

					break
				}
			}

			if complete {
				p.start.Status = statusSuccess

				for _, v := range p.starts {
					next := p.stop
					if p.loop != nil {
						next = p.loop
					}

					v.updateEdge(next, defaultEdgeWeight)
				}
			}
		case statusFailure:
			p.start.Status = statusFailure
			p.start.addEdge(p.stop, defaultEdgeWeight)
		}
	case stageStop:
		node, ok := p.stops[name]
		if !ok {
			return false
		}

		node.Status = status

		switch status {
		case statusRunning, statusUnstable:
			p.stop.Status = statusRunning
			p.stop.updateEdge(node, defaultEdgeWeight)
		}

		complete := true
		finalStatus := statusSuccess

		for _, v := range p.stops {
			switch v.Status {
			case statusSuccess:
			case statusFailure:
				finalStatus = statusFailure
			default:
				complete = false
			}

			if !complete {
				break
			}
		}

		if complete {
			p.stop.Status = finalStatus

			for _, v := range p.stops {
				v.updateEdge(p.cleanup, defaultEdgeWeight)
			}
		}
	case stageCleanup:
		node, ok := p.cleanups[name]
		if !ok {
			return false
		}

		node.Status = status

		switch status {
		case statusRunning, statusUnstable:
			p.cleanup.Status = statusRunning
			p.cleanup.updateEdge(node, defaultEdgeWeight)
		}

		complete := true
		finalStatus := statusSuccess

		for _, v := range p.cleanups {
			switch v.Status {
			case statusSuccess:
			case statusFailure:
				finalStatus = statusFailure
			default:
				complete = false
			}

			if !complete {
				break
			}
		}

		if complete {
			p.cleanup.Status = finalStatus

			for _, v := range p.cleanups {
				v.updateEdge(p.done, defaultEdgeWeight)
			}
		}
	default:
		return false
	}

	return true
}

func newPipeline(exp, name string, run, loop int) *pipeline {
	var (
		config = &node{ //nolint:exhaustruct // partial initialization
			Name:   stageConfigure,
			Status: statusUnknown,
			Exp:    exp,
			Run:    run,
			Loop:   loop,
			idx:    idxConfigure,
		}
		start = &node{
			Name:   stageStart,
			Status: statusUnknown,
			Exp:    exp,
			Run:    run,
			Loop:   loop,
			idx:    idxStart,
			Hint:   "",
			Next:   nil,
			Stage:  "",
			edges:  nil,
		}
		stop = &node{
			Name:   stageStop,
			Status: statusUnknown,
			Exp:    exp,
			Run:    run,
			Loop:   loop,
			idx:    idxStop,
			Hint:   "",
			Next:   nil,
			Stage:  "",
			edges:  nil,
		}
		cleanup = &node{
			Name:   stageCleanup,
			Status: statusUnknown,
			Exp:    exp,
			Run:    run,
			Loop:   loop,
			idx:    idxCleanup,
			Hint:   "",
			Next:   nil,
			Stage:  "",
			edges:  nil,
		}
		done = &node{
			Name:   stageDone,
			Status: statusUnknown,
			Exp:    exp,
			Run:    run,
			Loop:   loop,
			idx:    idxDone,
			Hint:   "",
			Next:   nil,
			Stage:  "",
			edges:  nil,
		}
	)

	return &pipeline{ //nolint:exhaustruct // partial initialization
		Pipeline: []*node{config, start, stop, cleanup, done},
		Name:     name,

		exp:    exp,
		runID:  run,
		loopID: loop,

		config:  config,
		start:   start,
		stop:    stop,
		cleanup: cleanup,
		done:    done,

		configs:  make(map[string]*node),
		starts:   make(map[string]*node),
		stops:    make(map[string]*node),
		cleanups: make(map[string]*node),
	}
}

func getPipeline(name string, run, loop int) (*pipeline, error) {
	if _, ok := pipelines[name]; ok {
		if _, ok := pipelines[name][run]; ok {
			if pl, ok := pipelines[name][run][loop]; ok {
				return pl, nil
			}
		}
	}

	exp, err := experiment.Get(name)
	if err != nil {
		return nil, fmt.Errorf("unable to get experiment from store: %w", err)
	}

	md, err := scorchmd.DecodeMetadata(exp)
	if err != nil {
		return nil, fmt.Errorf("unable to decode scorch metadata: %w", err)
	}

	var (
		exe     = md.Runs[run]
		runName = exe.Name
	)

	for i := 1; i <= loop; i++ {
		if exe.Loop == nil {
			return nil, fmt.Errorf("loop %d not configured", i)
		}

		exe = exe.Loop
	}

	pl := newPipeline(name, runName, run, loop)

	if len(exe.Configure) == 0 {
		pl.addComponentToStage(stageConfigure, pl.start)
	} else {
		for _, cmp := range exe.Configure {
			n := &node{Name: cmp, Status: statusUnknown} //nolint:exhaustruct // partial initialization
			n.addEdge(pl.start, 0)

			pl.addNode(stageConfigure, n)
			pl.addComponentToStage(stageConfigure, n)
		}
	}

	var next *node

	if exe.Loop == nil {
		next = pl.stop
	} else {
		next = &node{Name: stageLoop, Status: statusUnknown} //nolint:exhaustruct // partial initialization
		next.addEdge(pl.stop, 0)

		pl.addNode("", next)
		pl.loop = next
	}

	if len(exe.Start) == 0 {
		pl.addComponentToStage(stageStart, next)
	} else {
		for _, cmp := range exe.Start {
			n := &node{Name: cmp, Status: statusUnknown} //nolint:exhaustruct // partial initialization
			n.addEdge(next, 0)

			pl.addNode(stageStart, n)
			pl.addComponentToStage(stageStart, n)
		}
	}

	if len(exe.Stop) == 0 {
		pl.addComponentToStage(stageStop, pl.cleanup)
	} else {
		for _, cmp := range exe.Stop {
			n := &node{Name: cmp, Status: statusUnknown} //nolint:exhaustruct // partial initialization
			n.addEdge(pl.cleanup, 0)

			pl.addNode(stageStop, n)
			pl.addComponentToStage(stageStop, n)
		}
	}

	if len(exe.Cleanup) == 0 {
		pl.addComponentToStage(stageCleanup, pl.done)
	} else {
		for _, cmp := range exe.Cleanup {
			n := &node{Name: cmp, Status: statusUnknown} //nolint:exhaustruct // partial initialization
			n.addEdge(pl.done, 0)

			pl.addNode(stageCleanup, n)
			pl.addComponentToStage(stageCleanup, n)
		}
	}

	if _, ok := pipelines[name]; !ok {
		pipelines[name] = make(map[int]map[int]*pipeline)
	}

	if _, ok := pipelines[name][run]; !ok {
		pipelines[name][run] = make(map[int]*pipeline)
	}

	pipelines[name][run][loop] = pl

	return pl, nil
}

func updatePipeline(update PipelineUpdate) error {
	pl, err := getPipeline(update.Exp, update.Run, update.Loop)
	if err != nil {
		return fmt.Errorf("getting pipeline %d for experiment %s: %w", update.Run, update.Exp, err)
	}

	if update.CmpName == "" {
		if pl.setStageStatus(update.Stage, update.Status) {
			broadcastPipeline(update.Exp, update.Run, update.Loop, pl)
		}

		return nil
	}

	if update.CmpType == "break" {
		loopStat := statusRunning

		if update.Status == statusRunning {
			// use `unstable` state to represent running break component needing
			// attention, both for the break component node and parent loop nodes
			update.Status = statusUnstable
			loopStat = statusUnstable
		}

		for i := update.Loop - 1; i >= 0; i-- {
			pl, _ := getPipeline(update.Exp, update.Run, i)
			pl.setStageStatus(stageLoop, loopStat)

			broadcastPipeline(update.Exp, update.Run, i, pl)
		}
	}

	if pl.updateNodeStatus(update.Stage, update.CmpName, update.Status) {
		broadcastPipeline(update.Exp, update.Run, update.Loop, pl)
	}

	return nil
}

func broadcastPipeline(exp string, run, loop int, pl *pipeline) {
	name := fmt.Sprintf("%s/%d/%d", exp, run, loop)
	body, _ := json.Marshal(pl)

	resource := bt.NewResource("apps/scorch", name, "pipeline-update")
	broker.Broadcast(nil, resource, body)
}

type PipelineUpdate struct {
	ComponentUpdate

	resp chan error
}

type pipelineRequest struct {
	exp  string
	run  int
	loop int

	resp chan pipelineResponse
}

type pipelineResponse struct {
	pl  *pipeline
	err error
}

type pipelineDelete struct {
	exp     string
	run     int
	loop    int
	rebuild bool

	done chan struct{}
}

var (
	// maps to experiment, run, loop...
	pipelines        map[string]map[int]map[int]*pipeline //nolint:gochecknoglobals // global state
	pipelineUpdates  chan PipelineUpdate                  //nolint:gochecknoglobals // global state
	pipelineRequests chan pipelineRequest                 //nolint:gochecknoglobals // global state
	pipelineDeletes  chan pipelineDelete                  //nolint:gochecknoglobals // global state
)

func RequestPipeline(exp string, run, loop int) (*pipeline, error) {
	if pipelines == nil {
		return nil, errors.New("pipelines not initialized")
	}

	resp := make(chan pipelineResponse)

	pipelineRequests <- pipelineRequest{exp, run, loop, resp}

	r := <-resp

	return r.pl, r.err
}

func UpdatePipeline(update ComponentUpdate) error {
	if pipelineUpdates == nil {
		return nil
	}

	pu := PipelineUpdate{
		ComponentUpdate: update,
		resp:            make(chan error),
	}

	pipelineUpdates <- pu

	return <-pu.resp
}

func DeletePipeline(exp string, run, loop int, rebuild bool) {
	if pipelineDeletes != nil {
		done := make(chan struct{})

		pipelineDeletes <- pipelineDelete{exp, run, loop, rebuild, done}

		<-done
	}
}

func processPipelines() {
	pipelines = make(map[string]map[int]map[int]*pipeline)
	pipelineUpdates = make(chan PipelineUpdate)
	pipelineRequests = make(chan pipelineRequest)
	pipelineDeletes = make(chan pipelineDelete)

	for {
		select {
		case update := <-pipelineUpdates:
			err := updatePipeline(update)
			update.resp <- err
		case req := <-pipelineRequests:
			pl, err := getPipeline(req.exp, req.run, req.loop)
			req.resp <- pipelineResponse{pl, err}
		case del := <-pipelineDeletes:
			deleteLoop := func(loop int, rebuild bool) {
				if _, ok := pipelines[del.exp]; ok {
					if _, ok := pipelines[del.exp][del.run]; ok {
						delete(pipelines[del.exp][del.run], loop)
					}
				}

				if rebuild {
					if pl, err := getPipeline(del.exp, del.run, loop); err == nil {
						broadcastPipeline(del.exp, del.run, loop, pl)
					}
				}
			}

			if del.loop < 0 {
				if !del.rebuild {
					delete(pipelines, del.exp)
					close(del.done)

					continue
				}

				var loops []int

				if _, ok := pipelines[del.exp]; ok {
					if _, ok := pipelines[del.exp][del.run]; ok {
						for loop := range pipelines[del.exp][del.run] {
							loops = append(loops, loop)
						}
					}
				}

				sort.Ints(loops)

				for _, loop := range loops {
					deleteLoop(loop, true)
				}
			} else {
				deleteLoop(del.loop, del.rebuild)
			}

			close(del.done)
		}
	}
}
