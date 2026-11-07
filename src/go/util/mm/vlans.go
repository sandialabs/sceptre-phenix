package mm

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"phenix/util/mm/mmcli"
)

// ErrBlacklistRemovalUnsupported is returned by UnblacklistVLAN when minimega
// has no `clear vlans blacklist <vlan>` command.
var ErrBlacklistRemovalUnsupported = errors.New("minimega cannot remove a VLAN from its blacklist")

// Namespace is one minimega namespace, as `namespace` lists it.
type Namespace struct {
	Name string
	// MinVLAN and MaxVLAN are the namespace's VLAN range, 0 when not set.
	MinVLAN int
	MaxVLAN int
	Active  bool
}

// GetNamespaces lists minimega's namespaces, sorted by name.
func (Minimega) GetNamespaces(opts ...Option) ([]Namespace, error) {
	o := NewOptions(opts...)

	cmd := mmcli.NewCommand()
	cmd.Command = "namespace"

	rows, err := mmcli.RunTabularCheckedContext(o.ctx, cmd, nil)
	if err != nil {
		return nil, fmt.Errorf("listing minimega namespaces: %w", err)
	}

	namespaces := make([]Namespace, 0, len(rows))

	for _, row := range rows {
		ns := Namespace{Name: row["namespace"], MinVLAN: 0, MaxVLAN: 0, Active: row["active"] == "true"}

		if r := row["vlans"]; r != "" {
			lo, hi, ok := strings.Cut(r, "-")
			minID, err1 := strconv.Atoi(lo)
			maxID, err2 := strconv.Atoi(hi)

			if !ok || err1 != nil || err2 != nil {
				return nil, fmt.Errorf("namespace %s has the VLAN range %q, want <min>-<max>", ns.Name, r)
			}

			ns.MinVLAN, ns.MaxVLAN = minID, maxID
		}

		namespaces = append(namespaces, ns)
	}

	slices.SortFunc(namespaces, func(a, b Namespace) int { return strings.Compare(a.Name, b.Name) })

	return namespaces, nil
}

// GetVLANBlacklist lists the VLAN IDs on minimega's blacklist, sorted.
func (Minimega) GetVLANBlacklist(opts ...Option) ([]int, error) {
	o := NewOptions(opts...)

	cmd := mmcli.NewCommand()
	cmd.Command = "vlans blacklist"

	rows, err := mmcli.RunTabularCheckedContext(o.ctx, cmd, nil)
	if err != nil {
		return nil, fmt.Errorf("listing the minimega VLAN blacklist: %w", err)
	}

	ids := make([]int, 0, len(rows))

	for _, row := range rows {
		id, err := strconv.Atoi(row["vlan"])
		if err != nil {
			return nil, fmt.Errorf("converting blacklisted VLAN to integer: %w", err)
		}

		ids = append(ids, id)
	}

	slices.Sort(ids)

	return slices.Compact(ids), nil
}

// BlacklistVLAN adds the VLAN ID to minimega's blacklist.
func (Minimega) BlacklistVLAN(id int, opts ...Option) error {
	o := NewOptions(opts...)

	cmd := mmcli.NewCommand()
	cmd.Command = "vlans blacklist " + strconv.Itoa(id)

	if err := mmcli.ErrorResponse(o.run(cmd)); err != nil {
		return fmt.Errorf("blacklisting VLAN %d: %w", id, err)
	}

	return nil
}

// UnblacklistVLAN removes the VLAN ID from minimega's blacklist. The error
// wraps ErrBlacklistRemovalUnsupported when minimega has no such command.
func (Minimega) UnblacklistVLAN(id int, opts ...Option) error {
	o := NewOptions(opts...)

	cmd := mmcli.NewCommand()
	cmd.Command = "clear vlans blacklist " + strconv.Itoa(id)

	err := mmcli.ErrorResponse(o.run(cmd))
	if err == nil {
		return nil
	}

	if strings.Contains(err.Error(), "invalid command") {
		return fmt.Errorf("removing VLAN %d from the blacklist: %w: %w", id, ErrBlacklistRemovalUnsupported, err)
	}

	return fmt.Errorf("removing VLAN %d from the blacklist: %w", id, err)
}

// VLANBlacklistRemovable reports whether minimega has the command
// `clear vlans blacklist <vlan>`, from its usage lines in `help clear vlans`.
func (Minimega) VLANBlacklistRemovable(opts ...Option) (bool, error) {
	o := NewOptions(opts...)

	cmd := mmcli.NewCommand()
	cmd.Command = "help clear vlans"

	help, err := mmcli.SingleResponse(o.run(cmd))
	if err != nil {
		return false, fmt.Errorf("reading minimega help for clear vlans: %w", err)
	}

	return usageHasBlacklistRemoval(help), nil
}

// usageHasBlacklistRemoval reports whether a usage line of minimega help text
// is `clear vlans blacklist ...`. The long help text is not a usage line.
func usageHasBlacklistRemoval(help string) bool {
	usage := false

	for line := range strings.SplitSeq(help, "\n") {
		switch {
		case strings.TrimSpace(line) == "Usage:":
			usage = true
		case strings.TrimSpace(line) == "":
			usage = false
		case usage:
			if fields := strings.Fields(line); len(fields) >= 3 &&
				fields[0] == "clear" && fields[1] == "vlans" && fields[2] == "blacklist" {
				return true
			}
		}
	}

	return false
}

// ClearVLANs clears every VLAN alias of the namespace that NS names.
func (Minimega) ClearVLANs(opts ...Option) error {
	o := NewOptions(opts...)

	if o.ns == "" {
		return errors.New("no namespace provided")
	}

	cmd := mmcli.NewNamespacedCommand(o.ns)
	cmd.Command = "clear vlans"

	if err := mmcli.ErrorResponse(o.run(cmd)); err != nil {
		return fmt.Errorf("clearing the VLANs of namespace %s: %w", o.ns, err)
	}

	return nil
}
