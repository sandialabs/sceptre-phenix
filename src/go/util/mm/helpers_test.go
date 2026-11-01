package mm

import (
	"testing"

	"phenix/util/mm/mmtest"
)

// useFakeMinimega points mmcli at the fake minimega (see mmtest.Use).
func useFakeMinimega(t *testing.T, handle mmtest.Handler) func() []mmtest.Command {
	t.Helper()

	return mmtest.Use(t, handle)
}
