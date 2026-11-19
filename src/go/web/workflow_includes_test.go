package web

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"
	gocmp "github.com/google/go-cmp/cmp"

	"phenix/store"
	"phenix/web/middleware"
	"phenix/web/weberror"
)

// includesStore answers the reads of the store with stored, by config name,
// and lists exps; every other read is [store.ErrNotExist].
func includesStore(t *testing.T, stored map[string]store.Config, exps ...store.Config) {
	t.Helper()

	m := installValidationTestStore(t)
	m.EXPECT().List("Experiment").Return(store.Configs(exps), nil).AnyTimes()
	m.EXPECT().Get(gomock.Any()).DoAndReturn(func(c *store.Config) error {
		if cfg, ok := stored[c.Kind+"/"+c.Metadata.Name]; ok && c.Kind == "Topology" {
			*c = cfg

			return nil
		}

		return store.ErrNotExist
	}).AnyTimes()
}

// includesTopology returns the stored topology named name that includes each
// of includes.
func includesTopology(name string, includes ...string) store.Config {
	cfg := workflowTestTopology()
	cfg.Metadata.Name = name
	cfg.Spec["nodes"] = []any{map[string]any{
		"type": "VirtualMachine", "general": map[string]any{"hostname": name},
		"hardware": map[string]any{"os_type": "linux", "drives": []any{map[string]any{"image": "x.qc2"}}},
	}}

	if len(includes) > 0 {
		list := make([]any, len(includes))
		for i, include := range includes {
			list[i] = include
		}

		cfg.Spec["includeTopologies"] = list
	}

	return cfg
}

// includesFile writes a topology file with the given name that includes each
// of includes and has one VM with hostname, and returns its path.
func includesFile(t *testing.T, dir, name, hostname string, includes ...string) string {
	t.Helper()

	text := "apiVersion: phenix.sandia.gov/v1\nkind: Topology\nmetadata:\n  name: " + name + "\nspec:\n"
	if len(includes) > 0 {
		text += "  includeTopologies:\n"
		var textSb71 strings.Builder
		for _, include := range includes {
			textSb71.WriteString("  - " + include + "\n")
		}
		text += textSb71.String()
	}

	text += "  nodes:\n  - type: VirtualMachine\n    general:\n      hostname: \"" + hostname + "\"\n" +
		"    hardware:\n      os_type: linux\n      drives:\n      - image: x.qc2\n"

	path := filepath.Join(dir, name+".yml")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}

	return path
}

const (
	includesCreate = "  auto:\n    create: foo\n    restart: false\n  topology: topo\n"
	includesNoAuto = "  topology: topo\n"
	includesNoUpd  = "  auto:\n    update: false\n  topology: topo\n"
	includesNoRst  = "  auto:\n    restart: false\n  topology: topo\n"
)

