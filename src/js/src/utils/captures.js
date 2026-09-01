// Computes the updated `captures` array for a VM after a packet-capture
// "stop" broadcast is received over the websocket. If `result` includes an
// `interface` field, only the capture entry for that interface is removed,
// leaving any other running captures for the VM untouched (per-interface
// stop). Otherwise, an empty array is returned (all captures were stopped).
export function applyStopCaptureUpdate(captures, result) {
  if (result && result.interface !== undefined) {
    return (captures || []).filter((c) => c.interface !== result.interface);
  }

  return [];
}

// Returns true if the given interface currently has a running packet
// capture.
export function isInterfaceCapturing(captures, iface) {
  return (captures || []).some((c) => c.interface === iface);
}

// Returns true if more than one packet capture is currently running for a
// VM. Used to decide whether stopping a capture on a specific interface
// should first prompt the user to choose between stopping just that
// interface or all of the VM's running captures.
export function hasMultipleCaptures(captures) {
  return (captures || []).length > 1;
}
