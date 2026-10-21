package store

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestBoltProcessMutation(t *testing.T) {
	if path := os.Getenv("PHENIX_TEST_BOLT_HELPER"); path != "" {
		s := NewBoltDB()
		if err := s.Init(Endpoint(path)); err != nil {
			t.Fatal(err)
		}
		for range 20 {
			cfg, _ := NewConfig("experiment/process")
			if err := s.Mutate(cfg, func(current *Config) error {
				current.Status["counter"] = current.Status["counter"].(float64) + 1
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
		return
	}
	path := "bolt://" + filepath.Join(t.TempDir(), "process.db")
	s := NewBoltDB()
	if err := s.Init(Endpoint(path)); err != nil {
		t.Fatal(err)
	}
	cfg, _ := NewConfig("experiment/process")
	cfg.Status = map[string]any{"counter": float64(0)}
	if err := s.Create(cfg); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBoltProcessMutation$")
	child.Env = append(os.Environ(), "PHENIX_TEST_BOLT_HELPER="+path)
	var output bytes.Buffer
	child.Stdout = &output
	child.Stderr = &output
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	for range 20 {
		cfg, _ := NewConfig("experiment/process")
		if err := s.Mutate(cfg, func(current *Config) error {
			current.Status["counter"] = current.Status["counter"].(float64) + 1
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := child.Wait(); err != nil {
		t.Fatal(err, output.String())
	}
	if err := s.Get(cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Status["counter"] != float64(40) {
		t.Fatalf("cross-process updates lost: %v", cfg.Status)
	}
}

func TestBoltAtomicMutationAndStaleStatus(t *testing.T) {
	testAtomicStore(t, NewBoltDB(), NewBoltDB(), "bolt://"+filepath.Join(t.TempDir(), "store.db"))
}

func TestEtcdAtomicMutationAndStaleStatus(t *testing.T) {
	endpoint := os.Getenv("PHENIX_TEST_ETCD")
	if endpoint == "" {
		t.Skip("set PHENIX_TEST_ETCD to an isolated test server")
	}
	testAtomicStore(t, NewEtcd(), NewEtcd(), endpoint)
}

func testAtomicStore(t *testing.T, first, second Store, path string) {
	t.Helper()
	for _, s := range []Store{first, second} {
		if err := s.Init(Endpoint(path)); err != nil {
			t.Fatal(err)
		}
	}
	defer func() {
		for _, s := range []Store{first, second} {
			if _, ok := s.(*Etcd); ok {
				_ = s.Close()
			}
		}
	}()
	name := "experiment/test-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	c, err := NewConfig(name)
	if err != nil {
		t.Fatal(err)
	}
	c.Status = map[string]any{"counter": float64(0), "apps": map[string]any{"other": "keep"}}
	if err := first.Create(c); err != nil {
		t.Fatal(err)
	}
	stale := *c
	var wg sync.WaitGroup
	failures := make(chan error, 40)
	for i := range 40 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := first
			if i%2 != 0 {
				s = second
			}
			cfg, _ := NewConfig(name)
			failures <- s.Mutate(cfg, func(current *Config) error {
				current.Status["counter"] = current.Status["counter"].(float64) + 1
				return nil
			})
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := first.Get(c); err != nil {
		t.Fatal(err)
	}
	if c.Status["counter"] != float64(40) {
		t.Fatalf("lost concurrent updates: %v", c.Status)
	}
	rejection := errors.New("reject")
	if err := first.Mutate(
		c,
		func(current *Config) error { current.Status["counter"] = float64(-1); return rejection },
	); !errors.Is(
		err,
		rejection,
	) {
		t.Fatal(err)
	}
	if err := first.Get(c); err != nil {
		t.Fatal(err)
	}
	if c.Status["counter"] != float64(40) {
		t.Fatal("rejected mutation was committed")
	}
	if err := first.Mutate(c, func(current *Config) error {
		current.Status["apps"].(map[string]any)["scorch"] = map[string]any{
			"executions": map[string]any{"0": map[string]any{"executionID": "owner"}},
		}
		current.Status["appRunningStageStatus"] = map[string]any{"scorch": true}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	stale.Status = map[string]any{"apps": map[string]any{"other": "updated"}, "appRunningStageStatus": map[string]any{"scorch": false}}
	if err := second.Update(&stale); err != nil {
		t.Fatal(err)
	}
	if err := first.Get(c); err != nil {
		t.Fatal(err)
	}
	if c.Status["apps"].(map[string]any)["scorch"] == nil || c.Status["appRunningStageStatus"].(map[string]any)["scorch"] != true {
		t.Fatalf("stale writer erased managed state: %v", c.Status)
	}
	if c.Status["apps"].(map[string]any)["other"] != "updated" {
		t.Fatal("unrelated app update was lost")
	}
}
