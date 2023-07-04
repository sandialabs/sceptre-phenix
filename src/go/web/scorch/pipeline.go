package scorch

import (
	"encoding/json"
	"fmt"
	"sort"

	"phenix/api/experiment"
	"phenix/api/scorch/scorchmd"
	"phenix/web/broker"

	bt "phenix/web/broker/brokertypes"
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

func (this *node) addEdge(n *node, weight int) {
	e := &edge{Index: n.idx, Weight: weight}

	this.Next = append(this.Next, e)

	if this.edges == nil {
		this.edges = make(map[int]*edge)
	}

	this.edges[n.idx] = e
}

func (this *node) updateEdge(n *node, weight int) {
	if e, ok := this.edges[n.idx]; ok {
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

func (this *pipeline) addNode(stage string, node *node) {
	node.idx = len(this.Pipeline)
	node.Exp = this.exp
	node.Run = this.runID
	node.Loop = this.loopID

	this.Pipeline = append(this.Pipeline, node)

	switch stage {
	case "configure":
		this.configs[node.Name] = node
	case "start":
		this.starts[node.Name] = node
	case "stop":
		this.stops[node.Name] = node
	case "cleanup":
		this.cleanups[node.Name] = node
	}
}

func (this *pipeline) addComponentToStage(stage string, node *node) {
	node.Stage = stage

	switch stage {
	case "configure":
		this.config.addEdge(node, 0)
	case "start":
		this.start.addEdge(node, 0)
	case "stop":
		this.stop.addEdge(node, 0)
	case "cleanup":
		this.cleanup.addEdge(node, 0)
	}
}

func (this *pipeline) setStageStatus(stage string, status string) bool {
	switch stage {
	case "configure":
		this.config.Status = status

		switch status {
		case "success", "failure":
			this.config.updateEdge(this.start, 2)
		}
	case "start":
		this.start.Status = status

		switch status {
		case "success", "failure":
			next := this.stop
			if this.loop != nil {
				next = this.loop
			}

			this.start.updateEdge(next, 2)
		}
	case "stop":
		this.stop.Status = status

		switch status {
		case "success", "failure":
			this.stop.updateEdge(this.cleanup, 2)
		}
	case "cleanup":
		this.cleanup.Status = status

		switch status {
		case "success", "failure":
			this.cleanup.updateEdge(this.done, 2)
		}
	case "done":
		this.done.Status = status
	case "loop":
		if this.loop == nil {
			return false
		}

		this.loop.Status = status

		switch status {
		case "success", "failure":
			this.loop.updateEdge(this.stop, 2)
		}
	default:
		return false
	}

	return true
}

func (this *pipeline) updateNodeStatus(stage string, name string, status string) bool {
	switch stage {
	case "configure":
		node, ok := this.configs[name]
		if !ok {
			return false
		}

		node.Status = status

		switch status {
		case "running", "unstable":
			this.config.Status = "running"
			this.config.updateEdge(node, 2)
		case "background":
			this.config.Status = "running"
			this.config.updateEdge(node, 2)

			fallthrough
		case "success":
			complete := true

			for _, v := range this.configs {
				if v.Status != "background" && v.Status != "success" {
					complete = false
					break
				}
			}

			if complete {
				this.config.Status = "success"

				for _, v := range this.configs {
					v.updateEdge(this.start, 2)
				}
			}
		case "failure":
			this.config.Status = "failure"
			this.config.addEdge(this.cleanup, 2)
		}
	case "start":
		node, ok := this.starts[name]
		if !ok {
			return false
		}

		node.Status = status

		switch status {
		case "running", "unstable":
			this.start.Status = "running"
			this.start.updateEdge(node, 2)
		case "background":
			this.start.Status = "running"
			this.start.updateEdge(node, 2)

			fallthrough
		case "success":
			complete := true

			for _, v := range this.starts {
				if v.Status != "background" && v.Status != "success" {
					complete = false
					break
				}
			}

			if complete {
				this.start.Status = "success"

				for _, v := range this.starts {
					next := this.stop
					if this.loop != nil {
						next = this.loop
					}

					v.updateEdge(next, 2)
				}
			}
		case "failure":
			this.start.Status = "failure"
			this.start.addEdge(this.stop, 2)
		}
	case "stop":
		node, ok := this.stops[name]
		if !ok {
			return false
		}

		node.Status = status

		switch status {
		case "running", "unstable":
			this.stop.Status = "running"
			this.stop.updateEdge(node, 2)
		}

		complete := true
		finalStatus := "success"

		for _, v := range this.stops {
			switch v.Status {
			case "success":
			case "failure":
				finalStatus = "failure"
			default:
				complete = false
			}

			if !complete {
				break
			}
		}

		if complete {
			this.stop.Status = finalStatus

			for _, v := range this.stops {
				v.updateEdge(this.cleanup, 2)
			}
		}
	case "cleanup":
		node, ok := this.cleanups[name]
		if !ok {
			return false
		}

		node.Status = status

		switch status {
		case "running", "unstable":
			this.cleanup.Status = "running"
			this.cleanup.updateEdge(node, 2)
		}

		complete := true
		finalStatus := "success"

		for _, v := range this.cleanups {
			switch v.Status {
			case "success":
			case "failure":
				finalStatus = "failure"
			default:
				complete = false
			}

			if !complete {
				break
			}
		}

		if complete {
			this.cleanup.Status = finalStatus

			for _, v := range this.cleanups {
				v.updateEdge(this.done, 2)
			}
		}
	default:
		return false
	}

	return true
}

func newPipeline(exp, name string, run, loop int) *pipeline {
	var (
		config  = &node{Name: "configure", Status: "unknown", Exp: exp, Run: run, Loop: loop, idx: 0}
		start   = &node{Name: "start", Status: "unknown", Exp: exp, Run: run, Loop: loop, idx: 1}
		stop    = &node{Name: "stop", Status: "unknown", Exp: exp, Run: run, Loop: loop, idx: 2}
		cleanup = &node{Name: "cleanup", Status: "unknown", Exp: exp, Run: run, Loop: loop, idx: 3}
		done    = &node{Name: "done", Status: "unknown", Exp: exp, Run: run, Loop: loop, idx: 4}
	)

	return &pipeline{
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
		pl.addComponentToStage("configure", pl.start)
	} else {
		for _, cmp := range exe.Configure {
			n := &node{Name: cmp, Status: "unknown"}
			n.addEdge(pl.start, 0)

			pl.addNode("configure", n)
			pl.addComponentToStage("configure", n)
		}
	}

	var next *node

	if exe.Loop == nil {
		next = pl.stop
	} else {
		next = &node{Name: "loop", Status: "unknown"}
		next.addEdge(pl.stop, 0)

		pl.addNode("", next)
		pl.loop = next
	}

	if len(exe.Start) == 0 {
		pl.addComponentToStage("start", next)
	} else {
		for _, cmp := range exe.Start {
			n := &node{Name: cmp, Status: "unknown"}
			n.addEdge(next, 0)

			pl.addNode("start", n)
			pl.addComponentToStage("start", n)
		}
	}

	if len(exe.Stop) == 0 {
		pl.addComponentToStage("stop", pl.cleanup)
	} else {
		for _, cmp := range exe.Stop {
			n := &node{Name: cmp, Status: "unknown"}
			n.addEdge(pl.cleanup, 0)

			pl.addNode("stop", n)
			pl.addComponentToStage("stop", n)
		}
	}

	if len(exe.Cleanup) == 0 {
		pl.addComponentToStage("cleanup", pl.done)
	} else {
		for _, cmp := range exe.Cleanup {
			n := &node{Name: cmp, Status: "unknown"}
			n.addEdge(pl.done, 0)

			pl.addNode("cleanup", n)
			pl.addComponentToStage("cleanup", n)
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
		loopStat := "running"

		if update.Status == "running" {
			// use `unstable` state to represent running break component needing
			// attention, both for the break component node and parent loop nodes
			update.Status = "unstable"
			loopStat = "unstable"
		}

		for i := update.Loop - 1; i >= 0; i-- {
			pl, _ := getPipeline(update.Exp, update.Run, i)
			pl.setStageStatus("loop", loopStat)

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
	pipelines        map[string]map[int]map[int]*pipeline
	pipelineUpdates  chan PipelineUpdate
	pipelineRequests chan pipelineRequest
	pipelineDeletes  chan pipelineDelete
)

func RequestPipeline(exp string, run, loop int) (*pipeline, error) {
	if pipelines == nil {
		return nil, fmt.Errorf("pipelines not initialized")
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
