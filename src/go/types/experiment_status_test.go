package types

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

	"phenix/store"
	"phenix/store/storetest"
	ifaces "phenix/types/interfaces"
)

// experimentAppStatus returns the status of app that exp holds, nil when it
// holds none.
func experimentAppStatus(exp *Experiment, app string) map[string]any {
	var status map[string]any

	_ = exp.Status.ParseAppStatus(app, &status)

	return status
}

// experimentStored returns experiment exp as the store holds it.
func experimentStored(t *testing.T) *Experiment {
	t.Helper()

	exp := NewExperiment(store.ConfigMetadata{Name: "exp"})
	if err := exp.Reload(); err != nil {
		t.Fatalf("Reload() of exp error = %v", err)
	}

	return exp
}

// UpdateAppStatus changes the stored status of one app and keeps the status
// that other writers stored after the caller read its copy.
func TestExperimentUpdateAppStatusKeepsStoredStatus(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	storetest.UseBolt(t)
	experimentCreate(t, experimentStoredConfig("exp", "2024-01-01T00:00:00Z"))

	stale := experimentStored(t)

	other := experimentStored(t)
	other.Status.SetAppStatus("soh", map[string]any{"initialized": true})

	if err := other.WriteToStore(true); err != nil {
		t.Fatalf("WriteToStore(true) error = %v", err)
	}

	err := stale.UpdateAppStatus("scorch", func(status map[string]any) {
		status["runID"] = 3
	})
	if err != nil {
		t.Fatalf("UpdateAppStatus(scorch) error = %v", err)
	}

	stored := experimentStored(t)

	for _, exp := range []struct {
		name string
		exp  *Experiment
	}{{"stored", stored}, {"in memory", stale}} {
		if diff := cmp.Diff(map[string]any{"initialized": true}, experimentAppStatus(exp.exp, "soh")); diff != "" {
			t.Errorf("%s soh status after UpdateAppStatus(scorch) mismatch (-want +got):\n%s", exp.name, diff)
		}

		if got := fmt.Sprint(experimentAppStatus(exp.exp, "scorch")["runID"]); got != "3" {
			t.Errorf("%s scorch runID after UpdateAppStatus(scorch) = %s, want 3", exp.name, got)
		}

		if got := exp.exp.Status.StartTime(); got != "2024-01-01T00:00:00Z" {
			t.Errorf("%s start time after UpdateAppStatus(scorch) = %q, want the stored one", exp.name, got)
		}
	}
}

// UpdateStatus changes nothing, in memory or in the store, when it cannot read,
// decode or write the stored experiment.
func TestExperimentUpdateStatusErrors(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	errUpdate := errors.New("update failed")

	for _, test := range []struct {
		name      string
		exp       string
		getErr    error
		version   string
		updateErr error
		wantErr   error
		want      string
	}{
		{name: "invalid name", exp: "a/b", want: "getting experiment:"},
		{name: "not stored", exp: "exp", getErr: store.ErrNotExist, wantErr: store.ErrNotExist, want: "getting experiment exp from store"},
		{name: "unknown version", exp: "exp", version: "phenix.sandia.gov/v9", want: "decoding experiment exp"},
		{name: "update fails", exp: "exp", updateErr: errUpdate, wantErr: errUpdate, want: "saving experiment config"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			st := store.NewMockStore(ctrl)
			storetest.Use(t, st)

			st.EXPECT().Get(gomock.Any()).DoAndReturn(func(c *store.Config) error {
				*c = *experimentStoredConfig("exp", "")
				if test.version != "" {
					c.Version = test.version
				}

				return test.getErr
			}).MaxTimes(1)

			st.EXPECT().Update(gomock.Any()).Return(test.updateErr).MaxTimes(1)

			exp := NewExperiment(store.ConfigMetadata{Name: test.exp})
			exp.Status.SetStartTime("in memory")
			status := exp.Status

			var applied bool

			err := exp.UpdateStatus(func(s ifaces.ExperimentStatus) {
				applied = true

				s.SetStartTime("updated")
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Errorf("UpdateStatus() error = %v, want one containing %q", err, test.want)
			}

			if test.wantErr != nil && !errors.Is(err, test.wantErr) {
				t.Errorf("UpdateStatus() error = %v, want it to wrap %v", err, test.wantErr)
			}

			if exp.Status != status || exp.Status.StartTime() != "in memory" {
				t.Errorf("UpdateStatus() that failed changed the status in memory to start time %q", exp.Status.StartTime())
			}

			if wantApplied := test.updateErr != nil; applied != wantApplied {
				t.Errorf("UpdateStatus() applied its change = %t, want %t", applied, wantApplied)
			}
		})
	}
}

// lockCheckStore is a store that records each call made without statusMu held.
type lockCheckStore struct {
	store.Store

	mu       sync.Mutex
	unlocked []string
}

