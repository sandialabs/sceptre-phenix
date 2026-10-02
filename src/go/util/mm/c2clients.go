package mm

import (
	"strings"
	"time"

	"phenix/util/mm/mmcli"
)

// fallbackC2CheckTimeout is how long checkC2ClientAlone waits for the client of
// a VM that can't be checked with the others (see filterSafe).
const fallbackC2CheckTimeout = 1 * time.Second

// C2ClientRef names a VM whose miniccc client is waited on, and whether the
// client is identified by the VM's UUID (C2IDClientsByUUID) rather than by the
// hostname it reports.
type C2ClientRef struct {
	VM     string
	ByUUID bool
}

// ActiveC2Clients reports, for each of the given VMs, whether its miniccc
// client is registered, exactly as IsC2ClientActive with a short timeout would
// report each, but with one `vm info` and one `cc client` for all of them
// rather than one of each (and a wait) per VM.
func (m Minimega) ActiveC2Clients(ns string, refs []C2ClientRef) map[C2ClientRef]bool {
	active := make(map[C2ClientRef]bool, len(refs))

	var batched []C2ClientRef

	for _, ref := range refs {
		if filterSafe(ref.VM) {
			batched = append(batched, ref)

			continue
		}

		active[ref] = m.checkC2ClientAlone(ns, ref)
	}

	if len(batched) == 0 {
		return active
	}

	var (
		identities = getVMIdentities(ns, "")
		clients    = c2ClientRows(ns)
	)

	for _, ref := range batched {
		// What `.filter name=<vm>` would have kept, in the same order.
		var matches VMs

		for _, vm := range identities {
			if filterMatches(ref.VM, vm.Name) {
				matches = append(matches, vm)
			}
		}

		if len(matches) == 0 {
			active[ref] = false

			continue
		}

		vm := pickVM(matches, ref.VM)

		column, value := "hostname", vm.Name
		if ref.ByUUID {
			column, value = "uuid", vm.UUID
		}

		if !filterSafe(value) {
			active[ref] = m.checkC2ClientAlone(ns, ref)

			continue
		}

		found := false

		for _, client := range clients {
			if filterMatches(value, client[column]) {
				found = true

				break
			}
		}

		active[ref] = found
	}

	return active
}

// checkC2ClientAlone checks one VM's client the way IsC2ClientActive does.
func (m Minimega) checkC2ClientAlone(ns string, ref C2ClientRef) bool {
	opts := []C2Option{C2NS(ns), C2VM(ref.VM), C2Timeout(fallbackC2CheckTimeout)}

	if ref.ByUUID {
		opts = append(opts, C2IDClientsByUUID())
	}

	return m.IsC2ClientActive(opts...) == nil
}

// c2ClientRows lists the namespace's registered miniccc clients.
func c2ClientRows(ns string) []map[string]string {
	cmd := mmcli.NewNamespacedCommand(ns)
	cmd.Command = ccClientCmd

	// No .columns: mmcli maps any requested column containing "host" to the
	// responding node, so `hostname` would be lost. The full header is small.
	return mmcli.RunTabular(cmd)
}

// filterSafe reports whether `.filter <column>=<value>` parses in minicli as a
// plain equality filter on that column with that value, so filterMatches
// answers exactly as minimega would. Values that would be read as another
// filter operator, or that the command line would split or unquote, are not.
func filterSafe(value string) bool {
	return value != "" && !strings.ContainsAny(value, "=~\"'`\\ \t\r\n")
}

// filterMatches reports whether minicli's `.filter <column>=<want>` keeps a row
// whose column holds cell: equal ignoring case, or (for a list or object cell)
// containing want, ignoring case.
func filterMatches(want, cell string) bool {
	want, cell = strings.ToLower(want), strings.ToLower(cell)

	if cell == want {
		return true
	}

	listLike := strings.HasPrefix(cell, "[") && strings.HasSuffix(cell, "]") ||
		strings.HasPrefix(cell, "{") && strings.HasSuffix(cell, "}")

	return listLike && strings.Contains(cell, want)
}
