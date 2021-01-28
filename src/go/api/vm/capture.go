package vm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"phenix/api/experiment"
	"phenix/internal/mm"
)

var (
	ErrCaptureExists = errors.New("capture already exists")
	ErrNoCaptures    = errors.New("no captures exist")
)

// StartCapture starts a packet capture on the given interface for the given VM
// in the given experiment. The captured packets are written to the experiment's
// files directory using the base name of the provided output file in PCAP
// format. It returns any errors encountered while starting the packet capture.
func StartCapture(expName, vmName string, iface int, out string) error {
	if expName == "" {
		return fmt.Errorf("no experiment name provided")
	}

	if vmName == "" {
		return fmt.Errorf("no VM name provided")
	}

	if out == "" {
		return fmt.Errorf("no output file provided")
	}

	vm, err := Get(expName, vmName)
	if err != nil {
		return fmt.Errorf("getting VM details: %w", err)
	}

	if !vm.Running {
		return fmt.Errorf("VM is not running")
	}

	if iface < 0 || iface >= len(vm.Networks) {
		return fmt.Errorf("invalid interface provided for capture")
	}

	if vm.Networks[iface] == "disconnected" {
		return fmt.Errorf("cannot capture on a disconnected interface")
	}

	if ext := filepath.Ext(out); ext != ".pcap" {
		out = out + ".pcap"
	}

	out = fmt.Sprintf("%s/files/%s", expName, filepath.Base(out))

	if err := mm.StartVMCapture(mm.NS(expName), mm.VMName(vmName), mm.CaptureInterface(iface), mm.CaptureFile(out)); err != nil {
		return fmt.Errorf("starting VM capture for interface %d on VM %s in experiment %s: %w", iface, vmName, expName, err)
	}

	return nil
}

// StopCaptures stops all currently running packet captures for the given VM in
// the given experiment. Due to a limitation in minimega, it is not possible to
// stop a single capture if more than one capture is running for a VM. It
// returns any errors encountered while stopping the packet captures.
func StopCaptures(expName, vmName string) error {
	if expName == "" {
		return fmt.Errorf("no experiment name provided")
	}

	if vmName == "" {
		return fmt.Errorf("no VM name provided")
	}

	captures := mm.GetVMCaptures(mm.NS(expName), mm.VMName(vmName))

	if captures == nil {
		return fmt.Errorf("VM %s in experiment %s: %w", vmName, expName, ErrNoCaptures)
	}

	exp, err := experiment.Get(expName)
	if err != nil {
		return fmt.Errorf("getting experiment %s: %w", expName, err)
	}

	dir := fmt.Sprintf("%s/captures", exp.Spec.BaseDir())

	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating files directory for experiment %s: %w", expName, err)
	}

	if err := mm.StopVMCapture(mm.NS(expName), mm.VMName(vmName)); err != nil {
		return fmt.Errorf("stopping VM captures for VM %s in experiment %s: %w", vmName, expName, err)
	}

	return nil
}