func (s *lockCheckStore) check(call string) {
	if statusMu.TryLock() {
		statusMu.Unlock()

		s.mu.Lock()
		s.unlocked = append(s.unlocked, call)
		s.mu.Unlock()
	}
}

func (s *lockCheckStore) Get(c *store.Config) error {
	s.check("Get")

	return s.Store.Get(c) //nolint:wrapcheck // test double
}

func (s *lockCheckStore) Update(c *store.Config) error {
	s.check("Update")

	return s.Store.Update(c) //nolint:wrapcheck // test double
}

// WriteToStore and UpdateStatus read and write the store with statusMu held,
// so one cannot write between the read and the write of the other.
func TestExperimentStatusWritesHoldTheLock(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	st := &lockCheckStore{Store: storetest.NewBolt(t)}
	storetest.Use(t, st)
	experimentCreate(t, experimentStoredConfig("exp", ""))

	exp := NewExperiment(store.ConfigMetadata{Name: "exp"})

	if err := exp.WriteToStore(true); err != nil {
		t.Fatalf("WriteToStore(true) error = %v", err)
	}

	if err := exp.UpdateStatus(func(ifaces.ExperimentStatus) {}); err != nil {
		t.Fatalf("UpdateStatus() error = %v", err)
	}

	if len(st.unlocked) > 0 {
		t.Errorf("store calls without statusMu held = %q, want none", st.unlocked)
	}
}

// Writers that each change their own app's status from their own stale copy,
// at the same time, keep the changes of all the others.
func TestExperimentUpdateAppStatusConcurrentWriters(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	storetest.UseBolt(t)
	experimentCreate(t, experimentStoredConfig("exp", ""))

	const writers = 8

	var wg sync.WaitGroup

	errs := make([]error, writers)

	for i := range writers {
		stale := experimentStored(t)

		wg.Add(1)

		go func() {
			defer wg.Done()

			errs[i] = stale.UpdateAppStatus(fmt.Sprintf("app%d", i), func(status map[string]any) {
				status["writer"] = i
			})
		}()
	}

	wg.Wait()

	stored := experimentStored(t)

	for i := range writers {
		app := fmt.Sprintf("app%d", i)

		if errs[i] != nil {
			t.Errorf("UpdateAppStatus(%s) error = %v", app, errs[i])
		}

		if got := fmt.Sprint(experimentAppStatus(stored, app)["writer"]); got != strconv.Itoa(i) {
			t.Errorf("stored %s writer = %s, want %d", app, got, i)
		}
	}
}

// Any order of app status updates, each from a copy read before all of them,
// stores the last value given for each key of each app, and leaves the other
// keys of each app as stored.
func TestExperimentUpdateAppStatusProperties(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	storetest.UseBolt(t)

	var n int

	rapid.Check(t, func(t *rapid.T) {
		n++
		name := fmt.Sprintf("exp%d", n)

		c := experimentStoredConfig(name, "")
		c.Status = map[string]any{"apps": map[string]any{"a": map[string]any{"kept": "yes"}}}

		if err := store.Create(c); err != nil {
			t.Fatalf("store.Create(%s) error = %v", name, err)
		}

		type update struct {
			App, Key string
			Value    int
		}

		updates := rapid.SliceOfN(rapid.Custom(func(t *rapid.T) update {
			return update{
				App:   rapid.SampledFrom([]string{"a", "b", "c"}).Draw(t, "app"),
				Key:   rapid.SampledFrom([]string{"x", "y"}).Draw(t, "key"),
				Value: rapid.IntRange(0, 9).Draw(t, "value"),
			}
		}), 1, 8).Draw(t, "updates")

		copies := make([]*Experiment, len(updates))

		for i := range updates {
			copies[i] = NewExperiment(store.ConfigMetadata{Name: name})
			if err := copies[i].Reload(); err != nil {
				t.Fatalf("Reload() of %s error = %v", name, err)
			}
		}

		want := map[string]map[string]string{"a": {"kept": "yes"}}

		for i, u := range updates {
			err := copies[i].UpdateAppStatus(u.App, func(status map[string]any) { status[u.Key] = u.Value })
			if err != nil {
				t.Fatalf("UpdateAppStatus(%s) error = %v", u.App, err)
			}

			if want[u.App] == nil {
				want[u.App] = map[string]string{}
			}

			want[u.App][u.Key] = strconv.Itoa(u.Value)
		}

		stored := NewExperiment(store.ConfigMetadata{Name: name})
		if err := stored.Reload(); err != nil {
			t.Fatalf("Reload() of %s error = %v", name, err)
		}

		got := map[string]map[string]string{}

		for app := range want {
			got[app] = map[string]string{}
			for k, v := range experimentAppStatus(stored, app) {
				got[app][k] = fmt.Sprint(v)
			}
		}

		if diff := cmp.Diff(want, got); diff != "" {
			t.Fatalf("stored app status after %v mismatch (-want +got):\n%s", updates, diff)
		}
	})
}
