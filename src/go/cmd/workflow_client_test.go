package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"phenix/api/workflow"
)

func TestWorkflowClientOptions(t *testing.T) {
	fake := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"workflow-dry-run":true,"base-dir.injects":"/phenix/injects"}`)
	})

	opts, err := newWorkflowClient(fake.socket).options(t.Context())
	if err != nil {
		t.Fatalf("options() error = %v", err)
	}

	if dryRun, _ := opts["workflow-dry-run"].(bool); !dryRun {
		t.Errorf("options()[workflow-dry-run] = %v, want true", opts["workflow-dry-run"])
	}

	if dir, _ := opts["base-dir.injects"].(string); dir != "/phenix/injects" {
		t.Errorf("options()[base-dir.injects] = %v, want /phenix/injects", opts["base-dir.injects"])
	}

	reqs := fake.recorded()
	if len(reqs) != 1 || reqs[0].method != http.MethodGet || reqs[0].path != "/api/v1/options" {
		t.Errorf("requests = %+v, want one GET /api/v1/options", reqs)
	}
}

func TestWorkflowClientApply(t *testing.T) {
	tests := []struct {
		name      string
		branch    string
		tags      []string
		dryRun    bool
		pending   []string
		expect    workflow.Action
		wantPath  string
		wantQuery url.Values
	}{
		{
			name:      "dry run sends pending configs unchanged, and no tags and no expect",
			branch:    "foo",
			dryRun:    true,
			pending:   []string{"Topology/foo", "Scenario/foo?from=base&topology=foo"},
			wantPath:  "/api/v1/workflow/apply/foo",
			wantQuery: url.Values{"dryRun": {boolStringTrue}, "pending": {"Topology/foo", "Scenario/foo?from=base&topology=foo"}},
		},
		{
			name:     "apply sends repeated tags and expect",
			branch:   "foo",
			tags:     []string{"method=workflow", "dir=/topologies/my foo", "branch=foo"},
			expect:   workflow.ActionRestart,
			wantPath: "/api/v1/workflow/apply/foo",
			wantQuery: url.Values{
				"tag":    {"method=workflow", "dir=/topologies/my foo", "branch=foo"},
				"expect": {"restart"},
			},
		},
		{
			name:      "branch is path escaped",
			branch:    "a b",
			wantPath:  "/api/v1/workflow/apply/a%20b",
			wantQuery: url.Values{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// A restart plan, as NewPlan makes it, with no reason.
			fake := newFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
				dryRun := r.URL.Query().Get("dryRun") == boolStringTrue

				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"action":"restart","experiment":"foo","reason":"","dryRun":%t}`, dryRun)
			})

			body := []byte("kind: Workflow\n")

			got, err := newWorkflowClient(fake.socket).
				apply(t.Context(), tt.branch, body, "application/x-yaml", tt.tags, tt.dryRun, tt.pending, tt.expect)
			if err != nil {
				t.Fatalf("apply() error = %v", err)
			}

			want := workflow.Result{
				Plan:   workflow.Plan{Action: workflow.ActionRestart, Experiment: "foo", Reason: ""},
				DryRun: tt.dryRun,
			}
			if got != want {
				t.Errorf("apply() = %+v, want %+v", got, want)
			}

			reqs := fake.recorded()
			if len(reqs) != 1 {
				t.Fatalf("got %d requests, want 1", len(reqs))
			}

			req := reqs[0]
			if req.method != http.MethodPost || req.path != tt.wantPath {
				t.Errorf("request = %s %s, want POST %s", req.method, req.path, tt.wantPath)
			}

			if !reflect.DeepEqual(req.query, tt.wantQuery) {
				t.Errorf("query = %v, want %v", req.query, tt.wantQuery)
			}

			if req.contentType != "application/x-yaml" || req.body != string(body) {
				t.Errorf("request content = %q %q, want application/x-yaml %q", req.contentType, req.body, body)
			}
		})
	}
}

