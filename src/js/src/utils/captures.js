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

// The file a packet capture started from the UI writes to the experiment's
// files: <vm>_<interface>_<stamp>.pcap, so captures started together on
// several interfaces each get their own file.
export function captureFilename(vmName, iface, time) {
  return `${[vmName, iface, time].join('_')}.pcap`;
}
