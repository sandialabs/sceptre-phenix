// VLAN IDs across the cluster: the rows of each experiment and minimega
// namespace, free IDs, the minimega blacklist, and an experiment's pins and
// range. Names and aliases are encoded into the path.
import axiosInstance from '@/utils/axios.js';

const enc = encodeURIComponent;
const expURL = (name) => `experiments/${enc(name)}/vlans`;
const forced = (force) => (force ? { params: { force: true } } : undefined);

// GET /vlans: { vlans, experiments, issues, blacklist?, minimega }; params
// filter it (experiment, vlan, state, issues); config carries signal and meta
export function listVlans(params, config) {
  return axiosInstance.get('vlans', { params, ...config });
}

// GET /vlans/{id}: its rows, the VMs on it and its issues
export function getVlan(id, config) {
  return axiosInstance.get(`vlans/${enc(id)}`, config);
}

// GET /vlans/free: { start, end }, count IDs in a row; 404 when none
export function findFreeVlans({ count = 1, min, max, outsideRanges } = {}) {
  const params = { count };
  if (min) params.min = min;
  if (max) params.max = max;
  if (outsideRanges) params.outside_ranges = true;
  return axiosInstance.get('vlans/free', { params });
}

// POST /vlans/check: { issues } that a proposed spec would have
export function checkVlans({ experiment, aliases = {}, min = 0, max = 0 }) {
  return axiosInstance.post('vlans/check', { experiment, aliases, min, max });
}

// GET /vlans/blacklist: { vlans, removable }
export function getVlanBlacklist(config) {
  return axiosInstance.get('vlans/blacklist', config);
}

// PUT /vlans/blacklist/{id}
export function addVlanToBlacklist(id) {
  return axiosInstance.put(`vlans/blacklist/${enc(id)}`);
}

// DELETE /vlans/blacklist/{id}: 409 when a VM uses it, 501 when minimega
// cannot remove IDs
export function removeVlanFromBlacklist(id) {
  return axiosInstance.delete(`vlans/blacklist/${enc(id)}`);
}

// DELETE /vlans/allocations/{namespace}: clears an unowned namespace's IDs
export function releaseVlanAllocations(namespace) {
  return axiosInstance.delete(`vlans/allocations/${enc(namespace)}`);
}

// GET /experiments/{name}/vlans: { experiment, vlans, issues }
export function getExperimentVlans(name, config) {
  return axiosInstance.get(expURL(name), config);
}

// PUT /experiments/{name}/vlans/aliases/{alias}: pins the alias to id
export function pinVlanAlias(name, alias, id, { force = false } = {}) {
  return axiosInstance.put(
    `${expURL(name)}/aliases/${enc(alias)}`,
    { id },
    forced(force),
  );
}

// DELETE /experiments/{name}/vlans/aliases/{alias}: clears the pin
export function unpinVlanAlias(name, alias) {
  return axiosInstance.delete(`${expURL(name)}/aliases/${enc(alias)}`);
}

// PUT /experiments/{name}/vlans/range
export function setVlanRange(name, min, max, { force = false } = {}) {
  return axiosInstance.put(
    `${expURL(name)}/range`,
    { min, max },
    forced(force),
  );
}

// DELETE /experiments/{name}/vlans/range
export function clearVlanRange(name) {
  return axiosInstance.delete(`${expURL(name)}/range`);
}

// POST /experiments/{name}/vlans/pin-live: copies the live IDs into the spec
export function pinLiveVlans(name) {
  return axiosInstance.post(`${expURL(name)}/pin-live`);
}
