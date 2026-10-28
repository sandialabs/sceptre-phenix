package mm

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"phenix/util/mm/mmcli"
	"phenix/util/plog"
)

// ErrInvalidQoS wraps why a link quality setting is one minimega cannot apply.
var ErrInvalidQoS = errors.New("invalid link quality")

// maxLossPercent is the loss minimega accepts up to, but not including.
const maxLossPercent = 100

var (
	// qosLossRegex matches a loss percent: a decimal number with no sign or exponent.
	qosLossRegex = regexp.MustCompile(`^\d+(\.\d+)?$`)

	// qosDelayRegex matches a delay in a unit that both minimega and tc take.
	qosDelayRegex = regexp.MustCompile(`^\d+(\.\d+)?(us|ms|s)$`)

	// qosRateRegex matches a rate as `qos add` takes it: a whole number from 1
	// to 999999999 and its unit, such as 10mbit.
	qosRateRegex = regexp.MustCompile(`^([1-9][0-9]{0,8})(kbit|mbit|gbit)$`)
)

// InterfaceQoS is the link quality that minimega applies to the traffic one
// VM interface receives. Each value is as minimega reports it, and empty when
// it is not set.
type InterfaceQoS struct {
	Interface int    `json:"interface"`
	Loss      string `json:"loss"`
	Delay     string `json:"delay"`
	Rate      string `json:"rate"`
}

// Empty reports whether q sets no link quality.
func (q InterfaceQoS) Empty() bool {
	return q.Loss == "" && q.Delay == "" && q.Rate == ""
}

// ValidateQoS returns an error wrapping ErrInvalidQoS unless minimega can apply
// q: at least one setting, a loss that is a decimal percent from 0 up to 100,
// a decimal delay in us, ms or s, and a rate of a whole number from 1 to
// 999999999 of kbit, mbit or gbit. A rate cannot go with a loss or a delay.
func ValidateQoS(q InterfaceQoS) error {
	if q.Empty() {
		return fmt.Errorf("%w: give a loss, a delay or a rate", ErrInvalidQoS)
	}

	if q.Rate != "" && (q.Loss != "" || q.Delay != "") {
		return fmt.Errorf("%w: a rate cannot go with a loss or a delay", ErrInvalidQoS)
	}

	if q.Loss != "" {
		loss, err := strconv.ParseFloat(q.Loss, 64)
		if !qosLossRegex.MatchString(q.Loss) || err != nil || loss >= maxLossPercent {
			return fmt.Errorf("%w: loss %q is not a percent from 0 up to 100", ErrInvalidQoS, q.Loss)
		}
	}

	if q.Delay != "" && !qosDelayRegex.MatchString(q.Delay) {
		return fmt.Errorf("%w: delay %q is not a duration in us, ms or s, such as 100ms", ErrInvalidQoS, q.Delay)
	}

	if q.Rate != "" && !qosRateRegex.MatchString(q.Rate) {
		return fmt.Errorf("%w: rate %q is not a whole number from 1 to 999999999 of kbit, mbit or gbit, such as 10mbit",
			ErrInvalidQoS, q.Rate)
	}

	return nil
}

// GetVMQoS returns the link quality of each interface of the VM vm in the
// namespace ns, in interface order. Its error wraps ErrVMNotFound when minimega
// does not have the VM.
func GetVMQoS(ctx context.Context, ns, vm string) ([]InterfaceQoS, error) {
	cmd := mmcli.NewNamespacedCommand(ns)
	cmd.Command = vmInfoCmd
	cmd.Columns = []string{"name", "vlan", "qos"}
	cmd.Filters = []string{"name=" + vm}

	rows, err := mmcli.RunTabularCheckedContext(ctx, cmd, nil)
	if len(rows) == 0 {
		if err != nil {
			return nil, fmt.Errorf("reading the link quality of VM %s: %w", vm, err)
		}

		if err := ended(ctx); err != nil {
			return nil, err
		}

		return nil, &VMNotFoundError{Err: fmt.Errorf("VM %s not found in namespace %s", vm, ns)}
	}

	var (
		row     = rows[0]
		count   = len(parseList(row["vlan"]))
		applied = parseQoS(row["qos"])
		ifaces  = make([]InterfaceQoS, count)
	)

	for i := range ifaces {
		ifaces[i] = applied[i]
		ifaces[i].Interface = i
	}

	return ifaces, nil
}

