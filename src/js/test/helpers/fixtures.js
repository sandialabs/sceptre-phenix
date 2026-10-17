// Builders for disks, VMs, websocket messages and failed requests; it imports
// nothing a test mocks, so any test file may use it.

const basename = (path) => path.slice(path.lastIndexOf('/') + 1);

// A disk as the disks API lists it. One with no path relative to the files
// directory lies outside it and is read-only.
export const disk = (fullPath, relativePath = '', fields = {}) => ({
  kind: 'VM',
  name: basename(fullPath),
  fullPath,
  relativePath,
  outsideFilesDir: !relativePath,
  readOnly: !relativePath,
  inUse: false,
  backingImages: [],
  ...fields,
});

// a running VM, named web, as the experiment's VM list gives it
export const vm = (fields = {}) => ({
  name: 'web',
  state: 'RUNNING',
  running: true,
  cpus: 1,
  ram: 2048,
  ccActive: true,
  snapshot: true,
  disk: '/phenix/images/web_snap.qc2',
  cdRom: '',
  networks: ['DMZ (149)', 'MGMT (151)'],
  ipv4: ['10.0.0.5', '10.1.0.5'],
  taps: ['mega_tap1', 'mega_tap2'],
  captures: [],
  ...fields,
});

// a message the server sends over the websocket
export const wsMsg = (type, name, action, result, seq) => ({
  resource: { type, name, action },
  result,
  // the server's sequence number, when the test orders messages by it
  ...(seq === undefined ? {} : { seq }),
});

// the error axios rejects with when the server answers with `status`
export const httpError = (status, data) =>
  Object.assign(new Error(String(status)), { response: { status, data } });
