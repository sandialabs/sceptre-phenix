package mm

import (
	"testing"

	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/util/mm/mmtest"
)

//nolint:gochecknoglobals // cmp options shared by the package's tests
var (
	// ignoreRaw compares received commands by namespace and base alone.
	ignoreRaw = cmpopts.IgnoreFields(mmtest.Command{}, "Raw")
	// sortCommands compares received commands regardless of their order.
	sortCommands = cmpopts.SortSlices(func(a, b mmtest.Command) bool { return a.Base < b.Base })
)

// useFakeMinimega points mmcli at the fake minimega (see mmtest.Use), with
// nothing remembered about the cluster from earlier tests.
func useFakeMinimega(t *testing.T, handle mmtest.Handler) func() []mmtest.Command {
	t.Helper()

	received := mmtest.Use(t, handle)

	ForgetCluster()
	t.Cleanup(ForgetCluster)

	return received
}