// TestWorkflowClientApplyRefusesOtherDryRunAnswers checks that the answer
// to a workflow dry run must say dryRun and hold an action. A server that
// ignores dryRun may have applied the workflow config, and an empty action
// would send the real apply without expect. An answer that says dryRun but
// holds an action the command does not know comes from another phenix
// version, and its error names the action.
func TestWorkflowClientApplyRefusesOtherDryRunAnswers(t *testing.T) {
	const notDryRun = "the phenix server did not dry-run the workflow config; it may have applied it; upgrade the phenix server"

	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "not a dry run", body: `{"action":"restart","experiment":"foo","reason":"","dryRun":false}`, want: notDryRun},
		{
			name: "empty action",
			body: `{"action":"","experiment":"foo","reason":"","dryRun":true}`,
			want: `the phenix server answered the dry run with the unknown action ""; ` +
				"use the same phenix version for the command and the server",
		},
		{
			name: "unknown action",
			body: `{"action":"Restart","experiment":"foo","reason":"","dryRun":true}`,
			want: `the phenix server answered the dry run with the unknown action "Restart"; ` +
				"use the same phenix version for the command and the server",
		},
		{name: "empty object", body: `{}`, want: notDryRun},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tt.body)
			})

			got, err := newWorkflowClient(fake.socket).
				apply(t.Context(), "foo", []byte("kind: Workflow\n"), "application/x-yaml", nil, true, nil, "")

			if !errors.Is(err, errNoWorkflowDryRun) || err.Error() != tt.want {
				t.Errorf("apply() error = %v, want %q", err, tt.want)
			}

			if got != (workflow.Result{}) {
				t.Errorf("apply() = %+v, want the zero result with an error", got)
			}
		})
	}
}

