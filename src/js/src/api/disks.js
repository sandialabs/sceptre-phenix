// Disk images, named by their path. The actions send their parameters in the
// query string.
import axiosInstance from '@/utils/axios.js';
import { useJobStore } from '@/utils/jobs.js';

// GET /disks, filtered by params such as expName or diskType; config carries
// the request's signal and meta
export function listDisks(params, config) {
  return axiosInstance.get('disks', { params, ...config });
}

// GET /disks/details?disk=: what the server reads from one listed image, such
// as its qcow header and partitions; config carries the request's signal
export function getDiskDetails(disk, config) {
  return axiosInstance.get('disks/details', { params: { disk }, ...config });
}

// POST /disks, with an uploaded file as FormData; onUploadProgress receives
// axios's progress events
export function uploadDisk(formData, onUploadProgress) {
  return axiosInstance.post(`disks`, formData, {
    headers: { 'Content-Type': 'multipart/form-data' },
    onUploadProgress,
  });
}

// DELETE /disks?disk=
export function deleteDisk(disk) {
  return axiosInstance.request({
    method: 'delete',
    url: 'disks',
    params: { disk },
  });
}

// POST /disks/commit?disk=: commits the disk into its backing image
export function commitDisk(disk) {
  return axiosInstance.post('disks/commit', null, { params: { disk } });
}

// POST /disks/snapshot?disk=&new=: a new image backed by the disk
export function snapshotDisk(disk, newName) {
  return axiosInstance.request({
    method: 'post',
    url: 'disks/snapshot',
    params: { disk, new: newName },
  });
}

// POST /disks/rebase?disk=&backing=&unsafe=
export function rebaseDisk(disk, backing, unsafe) {
  return axiosInstance.request({
    method: 'post',
    url: 'disks/rebase',
    params: { disk, backing, unsafe },
  });
}

// POST /disks/resize?disk=&size=
export function resizeDisk(disk, size) {
  return axiosInstance.request({
    method: 'post',
    url: 'disks/resize',
    params: { disk, size },
  });
}

// POST /disks/rename?disk=&new=
export function renameDisk(disk, newName) {
  return axiosInstance.request({
    method: 'post',
    url: 'disks/rename',
    params: { disk, new: newName },
  });
}

// POST /disks/clone/jobs?disk=&new=: the server copies the disk in a
// background job (see utils/jobs.js), which this returns
export function cloneDisk(disk, newName) {
  return useJobStore().start(
    axiosInstance.post('disks/clone/jobs', null, {
      params: { disk, new: newName },
    }),
  );
}
