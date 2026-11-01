// An experiment's VMs: settings, snapshots, disks, forwards, discs, captures, files.
// Names go into the path as given unless a function says it encodes them.
import axiosInstance from '@/utils/axios.js';
import { useJobStore } from '@/utils/jobs.js';

const vmURL = (exp, vm) => 'experiments/' + exp + '/vms/' + vm;

// GET /vms: every running VM; config carries the request's signal and meta
export function listAllVms(config) {
  return axiosInstance.get('vms', config);
}

// GET /experiments/{exp}/vms
export function listVms(exp) {
  return axiosInstance.get('experiments/' + exp + '/vms');
}

// PATCH /experiments/{exp}/vms, setting each of vms ({ name, ... }) at once
export function updateVms(exp, vms) {
  return axiosInstance.patch('experiments/' + exp + '/vms', {
    vms,
    total: vms.length,
  });
}

// POST /experiments/{exp}/vms/actions: the server takes a power action on
// the VMs in a background job (see utils/jobs.js), which this returns
export function startVmActionsJob(exp, action, vms) {
  return useJobStore().start(
    axiosInstance.post(`experiments/${exp}/vms/actions`, { action, vms }),
  );
}

// GET /experiments/{exp}/vms/{vm}
export function getVm(exp, vm) {
  return axiosInstance.get(vmURL(exp, vm));
}

// PATCH /experiments/{exp}/vms/{vm}, with the settings to change
export function updateVm(exp, vm, update) {
  return axiosInstance.patch(vmURL(exp, vm), update);
}

// POST /experiments/{exp}/vms/{vm}/redeploy
export function redeployVm(exp, vm, body) {
  return axiosInstance.post(vmURL(exp, vm) + '/redeploy', body);
}

// GET /experiments/{exp}/vms/{vm}/snapshots
export function listVmSnapshots(exp, vm) {
  return axiosInstance.get(vmURL(exp, vm) + '/snapshots');
}

// POST /experiments/{exp}/vms/{vm}/snapshots, with no timeout
export function createVmSnapshot(exp, vm, filename) {
  return axiosInstance.post(
    vmURL(exp, vm) + '/snapshots',
    { filename },
    { timeout: 0 },
  );
}

// POST /experiments/{exp}/vms/{vm}/snapshots/{snapshot}: reverts the VM to
// the snapshot, with no timeout
export function restoreVmSnapshot(exp, vm, snapshot) {
  return axiosInstance.post(
    vmURL(exp, vm) + '/snapshots/' + snapshot,
    {},
    { timeout: 0 },
  );
}

// POST /experiments/{exp}/vms/{vm}/commit: a backing image of the VM's
// disk, with no timeout
export function commitVmDisk(exp, vm, filename) {
  return axiosInstance.post(
    vmURL(exp, vm) + '/commit',
    { filename },
    { timeout: 0 },
  );
}

// POST /experiments/{exp}/vms/{vm}/memorySnapshot, with no timeout
export function createVmMemorySnapshot(exp, vm, filename) {
  return axiosInstance.post(
    vmURL(exp, vm) + '/memorySnapshot',
    { filename },
    { timeout: 0 },
  );
}

// GET /experiments/{exp}/vms/{vm}/screenshot.png?size=&base64=true, both
// names encoded: a data URL, as text
export function getVmScreenshot(exp, vm, size, signal) {
  const e = encodeURIComponent(exp);
  const n = encodeURIComponent(vm);
  return axiosInstance.get(`experiments/${e}/vms/${n}/screenshot.png`, {
    params: { size, base64: true },
    responseType: 'text',
    signal,
  });
}

const forwardsURL = (exp, vm) => vmURL(exp, vm) + '/forwards';

// GET /experiments/{exp}/vms/{vm}/forwards
export function listVmForwards(exp, vm) {
  return axiosInstance.get(forwardsURL(exp, vm));
}

// POST /experiments/{exp}/vms/{vm}/forwards?src=&host=&dst=
export function createVmForward(exp, vm, params) {
  return axiosInstance.post(forwardsURL(exp, vm), null, { params });
}

// DELETE /experiments/{exp}/vms/{vm}/forwards?host=&dst=
export function deleteVmForward(exp, vm, params) {
  return axiosInstance.delete(forwardsURL(exp, vm), { params });
}

const cdromURL = (exp, vm) => vmURL(exp, vm) + '/cdrom';

// POST /experiments/{exp}/vms/{vm}/cdrom?isoPath=
export function insertVmDisc(exp, vm, isoPath) {
  return axiosInstance.post(cdromURL(exp, vm), null, { params: { isoPath } });
}

// DELETE /experiments/{exp}/vms/{vm}/cdrom
export function ejectVmDisc(exp, vm) {
  return axiosInstance.delete(cdromURL(exp, vm));
}

const capturesURL = (exp, vm) => vmURL(exp, vm) + '/captures';

// GET /experiments/{exp}/vms/{vm}/captures
export function listVmCaptures(exp, vm) {
  return axiosInstance.get(capturesURL(exp, vm));
}

// POST /experiments/{exp}/vms/{vm}/captures: starts capturing the
// interface's packets into the experiment file filename
export function createVmCapture(exp, vm, iface, filename) {
  return axiosInstance.post(capturesURL(exp, vm), {
    interface: iface,
    filename,
  });
}

// DELETE /experiments/{exp}/vms/{vm}/captures?iface=: stops the capture on
// one interface, or with no iface every capture of the VM
export function deleteVmCapture(exp, vm, iface) {
  const params = iface === undefined ? {} : { iface: iface };
  return axiosInstance.delete(capturesURL(exp, vm), { params: params });
}

// DELETE /experiments/{exp}/vms/{vm}/captures, with no query
export function deleteVmCaptures(exp, vm) {
  return axiosInstance.delete(capturesURL(exp, vm));
}

// POST /experiments/{exp}/vms/{vm}/mount: mounts the VM's files to browse
export function mountVm(exp, vm) {
  return axiosInstance.post(vmURL(exp, vm) + '/mount');
}

// DELETE /experiments/{exp}/vms/{vm}/unmount
export function unmountVm(exp, vm) {
  return axiosInstance.delete(vmURL(exp, vm) + '/unmount');
}

// GET /experiments/{exp}/vms/{vm}/files?path=: a folder of the mounted VM
export function listVmFiles(exp, vm, path) {
  return axiosInstance.get(vmURL(exp, vm) + '/files', { params: { path } });
}

// POST /experiments/{exp}/vms/{vm}/files/copy?path=&source=: copies the
// experiment file source into the mounted VM's folder path
export function copyFileToVm(exp, vm, path, source) {
  return axiosInstance.post(vmURL(exp, vm) + '/files/copy', null, {
    params: { path, source },
  });
}

// PUT /experiments/{exp}/vms/{vm}/files/upload?path=, with the file as
// FormData; onUploadProgress receives axios's progress events
export function uploadVmFile(exp, vm, path, formData, onUploadProgress) {
  return axiosInstance.put(vmURL(exp, vm) + '/files/upload', formData, {
    params: { path },
    headers: { 'Content-Type': 'multipart/form-data' },
    onUploadProgress,
  });
}
