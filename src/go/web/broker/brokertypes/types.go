package brokertypes

import (
	"encoding/json"
)

type RequestPolicy struct {
	Resource     string
	Verb         string
	ResourceName string
}

func NewRequestPolicy(r, v, rn string) *RequestPolicy {
	return &RequestPolicy{Resource: r, Verb: v, ResourceName: rn}
}

type Resource struct {
	Type   string `json:"type"`
	Name   string `json:"name"`
	Action string `json:"action"`
}

func NewResource(t, n, a string) *Resource {
	return &Resource{Type: t, Name: n, Action: a}
}

type Publish struct {
	RequestPolicy *RequestPolicy  `json:"-"`
	Resource      *Resource       `json:"resource"`
	Result        json.RawMessage `json:"result"`
	// Seq orders the message against every other message and REST response
	// of this phenix process (see freshness.Seq). The broker stamps it as the
	// message is published, after the change it tells of; a reply to a client
	// request is stamped before the data it carries is read.
	Seq uint64 `json:"seq,omitempty"`
}

type Request struct {
	Resource *Resource       `json:"resource"`
	Payload  json.RawMessage `json:"request"`
}

/*
Request:

{
	"resource": {
		"type": "experiment/vms",
		"name": "<exp name>",
		"action": "list"
	},
	"request": {
		"sort_column": "name",
		"sort_asc": true,
		"page_number": 1,
		"page_size": 5
	}
}

Response:

{
	"resource": {
		"type": "experiment/vms",
		"name": "<exp name>",
		"action": "list"
	},
	"result": {
		"vms": [
			...
		]
	},
	"seq": 1760450000123456
}

Screenshot Updates:

{
	"resource": {
		"type": "experiment/vm/screenshot",
		"name": "<exp name>/<vm name>",
		"action": "update"
	},
	"result": {
		"screenshot": "data:image/png;base64,..."
	},
	"seq": 1760450000123457
}
*/
