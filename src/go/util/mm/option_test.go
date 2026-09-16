package mm

import "testing"

// TestCaptureInterfaceSetsIfaceAndFlag verifies that the CaptureInterface
// option records both the interface index and a flag indicating that an
// interface was explicitly requested. This distinction is what lets
// StopVMCapture tell the difference between "stop all captures for the VM"
// and "stop only the capture on interface 0" (interface index 0 is also the
// options struct's zero value).
func TestCaptureInterfaceSetsIfaceAndFlag(t *testing.T) {
	o := NewOptions(VMName("test-vm"), CaptureInterface(0))

	if !o.captureIfaceSet {
		t.Fatal("expected captureIfaceSet to be true after CaptureInterface(0)")
	}

	if o.captureIface != 0 {
		t.Fatalf("expected captureIface to be 0, got %d", o.captureIface)
	}
}

func TestCaptureInterfaceSetsNonZeroIface(t *testing.T) {
	o := NewOptions(VMName("test-vm"), CaptureInterface(3))

	if !o.captureIfaceSet {
		t.Fatal("expected captureIfaceSet to be true after CaptureInterface(3)")
	}

	if o.captureIface != 3 {
		t.Fatalf("expected captureIface to be 3, got %d", o.captureIface)
	}
}

// TestNoCaptureInterfaceLeavesFlagUnset ensures that omitting the
// CaptureInterface option (as when stopping all captures for a VM) leaves
// captureIfaceSet false so callers can distinguish it from an explicit
// request for interface 0.
func TestNoCaptureInterfaceLeavesFlagUnset(t *testing.T) {
	o := NewOptions(VMName("test-vm"))

	if o.captureIfaceSet {
		t.Fatal("expected captureIfaceSet to be false when CaptureInterface is not used")
	}
}
