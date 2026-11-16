package diskchain

import (
	"context"
	"runtime"
)

// MaxWorkers is how many images WalkAll reads at once, at most.
const MaxWorkers = 8

// WalkAll walks each of paths (see Walk), or only opens it when openOnly says so (see Exists), a few at a time, until
// ctx is done. A path missing from the result was not read in time; a worker still reading it exits once its read
// returns.
func WalkAll(ctx context.Context, paths []string, openOnly map[string]bool) map[string]Result {
	type walked struct {
		path string
		res  Result
	}

	queue := make(chan string, len(paths))
	for _, path := range paths {
		queue <- path
	}

	close(queue)

	// buffered, so a worker never waits on a collector that gave up
	out := make(chan walked, len(paths))

	for range min(runtime.NumCPU(), MaxWorkers, len(paths)) {
		go func() {
			for path := range queue {
				if ctx.Err() != nil {
					return
				}

				walk := Walk
				if openOnly[path] {
					walk = Exists
				}

				res := walk(ctx, path)

				if ctx.Err() != nil {
					return
				}

				out <- walked{path: path, res: res}
			}
		}()
	}

	results := make(map[string]Result, len(paths))

	for range paths {
		select {
		case w := <-out:
			results[w.path] = w.res
		case <-ctx.Done():
			return results
		}
	}

	return results
}
