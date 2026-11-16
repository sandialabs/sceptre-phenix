package experiment

import (
	"context"
	"time"

	"phenix/util/plog"
)

// deleteStepsMessage is the message of the log record that gives the time of each step of a delete.
const deleteStepsMessage = "experiment deleted"

// deleteTimes holds how long the file steps of a delete took. hookDelete fills it in when the context of the delete
// carries one (see withDeleteTimes).
type deleteTimes struct {
	files   time.Duration // the C2 responses and snapshot files
	baseDir time.Duration // the base directory
}

type deleteTimesKey struct{}

// withDeleteTimes returns ctx carrying t, for hookDelete to fill in.
func withDeleteTimes(ctx context.Context, t *deleteTimes) context.Context {
	return context.WithValue(ctx, deleteTimesKey{}, t)
}

// deleteTimesFrom returns the deleteTimes that ctx carries, or nil.
func deleteTimesFrom(ctx context.Context) *deleteTimes {
	t, _ := ctx.Value(deleteTimesKey{}).(*deleteTimes)

	return t
}

// logDeleted logs the time of each step of the delete of experiment exp, which began at begun. The store step is the
// time until the config was deleted (at stored), less the file steps that its delete hook ran; the hooks step is the
// time after it.
func logDeleted(exp string, begun, stored time.Time, t deleteTimes) {
	now := time.Now()

	storeTime := max(stored.Sub(begun)-t.files-t.baseDir, 0)

	plog.Info(
		plog.TypeSystem, deleteStepsMessage, "exp", exp,
		"store", roundDuration(storeTime),
		"files", roundDuration(t.files),
		"base-dir", roundDuration(t.baseDir),
		"hooks", roundDuration(now.Sub(stored)),
		"total", roundDuration(now.Sub(begun)),
	)
}
