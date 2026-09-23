//nolint:testpackage // testing internal experiment worker coordination
package web

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestCancelAndWaitExperimentWorkers(t *testing.T) {
	const expName = "worker-wait-test"

	ctx, cancel := context.WithCancel(context.Background())
	canceled := make(chan struct{})
	release := make(chan struct{})
	returned := make(chan bool)

	var startWG sync.WaitGroup
	startWG.Add(1)

	go func() {
		defer startWG.Done()

		<-ctx.Done()
		close(canceled)
		<-release
	}()

	commonMu.Lock()
	cancelers[expName] = []context.CancelFunc{cancel}
	startWaiters[expName] = &startWG
	commonMu.Unlock()

	t.Cleanup(func() {
		commonMu.Lock()
		delete(cancelers, expName)
		delete(waiters, expName)
		delete(startWaiters, expName)
		commonMu.Unlock()
	})

	go func() {
		returned <- cancelAndWaitExperimentWorkers(expName)
	}()

	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("worker context was not canceled")
	}

	select {
	case <-returned:
		t.Fatal("returned before startup worker completed")
	default:
	}

	close(release)

	select {
	case hadStartWorker := <-returned:
		if !hadStartWorker {
			t.Fatal("did not report tracked startup worker")
		}
	case <-time.After(time.Second):
		t.Fatal("did not return after startup worker completed")
	}
}
