//nolint:testpackage // inspect PTY lifetime and run-scoped cache snapshots
package scorch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/mux"
)

func TestTerminalNaturalExitAndConcurrentKill(t *testing.T) {
	done, err := CreateWebTerminal(
		context.Background(),
		"natural",
		"example",
		0,
		0,
		"start",
		"break",
		t.TempDir(),
		"/bin/sh",
		[]string{"-c", "echo output"},
	)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("natural exit did not continue breakpoint")
	}
	if _, err := GetTerminalByExperiment("example|0|0|start|break"); err == nil {
		t.Fatal("finished PTY retained")
	}
	done, err = CreateWebTerminal(
		context.Background(),
		"kill",
		"example",
		1,
		0,
		"start",
		"break",
		t.TempDir(),
		"/bin/sh",
		[]string{"-c", "sleep 30"},
	)
	if err != nil {
		t.Fatal(err)
	}
	term, err := GetTerminalByExperiment("example|1|0|start|break")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := KillTerminal(term); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	select {
	case <-done:
	default:
		t.Fatal("kill returned before reaping child")
	}
}

func TestPipelineSnapshotsAndDeletionStayWithinRun(t *testing.T) {
	Start("/")
	first, second := newPipeline("example", "setup", 0, 0), newPipeline("example", "work", 1, 0)
	pipelines["example"] = map[int]map[int]*pipeline{0: {0: first}, 1: {0: second}}
	snapshot, err := RequestPipeline("example", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Pipeline[0].Status = "changed"
	again, err := RequestPipeline("example", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if again.Pipeline[0].Status == "changed" {
		t.Fatal("snapshot exposes mutable nodes")
	}
	DeletePipeline("example", 1, -1, false)
	again, err = RequestPipeline("example", 0, 0)
	if err != nil || again.Name != "setup" {
		t.Fatal("deleting work erased setup", err)
	}
}

func TestScorchEndpointsRequireExperimentPermission(t *testing.T) {
	for _, handler := range []func(http.ResponseWriter, *http.Request){GetTerminals, ConnectTerminal, StreamTerminal, StreamComponentOutput, ExitTerminal} {
		request := mux.SetURLVars(httptest.NewRequest(http.MethodGet, "/", nil), map[string]string{"name": "private"})
		response := httptest.NewRecorder()
		handler(response, request)
		if response.Code != http.StatusForbidden {
			t.Fatalf("unauthorized endpoint returned %d", response.Code)
		}
	}
}
