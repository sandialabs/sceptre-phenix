package util

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"phenix/util/mm"
	"phenix/web/cache"
)

// screenshotMM is a minimega that answers each screenshot request, numbered
// from 1, with shot.
type screenshotMM struct {
	mm.MM

	calls atomic.Int32
	shot  func(call int32) ([]byte, error)
}

func (m *screenshotMM) GetVMScreenshot(...mm.Option) ([]byte, error) {
	return m.shot(m.calls.Add(1))
}

// fakeScreenshots answers screenshot requests with shot, from an empty cache.
func fakeScreenshots(t *testing.T, shot func(call int32) ([]byte, error)) *screenshotMM {
	t.Helper()

	fake := &screenshotMM{shot: shot}

	originalMM, originalCache := mm.DefaultMM, cache.DefaultWebCache

	t.Cleanup(func() {
		mm.DefaultMM = originalMM             //nolint:reassign // restore test double
		cache.DefaultWebCache = originalCache //nolint:reassign // restore test double
	})

	mm.DefaultMM = fake                           //nolint:reassign // install test double
	cache.DefaultWebCache = cache.NewGoWebCache() //nolint:reassign // install an empty cache

	return fake
}

//nolint:paralleltest // replaces minimega and the web cache
func TestGetScreenshotSizes(t *testing.T) {
	fakeScreenshots(t, func(int32) ([]byte, error) { return []byte("png"), nil })

	for size, valid := range map[string]bool{
		"1":               true,
		"200":             true,
		"4096":            true,
		"":                false,
		"0":               false,
		"-5":              false,
		"4097":            false,
		"12.5":            false,
		"1e3":             false,
		"200 extra":       false,
		"200 file /etc/x": false,
	} {
		if got := ValidScreenshotSize(size); got != valid {
			t.Errorf("ValidScreenshotSize(%q) = %v, want %v", size, got, valid)
		}

		_, err := GetScreenshot("exp", "vm", size)
		if valid && err != nil {
			t.Errorf("GetScreenshot at size %q: %v", size, err)
		}

		if !valid && !errors.Is(err, ErrInvalidScreenshotSize) {
			t.Errorf("GetScreenshot at size %q: err = %v, want ErrInvalidScreenshotSize", size, err)
		}
	}
}

// A screenshot is reused at the size it was taken at, and a failed one is not.
//
//nolint:paralleltest // replaces minimega and the web cache
func TestGetScreenshotCaches(t *testing.T) {
	var (
		failure error // what minimega answers instead of a screenshot
		missing bool  // minimega answers with no screenshot
	)

	fake := fakeScreenshots(t, func(call int32) ([]byte, error) {
		if failure != nil || missing {
			return nil, failure
		}

		return []byte{byte('0' + call)}, nil
	})

	steps := []struct {
		name, vm, size string
		failure        error
		missing        bool
		want           string
		calls          int32
	}{
		{name: "takes a screenshot", vm: "a", size: "200", want: "1", calls: 1},
		{name: "reuses it", vm: "a", size: "200", want: "1", calls: 1},
		{name: "takes another at another size", vm: "a", size: "400", want: "2", calls: 2},
		{name: "takes another of another VM", vm: "b", size: "200", want: "3", calls: 3},
		{name: "reports a minimega error", vm: "c", size: "200", failure: mm.ErrVMNotFound, calls: 4},
		{name: "reports a missing screenshot", vm: "c", size: "200", missing: true, calls: 5},
		{name: "asks again after a failure", vm: "c", size: "200", want: "6", calls: 6},
	}

	for _, step := range steps {
		failure, missing = step.failure, step.missing

		shot, err := GetScreenshot("exp", step.vm, step.size)

		switch {
		case step.failure != nil || step.missing:
			if err == nil || (step.failure != nil && !errors.Is(err, step.failure)) {
				t.Errorf("%s: GetScreenshot() = %q, %v; want the error", step.name, shot, err)
			}
		case err != nil || string(shot) != step.want:
			t.Errorf("%s: GetScreenshot() = %q, %v; want %q", step.name, shot, err, step.want)
		}

		if n := fake.calls.Load(); n != step.calls {
			t.Errorf("%s: minimega was asked for %d screenshots, want %d", step.name, n, step.calls)
		}
	}
}

// Concurrent requests for a screenshot share one minimega command.
//
//nolint:paralleltest // replaces minimega and the web cache
func TestGetScreenshotSharesARequest(t *testing.T) {
	const callers = 5

	var (
		started = make(chan struct{})
		release = make(chan struct{})
		arrived sync.WaitGroup
		done    sync.WaitGroup
		shots   [callers][]byte
		errs    [callers]error
	)

	fake := fakeScreenshots(t, func(call int32) ([]byte, error) {
		if call == 1 {
			close(started)
			<-release
		}

		return []byte("png"), nil
	})

	arrived.Add(callers)
	done.Add(callers)

	for i := range callers {
		go func() {
			defer done.Done()

			arrived.Done()

			shots[i], errs[i] = GetScreenshot("exp", "vm", "200")
		}()
	}

	// hold the first minimega command open until every caller has started; one
	// that asks after it finishes reads the cached screenshot instead
	arrived.Wait()
	<-started
	close(release)
	done.Wait()

	for i := range callers {
		if errs[i] != nil || string(shots[i]) != "png" {
			t.Errorf("caller %d: GetScreenshot() = %q, %v; want png", i, shots[i], errs[i])
		}
	}

	if n := fake.calls.Load(); n != 1 {
		t.Errorf("minimega was asked for %d screenshots, want 1", n)
	}
}
