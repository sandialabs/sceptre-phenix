package app_test

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"pgregory.net/rapid"

	"phenix/app"
	"phenix/util/pubsub"
)

// A periodic app run is announced once, as an error when it failed and as a
// success only when it did not, so a failure is never followed by a success.
func TestRunResultIsPublishedOnce(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		var runErr error
		if rapid.Bool().Draw(t, "failed") {
			runErr = errors.New(rapid.StringN(1, 20, -1).Draw(t, "err"))
		}

		ch := pubsub.Subscribe("trigger-app")
		defer pubsub.Unsubscribe("trigger-app", ch)

		done := make(chan struct{})

		go func() {
			defer close(done)

			app.PublishRunResult("exp", "periodic", runErr)
		}()

		got, _ := (<-ch).(app.TriggerPublication)

		select {
		case <-done:
		case extra := <-ch:
			t.Fatalf("published a second time: %+v", extra)
		}

		want := app.TriggerPublication{Experiment: "exp", App: "periodic", State: "success"}
		if runErr != nil {
			want.State, want.Error = "error", runErr
		}

		if diff := cmp.Diff(want, got, cmpopts.EquateErrors()); diff != "" {
			t.Fatalf("publication (-want +got):\n%s", diff)
		}
	})
}
