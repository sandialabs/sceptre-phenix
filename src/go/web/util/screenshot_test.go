package util

import (
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"pgregory.net/rapid"

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
			t.Errorf("GetScreenshot(exp, vm, %q) = %v, want nil", size, err)
		}

		if !valid && !errors.Is(err, ErrInvalidScreenshotSize) {
			t.Errorf("GetScreenshot(exp, vm, %q) = %v, want ErrInvalidScreenshotSize", size, err)
		}
	}
}

func TestScreenshotWidth(t *testing.T) {
	t.Parallel()

	for size, want := range map[string]string{
		"1":    "200",
		"100":  "200",
		"200":  "200",
		"201":  "400",
		"215":  "400",
		"500":  "600",
		"800":  "800",
		"0200": "200",
		"4001": "4096",
		"4096": "4096",
	} {
		if got, err := ScreenshotWidth(size); err != nil || got != want {
			t.Errorf("ScreenshotWidth(%q) = %q, %v; want %q, nil", size, got, err, want)
		}
	}

	for _, size := range []string{"", "0", "4097", "12.5", "200 file /etc/x"} {
		if got, err := ScreenshotWidth(size); !errors.Is(err, ErrInvalidScreenshotSize) {
			t.Errorf("ScreenshotWidth(%q) = %q, %v; want ErrInvalidScreenshotSize", size, got, err)
		}
	}
}

// Every valid size is rounded up by less than one step, to a width that rounds
// to itself.
func TestScreenshotWidthRoundsUp(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, maxScreenshotSize).Draw(t, "size")

		got, err := ScreenshotWidth(strconv.Itoa(n))
		if err != nil {
			t.Fatalf("ScreenshotWidth(%d) error = %v", n, err)
		}

		width, _ := strconv.Atoi(got)

		if width < n || width-n >= screenshotStep || (width%screenshotStep != 0 && width != maxScreenshotSize) {
			t.Fatalf("ScreenshotWidth(%d) = %d, want the next multiple of %d or %d", n, width, screenshotStep, maxScreenshotSize)
		}

		if again, _ := ScreenshotWidth(got); again != got {
			t.Fatalf("ScreenshotWidth(%s) = %s, want it unchanged", got, again)
		}
	})
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
		{name: "reuses it at a size of the same width", vm: "a", size: "150", want: "1", calls: 1},
		{name: "takes another at another size", vm: "a", size: "400", want: "2", calls: 2},
		{name: "reuses that one at a size it is rounded up from", vm: "a", size: "250", want: "2", calls: 2},
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
				t.Errorf("%s: GetScreenshot(exp, %s, %s) = %q, %v; want an error", step.name, step.vm, step.size, shot, err)
			}
		case err != nil || string(shot) != step.want:
			t.Errorf("%s: GetScreenshot(exp, %s, %s) = %q, %v; want %q", step.name, step.vm, step.size, shot, err, step.want)
		}

		if n := fake.calls.Load(); n != step.calls {
			t.Errorf("%s: after GetScreenshot(exp, %s, %s), minimega was asked for %d screenshots, want %d",
				step.name, step.vm, step.size, n, step.calls)
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
			t.Errorf("concurrent caller %d: GetScreenshot(exp, vm, 200) = %q, %v; want png", i, shots[i], errs[i])
		}
	}

	if n := fake.calls.Load(); n != 1 {
		t.Errorf("%d concurrent GetScreenshot(exp, vm, 200) calls asked minimega for %d screenshots, want 1", callers, n)
	}
}
