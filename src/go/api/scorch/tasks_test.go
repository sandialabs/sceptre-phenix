//nolint:testpackage // verify execution-owned background task internals
package scorch

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestTaskGroupsJoinOnlyTheirOwnTasks(t *testing.T) {
	first, second := newTaskGroup(), newTaskGroup()
	exited := make(chan struct{})
	if err := first.start(
		context.Background(),
		"0/start/capture",
		func(ctx context.Context) error { <-ctx.Done(); close(exited); return ctx.Err() },
	); err != nil {
		t.Fatal(err)
	}
	if err := second.start(
		context.Background(),
		"0/start/capture",
		func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() },
	); err != nil {
		t.Fatal(err)
	}
	if err := first.stopPrefix("0/start/"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-exited:
	default:
		t.Fatal("cleanup raced background process")
	}
	second.mu.Lock()
	count := len(second.tasks)
	second.mu.Unlock()
	if count != 1 {
		t.Fatal("other execution task removed")
	}
	if err := second.close(); err != nil {
		t.Fatal(err)
	}
}

func TestBackgroundFailureNotifiesExecutionAndIsRetained(t *testing.T) {
	group := newTaskGroup()
	failure := errors.New("capture failed")
	reported := make(chan error, 1)
	group.failed = func(err error) { reported <- err }
	if err := group.start(context.Background(), "0/start/capture", func(context.Context) error { return failure }); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-reported:
		if !errors.Is(err, failure) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("background failure was not surfaced")
	}
	if err := group.close(); !errors.Is(err, failure) {
		t.Fatalf("background error lost: %v", err)
	}
}

func TestComponentsAreFreshInstances(t *testing.T) {
	first, second := GetComponent("pause"), GetComponent("pause")
	if first == second {
		t.Fatal("mutable component instance reused")
	}
	if err := first.Init(Name("setup")); err != nil {
		t.Fatal(err)
	}
	if err := second.Init(Name("work")); err != nil {
		t.Fatal(err)
	}
	if first.(*Pause).options.Name != "setup" {
		t.Fatal("other component initialization leaked")
	}
}
