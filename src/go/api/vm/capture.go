package vm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"phenix/api/experiment"
	"phenix/util/mm"
)

var (
	ErrCaptureExists = errors.New("capture already exists")
	ErrNoCaptures    = errors.New("no captures exist")
)

// ResolveInterface resolves the given interface identifier - either a
// zero-based index (e.g. "0") or the interface name as declared in the
// experiment topology (e.g. "IF0") - to the interface's zero-based index for
// the given VM. Name matching is case-insensitive. It returns an error if the
// identifier doesn't match a valid interface index or name for the VM.
func ResolveInterface(v *mm.VM, iface string) (int, error) {
	if v == nil {
		return 0, errors.New("no VM details provided")
	}

	if iface == "" {
		return 0, errors.New("no interface identifier provided")
	}

	if idx, err := strconv.Atoi(iface); err == nil {
		if idx < 0 || idx >= len(v.Networks) {
			return 0, fmt.Errorf("interface index %d is out of range for VM %s", idx, v.Name)
		}

		return idx, nil
	}

	for idx, name := range v.IfaceNames {
		if strings.EqualFold(name, iface) {
			return idx, nil
		}
	}

	return 0, fmt.Errorf("no interface named %q found for VM %s", iface, v.Name)
}

// StartCapture starts a packet capture on the given interface for the given VM
// in the given experiment. The captured packets are written to the experiment's
// files directory using the base name of the provided output file in PCAP
// format. It returns any errors encountered while starting the packet capture.
func StartCapture(expName, vmName string, iface int, out string) error {
	if expName == "" {
		return errors.New("no experiment name provided")
	}

	if vmName == "" {
		return errors.New("no VM name provided")
	}

	if out == "" {
		return errors.New("no output file provided")
	}

	v, err := Get(expName, vmName)
	if err != nil {
		return fmt.Errorf("getting VM details: %w", err)
	}

	return StartCaptureForVM(v, expName, vmName, iface, out)
}

// StartCaptureForVM behaves like StartCapture, but accepts an already-fetched
// VM instead of looking it up itself. Callers that already have the VM's
// details on hand (e.g. because they resolved an interface name to an index
// via ResolveInterface, which also requires the VM's details) should use this
// to avoid an extra, redundant VM lookup.
func StartCaptureForVM(v *mm.VM, expName, vmName string, iface int, out string) error {
	if v == nil {
		return errors.New("no VM details provided")
	}

	if expName == "" {
		return errors.New("no experiment name provided")
	}

	if vmName == "" {
		return errors.New("no VM name provided")
	}

	if out == "" {
		return errors.New("no output file provided")
	}

	if !v.Running {
		return errors.New("vm is not running")
	}

	if iface < 0 || iface >= len(v.Networks) {
		return errors.New("invalid interface provided for capture")
	}

	if v.Networks[iface] == "disconnected" {
		return errors.New("cannot capture on a disconnected interface")
	}

	if ext := filepath.Ext(out); ext != ".pcap" {
		out += ".pcap"
	}

	out = fmt.Sprintf("%s/files/%s", expName, filepath.Base(out))

	if err := mm.StartVMCapture(
		mm.NS(expName),
		mm.VMName(vmName),
		mm.CaptureInterface(iface),
		mm.CaptureFile(out),
	); err != nil {
		return fmt.Errorf(
			"starting VM capture for interface %d on VM %s in experiment %s: %w",
			iface,
			vmName,
			expName,
			err,
		)
	}

	return nil
}

// StopCaptures stops all currently running packet captures for the given VM in
// the given experiment. It returns any errors encountered while stopping the
// packet captures.
func StopCaptures(expName, vmName string) error {
	return stopCaptures(expName, vmName, nil)
}

// StopCapture stops the packet capture running on the given interface for the
// given VM in the given experiment, leaving any other running captures for
// the VM untouched. It returns any errors encountered while stopping the
// packet capture.
func StopCapture(expName, vmName string, iface int) error {
	if iface < 0 {
		return errors.New("invalid interface provided for capture")
	}

	return stopCaptures(expName, vmName, &iface)
}

// stopCaptures stops packet captures for the given VM in the given
// experiment. If iface is non-nil, only the capture running on that
// interface index is stopped; otherwise all captures for the VM are stopped.
func stopCaptures(expName, vmName string, iface *int) error {
	if expName == "" {
		return errors.New("no experiment name provided")
	}

	if vmName == "" {
		return errors.New("no VM name provided")
	}

	captures := mm.GetVMCaptures(mm.NS(expName), mm.VMName(vmName))

	if captures == nil {
		return fmt.Errorf("vm %s in experiment %s: %w", vmName, expName, ErrNoCaptures)
	}

	if iface != nil {
		var found bool

		for _, capture := range captures {
			if capture.Interface == *iface {
				found = true
				break
			}
		}

		if !found {
			return fmt.Errorf(
				"interface %d on VM %s in experiment %s: %w",
				*iface,
				vmName,
				expName,
				ErrNoCaptures,
			)
		}
	}

	exp, err := experiment.Get(expName)
	if err != nil {
		return fmt.Errorf("getting experiment %s: %w", expName, err)
	}

	dir := exp.Spec.BaseDir() + "/captures"

	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("creating files directory for experiment %s: %w", expName, err)
	}

	opts := []mm.Option{mm.NS(expName), mm.VMName(vmName)}

	if iface != nil {
		opts = append(opts, mm.CaptureInterface(*iface))
	}

	if err := mm.StopVMCapture(opts...); err != nil {
		if iface != nil {
			return fmt.Errorf(
				"stopping VM capture for interface %d on VM %s in experiment %s: %w",
				*iface,
				vmName,
				expName,
				err,
			)
		}

		return fmt.Errorf(
			"stopping VM captures for VM %s in experiment %s: %w",
			vmName,
			expName,
			err,
		)
	}

	return nil
}
