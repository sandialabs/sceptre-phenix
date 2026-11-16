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

// What GET disks/details gives for a qcow2 v3 image, kali.qc2: an MBR disk
// with an ext4 root (1), an extended partition (2) and swap in it (5), used
// by the kali VM of the running bennu experiment, which injects into 1.
export const diskDetails = (fields = {}) => ({
  fullPath: '/phenix/images/kali.qc2',
  format: 'qcow2',
  formatVersion: 3,
  formatIssue: null,
  fileBytes: 15 * 1024 ** 3,
  allocatedBytes: 16 * 1024 ** 3,
  virtualBytes: 80 * 1024 ** 3,
  modified: '2026-09-28T14:12:00Z',
  qcow: {
    compat: '1.1',
    clusterSize: 65536,
    refcountBits: 16,
    encryption: 'none',
    compression: 'zlib',
    dirty: false,
    corrupt: false,
    externalDataFile: false,
    extendedL2: false,
    lazyRefcounts: false,
    snapshots: 0,
    backingFile: '',
    backingFormat: '',
  },
  fileUnavailable: '',
  partitionTable: 'mbr',
  diskId: '0x8f3c2a11',
  sectorSize: 512,
  partitions: [
    partition(1, 'Linux', '0x83', { bootable: true, fs: 'ext4' }),
    partition(2, 'Extended', '0x05', { extended: true }),
    partition(5, 'Linux swap / Solaris', '0x82', { logical: true, fs: 'swap' }),
  ],
  filesystem: null,
  partitionNotes: [],
  partitionsUnavailable: '',
  overlays: [],
  vms: [
    {
      experiment: 'bennu',
      running: true,
      vm: 'kali',
      drive: 0,
      injectPartition: 1,
    },
  ],
  ...fields,
});

// a partition as GET disks/details gives it, 1 GiB at 1 MiB per number
export const partition = (number, type, typeId, opts = {}) => ({
  number,
  type,
  typeId,
  start: number * 1024 ** 2,
  size: 1024 ** 3,
  bootable: !!opts.bootable,
  extended: !!opts.extended,
  logical: !!opts.logical,
  name: opts.name ?? '',
  guid: '',
  filesystem: opts.fs ? { type: opts.fs, version: '', label: '' } : null,
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
