package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestAddAnnotation(t *testing.T) {
	// Input JSON (minimal valid structure)
	input := []byte(`{
		"spec": {
			"experimentName": "test-exp"
		},
		"metadata": {
			"existing": "value"
		}
	}`)

	output, err := addAnnotation(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}

	// Verify annotation was added
	meta := result["metadata"].(map[string]any)
	annotations := meta["annotations"].(map[string]any)

	if annotations["example-go-processed"] != "true" {
		t.Errorf(
			"expected annotation 'example-go-processed' to be 'true', got %v",
			annotations["example-go-processed"],
		)
	}

	// Verify existing data was preserved
	if meta["existing"] != "value" {
		t.Errorf("expected existing metadata to be preserved")
	}
}

func TestAddAnnotation_MissingMetadata(t *testing.T) {
	input := []byte(`{
		"spec": {
			"experimentName": "test-exp"
		}
	}`)

	output, err := addAnnotation(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}

	meta, ok := result["metadata"].(map[string]any)
	if !ok {
		t.Fatal("expected metadata to be created")
	}

	annotations, ok := meta["annotations"].(map[string]any)
	if !ok {
		t.Fatal("expected annotations to be created")
	}

	if annotations["example-go-processed"] != "true" {
		t.Errorf(
			"expected annotation 'example-go-processed' to be 'true', got %v",
			annotations["example-go-processed"],
		)
	}
}

func TestPanicRecovery(t *testing.T) {
	if os.Getenv("TEST_PANIC_RECOVERY") == "1" {
		os.Args = []string{"app", "panic"} //nolint:reassign // test pattern
		main()
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestPanicRecovery")
	cmd.Env = append(os.Environ(), "TEST_PANIC_RECOVERY=1")
	cmd.Stdin = strings.NewReader(
		`{"apiVersion": "phenix.sandia.gov/v1", "kind": "Experiment", "metadata": {"name": "test"}, "spec": {"experimentName": "test"}}`,
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	err := cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && !exitErr.Success() {
		// Expected exit code 1 (from os.Exit(1) in recovery)
	} else {
		t.Fatalf("process ran with err %v, want exit status 1", err)
	}

	output := stderr.String()
	if !strings.Contains(output, `"msg":"application panicked"`) {
		t.Errorf("expected log message 'application panicked', got: %s", output)
	}
	if !strings.Contains(output, `"traceback":`) {
		t.Errorf("expected traceback field in log, got: %s", output)
	}
}

func TestLogLevelDebug(t *testing.T) {
	if os.Getenv("TEST_LOG_LEVEL_DEBUG") == "1" {
		os.Args = []string{"app", "running"} //nolint:reassign // test pattern
		main()
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestLogLevelDebug")
	cmd.Env = append(os.Environ(), "TEST_LOG_LEVEL_DEBUG=1", "PHENIX_LOG_LEVEL=DEBUG")
	cmd.Stdin = strings.NewReader(
		`{"apiVersion": "phenix.sandia.gov/v1", "kind": "Experiment", "metadata": {"name": "test"}, "spec": {"experimentName": "test"}}`,
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		t.Fatalf("process ran with err %v", err)
	}

	output := stderr.String()
	if !strings.Contains(output, `"msg":"detailed debug info"`) {
		t.Errorf("expected log message 'detailed debug info', got: %s", output)
	}
	if !strings.Contains(output, `"level":"DEBUG"`) {
		t.Errorf("expected level DEBUG, got: %s", output)
	}
}

func TestLogJSONFormat(t *testing.T) {
	if os.Getenv("TEST_LOG_JSON") == "1" {
		os.Args = []string{"app", "running"} //nolint:reassign // test pattern
		main()
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestLogJSONFormat")
	cmd.Env = append(os.Environ(), "TEST_LOG_JSON=1")
	cmd.Stdin = strings.NewReader(
		`{"apiVersion": "phenix.sandia.gov/v1", "kind": "Experiment", "metadata": {"name": "test"}, "spec": {"experimentName": "test"}}`,
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("process ran with err %v", err)
	}

	scanner := bufio.NewScanner(&stderr)
	for scanner.Scan() {
		line := scanner.Bytes()
		var entry map[string]any
		if err := json.Unmarshal(line, &entry); err != nil {
			t.Errorf("log line is not valid JSON: %s", line)
		}

		if _, ok := entry["level"]; !ok {
			t.Errorf("log line missing required 'level' field: %s", line)
		}
	}
}

func TestLogFile(t *testing.T) {
	if os.Getenv("TEST_LOG_FILE") == "1" {
		os.Args = []string{"app", "running"} //nolint:reassign // test pattern
		main()
		return
	}

	tmpFile, err := os.CreateTemp(t.TempDir(), "phenix-test-log")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	cmd := exec.Command(os.Args[0], "-test.run=TestLogFile")
	cmd.Env = append(os.Environ(), "TEST_LOG_FILE=1", "PHENIX_LOG_FILE="+tmpFile.Name())
	cmd.Stdin = strings.NewReader(
		`{"apiVersion": "phenix.sandia.gov/v1", "kind": "Experiment", "metadata": {"name": "test"}, "spec": {"experimentName": "test"}}`,
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("process ran with err %v", err)
	}

	content, err := os.ReadFile(tmpFile.Name())
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(content), `"msg":"running application logic"`) {
		t.Errorf("expected log message in file, got: %s", string(content))
	}
}
