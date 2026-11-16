package cmd

import (
	"net/http"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"

	"phenix/util/common"
	ft "phenix/web/forward/forwardtypes"
)

// The VM completions give the interface indexes with their names, and the
// <vm>: prefixes and local files of vm cp.
func TestCompleteVMIndexesAndCopy(t *testing.T) { //nolint:paralleltest // replaces the phenix store and minimega
	expCmdFixture(t, nil)

	var (
		noFile  = cobra.ShellCompDirectiveNoFileComp
		files   = cobra.ShellCompDirectiveDefault
		noSpace = cobra.ShellCompDirectiveNoSpace
	)

	runCompletionTests(t, []completionTestCase{
		{
			name: "indexes", fn: completeVMInterfaceIndexes, args: []string{"stopped", "db"},
			want: []string{"0\tIF0", "1\tIF1"}, directive: noFile | cobra.ShellCompDirectiveKeepOrder,
		},
		{
			name: "indexes, prefix", fn: completeVMInterfaceIndexes, args: []string{"stopped", "db"}, toComplete: "1",
			want: []string{"1\tIF1"}, directive: noFile | cobra.ShellCompDirectiveKeepOrder,
		},
		{name: "indexes of a missing VM", fn: completeVMInterfaceIndexes, args: []string{"stopped", "none"}, directive: noFile},
		{name: "cp experiment", fn: completeVMCopyArgs, want: []string{"dry", "running"}, directive: noFile},
		{name: "cp source", fn: completeVMCopyArgs, args: []string{"stopped"}, want: []string{"web:", "db:"}, directive: noSpace},
		{
			name:       "cp source, prefix",
			fn:         completeVMCopyArgs,
			args:       []string{"stopped"},
			toComplete: "w",
			want:       []string{"web:"},
			directive:  noSpace,
		},
		{name: "cp local source", fn: completeVMCopyArgs, args: []string{"stopped"}, toComplete: "./", directive: files},
		{name: "cp path in the VM", fn: completeVMCopyArgs, args: []string{"stopped"}, toComplete: "web:/tm", directive: noFile},
		{name: "cp destination of a VM source", fn: completeVMCopyArgs, args: []string{"stopped", "web:/a"}, directive: files},
		{
			name: "cp destination of a local source", fn: completeVMCopyArgs, args: []string{"stopped", "./a"},
			want: []string{"web:", "db:"}, directive: noSpace,
		},
		{name: "cp after the last", fn: completeVMCopyArgs, args: []string{"stopped", "./a", "web:/a"}, directive: noFile},
	})
}

// The VLAN completions give the blacklisted IDs, the owners and the unowned
// namespaces of the cluster, and the aliases of an experiment as pins.
func TestCompleteVLANs(t *testing.T) { //nolint:paralleltest // replaces the phenix store and minimega
	vmCmdUseStore(t, vlanOutputConfigs(t)...)
	vlanCmdUseMinimega(t, true)

	noFile := cobra.ShellCompDirectiveNoFileComp

	runCompletionTests(t, []completionTestCase{
		{name: "blacklist", fn: completeBlacklistedVLANs, want: []string{"999"}, directive: noFile},
		{name: "owners", fn: completeVLANOwners, want: []string{"alpha", "beta", "gamma", "old-demo"}, directive: noFile},
		{name: "unowned namespaces", fn: completeUnownedNamespaces, want: []string{"old-demo"}, directive: noFile},
		{
			name: "alias pins", fn: completeVLANAliasPins, args: []string{"alpha"}, toComplete: "EXP",
			want: []string{"EXP_1=", "EXP_3="}, directive: noFile | cobra.ShellCompDirectiveNoSpace,
		},
	})
}

// completeForwardPorts gives vnc and the ports of the forwards of
// global-admin, and only vnc when the server does not answer.
func TestCompleteForwardPorts(t *testing.T) {
	fake := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSONBody(w, map[string][]ft.Listener{"listeners": {
			{Exp: "exp", VM: "web", SrcPort: 2222, DstHost: "127.0.0.1", DstPort: 22, Owner: socketUser},
			{Exp: "exp", VM: "web", SrcPort: 8080, DstHost: "127.0.0.1", DstPort: 80, Owner: "alice"},
		}})
	})

	original := common.UnixSocket
	t.Cleanup(func() { common.UnixSocket = original }) //nolint:reassign // restore

	noFile := cobra.ShellCompDirectiveNoFileComp

	common.UnixSocket = fake.socket //nolint:reassign // the fake server

	runCompletionTests(t, []completionTestCase{
		{name: "ports", fn: completeForwardPorts, args: []string{"exp", "web"}, want: []string{"vnc", "22"}, directive: noFile},
		{name: "no VM yet", fn: completeForwardPorts, args: []string{"exp"}, want: []string{"vnc"}, directive: noFile},
	})

	common.UnixSocket = filepath.Join(t.TempDir(), "none.sock") //nolint:reassign // no server

	runCompletionTests(t, []completionTestCase{
		{name: "no server", fn: completeForwardPorts, args: []string{"exp", "web"}, want: []string{"vnc"}, directive: noFile},
	})
}
