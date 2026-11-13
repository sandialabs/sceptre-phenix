package shell

import "time"

// WithKillDelay returns a Shell that waits d, not killDelay, before it sends
// SIGKILL and before it stops the read of the output.
func WithKillDelay(d time.Duration) Shell { //nolint:ireturn // the tests call it as a Shell
	return shell{killDelay: d}
}
