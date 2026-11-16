package broker

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"phenix/api/job"
	"phenix/util/plog"
	bt "phenix/web/broker/brokertypes"
)

// wsSchemas loads the /ws message schemas from openapi.yml.
func wsSchemas(t *testing.T) (*openapi3.Schema, *openapi3.Schema) {
	t.Helper()

	const path = "../public/docs/openapi.yml"

	doc, err := openapi3.NewLoader().LoadFromFile(path)
	if err != nil {
		t.Fatalf("loading %s: %v", path, err)
	}

	schema := func(name string) *openapi3.Schema {
		ref := doc.Components.Schemas[name]
		if ref == nil || ref.Value == nil {
			t.Fatalf("%s has no %s schema", path, name)
		}

		return ref.Value
	}

	return schema("WebSocketMessage"), schema("WebSocketRequest")
}

// undeclared lists the keys of value, and of the objects nested in it that
// schema describes, that schema does not declare.
func undeclared(schema *openapi3.Schema, value any, at string) []string {
	object, ok := value.(map[string]any)
	if !ok {
		return nil
	}

	var keys []string

	for key, v := range object {
		prop := schema.Properties[key]
		if prop == nil {
			if len(schema.AllOf) == 0 {
				keys = append(keys, at+key)
			}

			continue
		}

		sub := prop.Value
		if len(sub.AllOf) == 1 {
			sub = sub.AllOf[0].Value
		}

		// a free-form object, such as a message's result, declares nothing
		if len(sub.Properties) > 0 {
			keys = append(keys, undeclared(sub, v, at+key+".")...)
		}
	}

	slices.Sort(keys)

	return keys
}

// checkAgainst fails the test unless msg, as JSON, matches schema and holds
// only the properties it declares.
func checkAgainst(t *testing.T, schema *openapi3.Schema, what string, msg []byte) {
	t.Helper()

	var value any
	if err := json.Unmarshal(msg, &value); err != nil {
		t.Fatalf("decoding %s %s: %v", what, msg, err)
	}

	if keys := undeclared(schema, value, ""); len(keys) > 0 {
		t.Errorf("%s %s has properties its schema lacks: %q", what, msg, keys)
	}

	// a schema of no type allows null, which kin-openapi refuses without nullable
	if object, ok := value.(map[string]any); ok {
		for key, v := range object {
			if prop := schema.Properties[key]; v == nil && prop != nil && prop.Value.Type == "" {
				delete(object, key)
			}
		}
	}

	if err := schema.VisitJSON(value); err != nil {
		t.Errorf("%s %s does not match its schema: %v", what, msg, err)
	}
}

// Every kind of message the broker sends on /ws matches WebSocketMessage, and
// every request a client sends matches WebSocketRequest.
func TestMessagesMatchOpenAPI(t *testing.T) {
	message, request := wsSchemas(t)

	jobBody, err := json.Marshal(job.Job{ID: "j1", Kind: "vms.action", Owner: "alice"})
	if err != nil {
		t.Fatalf("json.Marshal(job) error = %v", err)
	}

	logEntry, err := json.Marshal(plog.LogEntry{
		Time: 1, Timestamp: "2026-01-01 00:00:00.000", Level: "INFO", Type: string(plog.TypeSystem), Message: "started",
	})
	if err != nil {
		t.Fatalf("json.Marshal(log entry) error = %v", err)
	}

	broadcasts := map[string]bt.Publish{
		"experiment": {
			RequestPolicy: bt.NewRequestPolicy("experiments", "get", "exp"),
			Resource:      bt.NewResource("experiment", "exp", "start"),
			Result:        json.RawMessage(`{"name":"exp","running":true}`),
			Seq:           0,
		},
		"VM without a result": {
			RequestPolicy: bt.NewRequestPolicy("vms", "get", "exp/web"),
			Resource:      bt.NewResource("experiment/vm", "exp/web", "stopping"),
			Result:        nil,
			Seq:           0,
		},
		"screenshot": {
			RequestPolicy: bt.NewRequestPolicy("vms", "get", "exp/web"),
			Resource:      bt.NewResource("experiment/vm/screenshot", "exp/web", "update"),
			Result:        json.RawMessage(`{"screenshot":"data:image/png;base64,AA=="}`),
			Seq:           0,
		},
		"job": {
			RequestPolicy: bt.NewRequestPolicy("jobs", "list", "exp"),
			Resource:      bt.NewResource("job", "j1", "update"),
			Result:        jobBody,
			Seq:           0,
		},
		"log": logPublish(logEntry),
		"app error": {
			Resource: bt.NewResource("apps/scorch", "exp", "error"),
			Result:   errorResult(errors.New("failed")),
		},
		"stamped seq": {
			Resource: bt.NewResource("config", "topology/t", "update"),
			Result:   json.RawMessage(`{}`),
			Seq:      42,
		},
	}

	for name, pub := range broadcasts {
		t.Run(name, func(t *testing.T) {
			out, ok := encode(pub)
			if !ok {
				t.Fatalf("encode(%s) failed", name)
			}

			data, err := encodeMessage(out)
			if err != nil {
				t.Fatalf("encodeMessage(%s) error = %v", name, err)
			}

			checkAgainst(t, message, "broadcast "+name, data)

			var seq struct {
				Seq uint64 `json:"seq"`
			}

			if err := json.Unmarshal(data, &seq); err != nil || seq.Seq == 0 {
				t.Errorf("broadcast %s %s has no seq, want one", name, data)
			}
		})
	}

	// a reply to a client's request, sent as it is rather than pre-encoded
	reply, err := encodeMessage(bt.Publish{
		Resource: bt.NewResource("experiment/vms", "exp", "list"),
		Result:   json.RawMessage(`{"vms":[],"total":0}`),
		Seq:      7,
	})
	if err != nil {
		t.Fatalf("encodeMessage(reply) error = %v", err)
	}

	checkAgainst(t, message, "reply", reply)

	requests := map[string]bt.Request{
		"subscribe": {Resource: bt.NewResource("log", "", "subscribe"), Payload: nil},
		"list": {
			Resource: bt.NewResource("experiment/vms", "exp", "list"),
			Payload:  json.RawMessage(`{"filter":"web","page_number":1,"page_size":10}`),
		},
		"search": {
			Resource: bt.NewResource("experiment/topology", "exp", "search"),
			Payload:  json.RawMessage(`{"term":"hostname","value":"web"}`),
		},
	}

	for name, req := range requests {
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("json.Marshal(%s request) error = %v", name, err)
		}

		checkAgainst(t, request, "request "+name, data)
	}
}