// TestApplyWorkflowDryRunReportsIncludedVariables checks that an apply dry run
// reports each request variable that an included topology file uses, at any
// depth of include, for a pending or a stored topology and for every plan, and
// that it does not fail where the real apply would not.
func TestApplyWorkflowDryRunReportsIncludedVariables(t *testing.T) {
	dir := t.TempDir()
	leaf := includesFile(t, dir, "leaf", "${SITE}-fw")
	mid := includesFile(t, dir, "mid", "mid", leaf)
	garbage := filepath.Join(dir, "garbage.yml")

	if err := os.WriteFile(garbage, []byte("not: [a topology"), 0o600); err != nil {
		t.Fatal(err)
	}

	self := filepath.Join(dir, "self.yml")
	if err := os.WriteFile(self, []byte("apiVersion: phenix.sandia.gov/v1\nkind: Topology\nmetadata:\n  name: self\n"+
		"spec:\n  includeTopologies:\n  - "+self+"\n  - "+leaf+"\n  nodes: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var (
		missing   = filepath.Join(dir, "missing.yml")
		vars      = map[string]string{"SITE": "lab", "EXP": "foo", "UNUSED": "x"}
		running   = workflowTestExperiment("exp", "main", true)
		mapped    = workflowTestExperiment("exp", "main", false)
		storedMid = map[string]store.Config{
			"Topology/topo": includesTopology("topo", "inc"),
			"Topology/inc":  includesTopology("inc", mid),
		}
	)

	tests := []struct {
		name     string
		spec     string
		stored   map[string]store.Config
		exps     []store.Config
		pending  []string
		vars     map[string]string
		wantCode int
		wantVars []any
		wantMsg  string
	}{
		{
			name: "pending topology, nested file include", spec: includesCreate,
			pending:  []string{"Topology/topo?include=" + mid},
			wantVars: []any{"SITE"},
		},
		{
			name: "pending topology includes a pending topology that includes a file", spec: includesCreate,
			pending:  []string{"Topology/topo?include=inc", "Topology/inc?include=" + mid},
			wantVars: []any{"SITE"},
		},
		{
			name: "pending topology includes a stored topology that includes a file", spec: includesCreate,
			stored:   map[string]store.Config{"Topology/inc": includesTopology("inc", mid)},
			pending:  []string{"Topology/topo?include=inc"},
			wantVars: []any{"SITE"},
		},
		{
			name: "stored topology, nested stored and file includes", spec: includesCreate,
			stored: storedMid, wantVars: []any{"SITE"},
		},
		{
			name: "update of a stored topology", spec: includesNoRst,
			stored: storedMid, exps: []store.Config{mapped}, wantVars: []any{"SITE"},
		},
		{
			name: "restart of a pending topology", spec: "  topology: topo\n",
			pending: []string{"Topology/topo?include=" + mid}, exps: []store.Config{running}, wantVars: []any{"SITE"},
		},
		{
			name: "none: no experiment mapped and auto.create not set", spec: includesNoAuto,
			stored: storedMid, wantVars: []any{"SITE"},
		},
		{
			name: "none: no experiment mapped, pending topology", spec: includesNoAuto,
			pending: []string{"Topology/topo?include=inc", "Topology/inc?include=" + mid}, wantVars: []any{"SITE"},
		},
		{
			name: "none: auto.update is false", spec: includesNoUpd,
			stored: storedMid, exps: []store.Config{mapped}, wantVars: []any{"SITE"},
		},
		{
			name: "none: running and auto.restart is false", spec: includesNoRst,
			stored: storedMid, exps: []store.Config{running}, wantVars: []any{"SITE"},
		},
		{
			name: "none: topology from the experiment annotation", spec: "  auto:\n    update: false\n",
			stored: storedMid, exps: []store.Config{withTopologyAnnotation(mapped, "topo")}, wantVars: []any{"SITE"},
		},
		{
			name: "include cycle", spec: includesNoAuto,
			pending: []string{"Topology/topo?include=" + self}, wantVars: []any{"SITE"},
		},
		{
			name: "line break in a value used only by a nested file", spec: includesCreate,
			pending: []string{"Topology/topo?include=" + mid}, vars: map[string]string{"SITE": "a\nb"},
			wantCode: http.StatusBadRequest, wantMsg: "unable to prepare phenix workflow config",
		},
		{
			name: "line break in a value used only by a nested file, none plan", spec: includesNoAuto,
			stored: storedMid, vars: map[string]string{"SITE": "a\nb"},
			wantCode: http.StatusBadRequest, wantMsg: "unable to prepare phenix workflow config",
		},
		{
			name: "missing include of a none plan", spec: includesNoAuto,
			pending: []string{"Topology/topo?include=" + missing},
		},
		{
			name: "undecodable include of a none plan", spec: includesNoAuto,
			pending: []string{"Topology/topo?include=" + garbage + "&include=" + leaf}, wantVars: []any{"SITE"},
		},
		{
			name: "missing topology of a none plan", spec: includesNoAuto,
		},
		{
			name: "stored topology that does not decode, none plan", spec: includesNoAuto,
			stored: map[string]store.Config{"Topology/topo": {Version: "phenix.sandia.gov/v1", Kind: "Topology",
				Metadata: store.ConfigMetadata{Name: "topo"}, Spec: map[string]any{"nodes": "no"}}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			includesStore(t, tt.stored, tt.exps...)

			v := vars
			if tt.vars != nil {
				v = tt.vars
			}

			query := url.Values{"dryRun": {"true"}, "pending": tt.pending}.Encode()

			body, typ := varsTestRequest(t, mimeYAML, workflowTestYAML(tt.spec), v)
			rec := serveApplyWorkflow(t, applyWorkflowRequest(workflowTestRole(true), query, typ, bytes.NewReader(body)))

			if tt.wantCode != 0 {
				assertWorkflowError(t, rec, tt.wantCode, tt.wantMsg, "variable holds a line break")

				return
			}

			got := decodeVarsBody(t, rec)
			var want any
			if tt.wantVars != nil {
				want = tt.wantVars
			}

			if diff := gocmp.Diff(want, got["variables"]); diff != "" {
				t.Errorf("ApplyWorkflow(%s) variables mismatch (-want +got):\n%s", query, diff)
			}
		})
	}
}

// withTopologyAnnotation returns exp with the topology annotation set.
func withTopologyAnnotation(exp store.Config, topology string) store.Config {
	annotations := store.Annotations{"topology": topology}
	maps.Copy(annotations, exp.Metadata.Annotations)

	exp.Metadata.Annotations = annotations

	return exp
}

// TestApplyWorkflowDryRunWarnsOnce checks that the warning of an included file
// that the dry run reads two times is in the response and in the log once, and
// counts each of its other lines once.
func TestApplyWorkflowDryRunWarnsOnce(t *testing.T) {
	dir := t.TempDir()
	leaf := includesFile(t, dir, "leaf", "leaf")
	mid := includesFile(t, dir, "mid", "${NOWHERE}", leaf)

	text, err := os.ReadFile(mid)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(mid, append(text, "# ${NOWHERE}\n"...), 0o600); err != nil {
		t.Fatal(err)
	}

	want := []any{mid + " line 11 (and 1 other line): no variable NOWHERE in the request or in the server environment, " +
		"and the placeholder has no default; phenix used an empty string"}

	for name, stored := range map[string]map[string]store.Config{
		"pending topology": nil,
		"stored topology":  {"Topology/topo": includesTopology("topo", mid)},
	} {
		t.Run(name, func(t *testing.T) {
			includesStore(t, stored)

			warnings := captureWarnings(t)

			var pending []string
			if stored == nil {
				pending = []string{"Topology/topo?include=" + mid}
			}

			query := url.Values{"dryRun": {"true"}, "pending": pending}.Encode()
			body, typ := varsTestRequest(t, mimeYAML, workflowTestYAML(includesCreate), map[string]string{})
			rec := serveApplyWorkflow(t, applyWorkflowRequest(workflowTestRole(true), query, typ, bytes.NewReader(body)))

			got := decodeVarsBody(t, rec)

			if diff := gocmp.Diff(want, got["warnings"]); diff != "" {
				t.Errorf("ApplyWorkflow() warnings mismatch (-want +got):\n%s", diff)
			}

			if n := len(variableWarnings(warnings())); n != 1 {
				t.Errorf("logged %d variable warnings, want 1", n)
			}
		})
	}
}

// TestApplyWorkflowDryRunWarnsInReadOrder checks that the direct includes of a
// pending topology are read with the request variables, before the includes
// of those files: the warnings come in that order.
func TestApplyWorkflowDryRunWarnsInReadOrder(t *testing.T) {
	includesStore(t, nil)

	var (
		dir = t.TempDir()
		n   = includesFile(t, dir, "n", "n${NOWHERE_N}")
		a   = includesFile(t, dir, "a", "a${NOWHERE_A}", n)
		b   = includesFile(t, dir, "b", "b${NOWHERE_B}")
	)

	empty := func(file string, line int, name string) string {
		return fmt.Sprintf("%s line %d: no variable %s in the request or in the server environment, "+
			"and the placeholder has no default; phenix used an empty string", file, line, name)
	}

	query := url.Values{"dryRun": {"true"}, "pending": {"Topology/topo?include=" + a + "&include=" + b}}.Encode()
	body, typ := varsTestRequest(t, mimeYAML, workflowTestYAML(includesCreate), map[string]string{})
	rec := serveApplyWorkflow(t, applyWorkflowRequest(workflowTestRole(true), query, typ, bytes.NewReader(body)))

	want := []any{empty(a, 11, "NOWHERE_A"), empty(b, 9, "NOWHERE_B"), empty(n, 9, "NOWHERE_N")}
	if diff := gocmp.Diff(want, decodeVarsBody(t, rec)["warnings"]); diff != "" {
		t.Errorf("ApplyWorkflow() warnings mismatch (-want +got):\n%s", diff)
	}
}

// TestApplyWorkflowPlainDryRunReadsNoMoreIncludes checks that a plain request
// does not walk the includes of a none plan.
func TestApplyWorkflowPlainDryRunReadsNoMoreIncludes(t *testing.T) {
	m := installValidationTestStore(t)
	m.EXPECT().List("Experiment").Return(store.Configs(nil), nil)

	rec := serveApplyWorkflow(t, applyWorkflowRequest(
		workflowTestRole(true), "dryRun=true&pending=Topology/topo?include=/nonexistent.yml", mimeYAML,
		strings.NewReader(workflowTestYAML(includesNoAuto)),
	))

	decodeVarsBody(t, rec)
}

// TestWorkflowUpsertConfigVariablesRefused checks that a configs request in
// the multipart format that the role may not write names the variables it used
// in its refusal audit record, and logs its warnings once, with the config name.
func TestWorkflowUpsertConfigVariablesRefused(t *testing.T) {
	m := installValidationTestStore(t)
	expectUpsertExists(m, workflowTestKey("topology/lab-topo"), false)

	var (
		events   = captureAudit(t)
		warnings = captureWarnings(t)
		config   = varsTopology("${SITE}-topo", `["${U}"]`)
	)

	body, typ := varsTestRequest(t, mimeYAML, config, map[string]string{"SITE": "lab", "UNUSED": "x"})
	req := varsUpsertRequest(t, "?dryRun=true", body, typ)
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyRole, configsRoleOn([]string{"scenario/*"}, "create")))

	err := WorkflowUpsertConfig(httptest.NewRecorder(), req)

	var werr *weberror.WebError
	if !errors.As(err, &werr) || werr.Status != http.StatusForbidden {
		t.Fatalf("WorkflowUpsertConfig() error = %v, want a 403", err)
	}

	wantAudit(t, events(), "config-create", "failure", map[string]any{
		"phenix.workflow.branch": "main", "phenix.workflow.variables": "SITE",
	})

	records := variableWarnings(warnings())
	if len(records) != 1 || records[0].Variable != "U" || records[0].File != "Topology/lab-topo" {
		t.Errorf("warning records = %v, want one for U in Topology/lab-topo", records)
	}
}