// parseQoS reads the `qos` column of `vm info`, such as
// `[0: loss 0.5 delay 100ms, 1: rate 10mbit]`, by interface index.
func parseQoS(column string) map[int]InterfaceQoS {
	applied := make(map[int]InterfaceQoS)

	for _, entry := range parseList(column) {
		index, settings, ok := strings.Cut(entry, ": ")
		if !ok {
			continue
		}

		i, err := strconv.Atoi(index)
		if err != nil || i < 0 {
			continue
		}

		q := InterfaceQoS{Interface: i} //nolint:exhaustruct // the settings are read below
		fields := strings.Fields(settings)

		for f := 0; f+1 < len(fields); f += 2 {
			switch fields[f] {
			case "loss":
				q.Loss = fields[f+1]
			case "delay":
				q.Delay = fields[f+1]
			case "rate":
				q.Rate = fields[f+1]
			}
		}

		applied[i] = q
	}

	return applied
}

// qosAddCommands are the `qos add` commands that apply q to the interface iface of the VM vm.
func qosAddCommands(vm string, iface int, q InterfaceQoS) []string {
	var cmds []string

	if q.Loss != "" {
		cmds = append(cmds, fmt.Sprintf("qos add %s %d loss %s", vm, iface, q.Loss))
	}

	if q.Delay != "" {
		cmds = append(cmds, fmt.Sprintf("qos add %s %d delay %s", vm, iface, q.Delay))
	}

	if match := qosRateRegex.FindStringSubmatch(q.Rate); match != nil {
		cmds = append(cmds, fmt.Sprintf("qos add %s %d rate %s %s", vm, iface, match[1], match[2]))
	}

	return cmds
}

// SetInterfaceQoS replaces the link quality of the interface iface of the VM
// vm in the namespace ns with q: it clears the interface's link quality, then
// adds each setting q gives. Its error wraps ErrInvalidQoS, before minimega is
// sent anything, for a q that ValidateQoS refuses, and also when minimega
// refuses a setting. Then it puts back previous, the interface's link quality
// before the change, and logs a failure to do so.
func SetInterfaceQoS(ctx context.Context, ns, vm string, iface int, q, previous InterfaceQoS) error {
	if err := ValidateQoS(q); err != nil {
		return err
	}

	if err := ClearInterfaceQoS(ctx, ns, vm, iface); err != nil {
		return err
	}

	// the interface was cleared, so the settings are added whatever ctx says
	ctx = context.WithoutCancel(ctx)

	if err := addInterfaceQoS(ctx, ns, vm, iface, q); err != nil {
		if restoreErr := restoreInterfaceQoS(ctx, ns, vm, iface, previous); restoreErr != nil {
			plog.Error(plog.TypeSystem, "restoring vm link quality", "ns", ns, "vm", vm, "interface", iface, "err", restoreErr)
		}

		return fmt.Errorf("%w: %w", ErrInvalidQoS, err)
	}

	return nil
}

// addInterfaceQoS adds each setting q gives to the interface iface of the VM vm in the namespace ns.
func addInterfaceQoS(ctx context.Context, ns, vm string, iface int, q InterfaceQoS) error {
	for _, command := range qosAddCommands(vm, iface, q) {
		cmd := mmcli.NewNamespacedCommand(ns)
		cmd.Command = command

		if err := mmcli.VMTargetErrorResponse(mmcli.RunContext(ctx, cmd)); err != nil {
			return fmt.Errorf("setting the link quality of interface %d of VM %s: %w", iface, vm, err)
		}
	}

	return nil
}

// restoreInterfaceQoS sets the interface iface of the VM vm in the namespace
// ns back to previous, after minimega applied only part of a change.
func restoreInterfaceQoS(ctx context.Context, ns, vm string, iface int, previous InterfaceQoS) error {
	if err := ClearInterfaceQoS(ctx, ns, vm, iface); err != nil {
		return err
	}

	return addInterfaceQoS(ctx, ns, vm, iface, previous)
}

// ClearInterfaceQoS removes the link quality of the interface iface of the VM
// vm in the namespace ns.
func ClearInterfaceQoS(ctx context.Context, ns, vm string, iface int) error {
	cmd := mmcli.NewNamespacedCommand(ns)
	cmd.Command = fmt.Sprintf("clear qos %s %d", vm, iface)

	if err := mmcli.VMTargetErrorResponse(mmcli.RunContext(ctx, cmd)); err != nil {
		return fmt.Errorf("clearing the link quality of interface %d of VM %s: %w", iface, vm, err)
	}

	return nil
}