func TestWorkflowClientConfigDryRun(t *testing.T) {
	fake := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"action":"create","kind":"Topology","name":"main-topo","dryRun":true,"pending":"Topology/main-topo"}`)
	})

	body := []byte("kind: Topology\nmetadata:\n  name: ${BRANCH_NAME}-topo\n")

	got, err := newWorkflowClient(fake.socket).configDryRun(t.Context(), "main", body, "application/x-yaml")
	if err != nil {
		t.Fatalf("configDryRun() error = %v", err)
	}

	want := workflow.ConfigResult{
		Action:  workflow.ActionCreate,
		Kind:    "Topology",
		Name:    "main-topo",
		DryRun:  true,
		Pending: "Topology/main-topo",
	}
	if got != want {
		t.Errorf("configDryRun() = %+v, want %+v", got, want)
	}

	reqs := fake.recorded()
	if len(reqs) != 1 {
		t.Fatalf("got %d requests, want 1", len(reqs))
	}

	req := reqs[0]
	if req.method != http.MethodPost || req.path != "/api/v1/workflow/configs/main" {
		t.Errorf("request = %s %s, want POST /api/v1/workflow/configs/main", req.method, req.path)
	}

	if wantQuery := (url.Values{"dryRun": {boolStringTrue}}); !reflect.DeepEqual(req.query, wantQuery) {
		t.Errorf("query = %v, want %v", req.query, wantQuery)
	}

	if req.contentType != "application/x-yaml" || req.body != string(body) {
		t.Errorf("request content = %q %q, want the file as read, application/x-yaml %q", req.contentType, req.body, body)
	}
}

func TestWorkflowClientConfigDryRunRefusesOtherAnswers(t *testing.T) {
	dryRunBody := `{"action":"create","kind":"Topology","name":"foo","dryRun":true}`

	tests := []struct {
		name     string
		status   int
		body     string
		answered string
	}{
		{name: "an older server stores the config", status: http.StatusCreated, answered: "201 Created"},
		{name: "202 with a dry-run body", status: http.StatusAccepted, body: dryRunBody, answered: "202 Accepted"},
		{
			name:     "not a dry run",
			status:   http.StatusOK,
			body:     `{"action":"create","kind":"Topology","name":"foo","dryRun":false}`,
			answered: "200 OK",
		},
		{name: "not JSON", status: http.StatusOK, body: "stored", answered: "200 OK"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			})

			_, err := newWorkflowClient(fake.socket).configDryRun(t.Context(), "foo", []byte("kind: Topology\n"), "application/x-yaml")

			want := "the phenix server did not dry-run the config: it answered " + tt.answered +
				"; it may have stored it; upgrade the phenix server"
			if !errors.Is(err, errNoConfigDryRun) || err.Error() != want {
				t.Errorf("configDryRun() error = %v, want %q", err, want)
			}
		})
	}
}

func TestWorkflowClientUpsertConfig(t *testing.T) {
	fake := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})

	body := []byte(`{"kind":"Topology"}`)

	if err := newWorkflowClient(fake.socket).upsertConfig(t.Context(), "main", body, "application/json"); err != nil {
		t.Fatalf("upsertConfig() error = %v", err)
	}

	reqs := fake.recorded()
	if len(reqs) != 1 {
		t.Fatalf("got %d requests, want 1", len(reqs))
	}

	req := reqs[0]
	if req.method != http.MethodPost || req.path != "/api/v1/workflow/configs/main" || len(req.query) != 0 {
		t.Errorf("request = %s %s?%v, want POST /api/v1/workflow/configs/main", req.method, req.path, req.query)
	}

	if req.contentType != "application/json" || req.body != string(body) {
		t.Errorf("request content = %q %q, want application/json %q", req.contentType, req.body, body)
	}
}

func TestWorkflowClientErrors(t *testing.T) {
	explained := `nodes[1] "ADServer" (line 20): property "image" is missing (at hardware.drives[0].image)`
	hint := `  hint: "image:" is on line 19 under nodes[1].hardware, but the schema expects it at nodes[1].hardware.drives[0].image`
	raw := `validating config: config validation failed: Error at "/nodes/1/hardware/drives/0/image": property "image" is missing`

	envelope, err := json.Marshal(map[string]any{
		"cause":    raw,
		"message":  explained,
		"metadata": map[string]string{"validation": explained + "\n" + hint, "validation-raw": raw},
	})
	if err != nil {
		t.Fatalf("encoding envelope: %v", err)
	}

	tests := []struct {
		name    string
		status  int
		body    string
		want    workflowAPIError
		wantMsg string
	}{
		{
			name:   "explained validation rejection",
			status: http.StatusBadRequest,
			body:   string(envelope),
			want: workflowAPIError{
				Status:     http.StatusBadRequest,
				Message:    explained,
				Cause:      raw,
				Validation: explained + "\n" + hint,
			},
			wantMsg: "phenix server returned 400 Bad Request: " + explained,
		},
		{
			name:   "conflict",
			status: http.StatusConflict,
			body:   `{"cause":"workflow conflict: 2 experiments mapped","message":"unable to plan phenix workflow"}`,
			want: workflowAPIError{
				Status:  http.StatusConflict,
				Message: "unable to plan phenix workflow",
				Cause:   "workflow conflict: 2 experiments mapped",
			},
			wantMsg: "phenix server returned 409 Conflict: unable to plan phenix workflow: workflow conflict: 2 experiments mapped",
		},
		{
			name:    "internal error with an empty body",
			status:  http.StatusInternalServerError,
			want:    workflowAPIError{Status: http.StatusInternalServerError},
			wantMsg: "phenix server returned 500 Internal Server Error",
		},
		{
			name:    "plain text body",
			status:  http.StatusNotFound,
			body:    "404 page not found\n",
			want:    workflowAPIError{Status: http.StatusNotFound, Message: "404 page not found"},
			wantMsg: "phenix server returned 404 Not Found: 404 page not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			})

			client := newWorkflowClient(fake.socket)

			_, applyErr := client.apply(t.Context(), "foo", nil, "application/x-yaml", nil, false, nil, "")
			_, dryRunErr := client.configDryRun(t.Context(), "foo", nil, "application/x-yaml")
			upsertErr := client.upsertConfig(t.Context(), "foo", nil, "application/x-yaml")
			_, optionsErr := client.options(t.Context())

			calls := map[string]error{"apply": applyErr, "configDryRun": dryRunErr, "upsertConfig": upsertErr, "options": optionsErr}

			for call, err := range calls {
				var apiErr *workflowAPIError
				if !errors.As(err, &apiErr) {
					t.Errorf("%s() error = %v, want a *workflowAPIError", call, err)

					continue
				}

				if *apiErr != tt.want {
					t.Errorf("%s() error = %+v, want %+v", call, *apiErr, tt.want)
				}

				if got := apiErr.Error(); got != tt.wantMsg {
					t.Errorf("%s() error message = %q, want %q", call, got, tt.wantMsg)
				}

				if errors.Is(err, errServerUnreachable) {
					t.Errorf("%s() error = %v, an answer must not count as unreachable", call, err)
				}
			}
		})
	}
}

//nolint:gocyclo,cyclop // eight independent transport-error subtests, each a short, self-contained check
func TestWorkflowClientTransportErrors(t *testing.T) {
	t.Run("nothing listening", func(t *testing.T) {
		_, err := newWorkflowClient(filepath.Join(t.TempDir(), "none.sock")).options(t.Context())

		var apiErr *workflowAPIError
		if !errors.Is(err, errServerUnreachable) || errors.As(err, &apiErr) {
			t.Fatalf("options() error = %v, want errServerUnreachable", err)
		}
	})

	t.Run("permission denied on the socket", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root ignores socket permissions")
		}

		fake := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

		// phenix ui usually runs as root, so its socket is root's and 0755:
		// other users may not write to it, and connect fails with EACCES.
		if err := os.Chmod(fake.socket, 0o555); err != nil {
			t.Fatalf("making %s read-only: %v", fake.socket, err)
		}

		_, err := newWorkflowClient(fake.socket).options(t.Context())

		want := "dial unix " + fake.socket + ": connect: permission denied; " + socketPermissionHint
		if !errors.Is(err, errServerUnreachable) || !errors.Is(err, fs.ErrPermission) || !strings.HasSuffix(err.Error(), want) {
			t.Fatalf("options() error = %v, want errServerUnreachable ending in %q", err, want)
		}

		if reqs := fake.recorded(); len(reqs) != 0 {
			t.Errorf("got %d requests, want none", len(reqs))
		}
	})

	t.Run("connection dropped", func(t *testing.T) {
		fake := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request) { dropConnection(t, w) })

		err := newWorkflowClient(fake.socket).upsertConfig(t.Context(), "foo", []byte("kind: Topology\n"), "application/x-yaml")
		if !errors.Is(err, errServerUnreachable) {
			t.Fatalf("upsertConfig() error = %v, want errServerUnreachable", err)
		}

		// net/http does not retry a POST, so the server saw it once.
		if reqs := fake.recorded(); len(reqs) != 1 {
			t.Errorf("got %d requests, want 1", len(reqs))
		}
	})

	t.Run("canceled context", func(t *testing.T) {
		fake := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusCreated) })

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		err := newWorkflowClient(fake.socket).upsertConfig(ctx, "foo", nil, "application/x-yaml")

		var urlErr *url.Error
		if !errors.As(err, &urlErr) || !errors.Is(err, context.Canceled) || errors.Is(err, errServerUnreachable) {
			t.Fatalf("upsertConfig() error = %v, want a *url.Error wrapping context.Canceled, not errServerUnreachable", err)
		}

		if reqs := fake.recorded(); len(reqs) != 0 {
			t.Errorf("got %d requests, want none", len(reqs))
		}
	})

	t.Run("context canceled while the server waits", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(cancel)

		// The server holds the request until the client goes away.
		fake := newFakeServer(t, func(_ http.ResponseWriter, r *http.Request) {
			cancel()

			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
		})

		err := newWorkflowClient(fake.socket).upsertConfig(ctx, "foo", []byte("kind: Topology\n"), "application/x-yaml")

		var urlErr *url.Error
		if !errors.As(err, &urlErr) || !errors.Is(err, context.Canceled) || errors.Is(err, errServerUnreachable) {
			t.Fatalf("upsertConfig() error = %v, want a *url.Error wrapping context.Canceled, not errServerUnreachable", err)
		}

		if reqs := fake.recorded(); len(reqs) != 1 {
			t.Errorf("got %d requests, want 1", len(reqs))
		}
	})

	t.Run("response is not JSON", func(t *testing.T) {
		fake := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "not json") })
		client := newWorkflowClient(fake.socket)

		if _, err := client.options(t.Context()); err == nil || !strings.Contains(err.Error(), "decoding phenix server options") {
			t.Errorf("options() error = %v, want a decoding error", err)
		}

		_, err := client.apply(t.Context(), "foo", nil, "application/x-yaml", nil, true, nil, "")
		if err == nil || !strings.Contains(err.Error(), "decoding apply result") {
			t.Errorf("apply() error = %v, want a decoding error", err)
		}
	})

	t.Run("truncated response", func(t *testing.T) {
		fake := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", "100")
			_, _ = io.WriteString(w, "short")
		})

		_, err := newWorkflowClient(fake.socket).options(t.Context())
		if !errors.Is(err, errServerUnreachable) || !strings.Contains(err.Error(), "reading the response") {
			t.Fatalf("options() error = %v, want errServerUnreachable while reading the response", err)
		}
	})

	t.Run("invalid base URL", func(t *testing.T) {
		client := &workflowClient{http: http.DefaultClient, base: "http://[::1"}

		if _, err := client.options(t.Context()); err == nil || !strings.Contains(err.Error(), "building options request") {
			t.Errorf("options() error = %v, want a request error", err)
		}

		err := client.upsertConfig(t.Context(), "foo", nil, "application/x-yaml")
		if err == nil || !strings.Contains(err.Error(), "building request") {
			t.Errorf("upsertConfig() error = %v, want a request error", err)
		}
	})
}

// TestWorkflowClientPreflightTimeout checks that the options request and
// the dry runs, which the phenix server answers at once, get no answer
// within the preflight bound and fail as an unreachable server, while the
// config upsert and the real apply, which may take long, are not bounded.
func TestWorkflowClientPreflightTimeout(t *testing.T) {
	lowerPreflightTimeout(t, 200*time.Millisecond)

	// The server holds every request for twice the bound, then answers.
	fake := newFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(400 * time.Millisecond):
		}

		w.Header().Set("Content-Type", "application/json")

		if strings.HasPrefix(r.URL.Path, "/api/v1/workflow/configs/") {
			w.WriteHeader(http.StatusCreated)

			return
		}

		_, _ = io.WriteString(w, `{"action":"update","experiment":"foo","reason":"","dryRun":false}`)
	})

	client := newWorkflowClient(fake.socket)
	body := []byte("kind: Workflow\n")

	bounded := map[string]func() error{
		"options": func() error {
			_, err := client.options(t.Context())

			return err
		},
		"configDryRun": func() error {
			_, err := client.configDryRun(t.Context(), "foo", body, "application/x-yaml")

			return err
		},
		"apply dry run": func() error {
			_, err := client.apply(t.Context(), "foo", body, "application/x-yaml", nil, true, nil, "")

			return err
		},
	}

	for call, send := range bounded {
		err := send()

		want := "phenix server unreachable: no answer within 200ms"
		if !errors.Is(err, errServerUnreachable) || err.Error() != want {
			t.Errorf("%s error = %v, want %q", call, err, want)
		}
	}

	if err := client.upsertConfig(t.Context(), "foo", body, "application/x-yaml"); err != nil {
		t.Errorf("upsertConfig() error = %v, want no time bound", err)
	}

	if _, err := client.apply(t.Context(), "foo", body, "application/x-yaml", nil, false, nil, workflow.ActionUpdate); err != nil {
		t.Errorf("apply() error = %v, want no time bound", err)
	}
}

// TestPreflightTimeout pins the bound of a preflight request and how an
// unanswered one is described.
func TestPreflightTimeout(t *testing.T) {
	if got := noAnswerError(preflightTimeout).Error(); got != "phenix server unreachable: no answer within 1m0s" {
		t.Errorf("noAnswerError(preflightTimeout) = %q, want the one-minute bound", got)
	}

	if preflightRequestTimeout != preflightTimeout {
		t.Errorf("preflightRequestTimeout = %v, want preflightTimeout", preflightRequestTimeout)
	}
}

// cancelAfterRoundTrip wraps a RoundTripper and cancels once it returns a
// response, the moment the caller has the status and headers but has not yet
// read the body. That pins the exact instant TestWorkflowClientInterruptedWhileReading
// needs: earlier, and do's own [http.Client.Do] would fail instead; later,
// and there would be nothing left to interrupt.
type cancelAfterRoundTrip struct {
	rt     http.RoundTripper
	cancel context.CancelFunc
}

// RoundTrip implements [http.RoundTripper].
func (c cancelAfterRoundTrip) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := c.rt.RoundTrip(req)
	if err == nil {
		c.cancel()
	}

	return resp, err
}

// TestWorkflowClientInterruptedWhileReading pins the case where the response
// headers already arrived, and the request's context ends while do reads the
// body: that is an interrupt, not an unreachable server, even though the read
// fails the same way a dropped connection would.
func TestWorkflowClientInterruptedWhileReading(t *testing.T) {
	release := make(chan struct{})

	// The handler answers 200 but promises more bytes than it sends, flushes
	// what it has, then waits: once do's http.Client.Do returns, the body is
	// only partially arrived, and the caller is left blocked reading the
	// rest, exactly as it would be against a slow or stalled server.
	fake := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "partial")

		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("the response writer cannot flush")

			return
		}

		flusher.Flush()

		<-release
	})

	// Release the handler before the fake server's own Close, so Close
	// cannot hang waiting for a connection the handler is still holding.
	t.Cleanup(func() { close(release) })

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	// base supplies the unix-socket transport; wrapping it lets the test
	// cancel at the exact moment RoundTrip hands back the response, rather
	// than racing a goroutine against the client's own read of the socket.
	base := newWorkflowClient(fake.socket)
	client := &workflowClient{
		http: &http.Client{Transport: cancelAfterRoundTrip{rt: base.http.Transport, cancel: cancel}},
		base: base.base,
	}

	_, err := client.options(ctx)

	var apiErr *workflowAPIError
	if errors.As(err, &apiErr) {
		t.Fatalf("options() error = %v, want no *workflowAPIError", err)
	}

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("options() error = %v, want context.Canceled", err)
	}

	if errors.Is(err, errServerUnreachable) {
		t.Fatalf("options() error = %v, an interrupt must not count as unreachable", err)
	}

	want := "contacting phenix server: "
	if !strings.HasPrefix(err.Error(), want) {
		t.Fatalf("options() error = %v, want a message starting with %q", err, want)
	}
}

func TestWorkflowClientLogsRequests(t *testing.T) {
	logs := captureLogs(t)
	fake := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})

	err := newWorkflowClient(fake.socket).upsertConfig(t.Context(), "foo", []byte("kind: Topology\n"), "application/x-yaml")
	if err != nil {
		t.Fatalf("upsertConfig() error = %v", err)
	}

	checkFields(t, logs.first(t, "phenix server request"), map[string]string{
		"level":  "DEBUG",
		"type":   "SYSTEM",
		"method": http.MethodPost,
		"url":    "http://unix/api/v1/workflow/configs/foo",
		"bytes":  "15",
	})
	checkFields(t, logs.first(t, "phenix server response"), map[string]string{
		"level":  "DEBUG",
		"method": http.MethodPost,
		"status": "201",
		"body":   "",
	})
}
