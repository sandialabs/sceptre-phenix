// Experiments and what runs across one: its apps, netflow captures and health.
// Names go into the path as given unless a function says it encodes them.
import axiosInstance from '@/utils/axios.js';

// GET /options: the choices the create-experiment card offers
export function getExperimentOptions() {
  return axiosInstance.get('/options');
}

// POST /experiments, with no timeout
export function createExperiment(request) {
  return axiosInstance.post('experiments', request, { timeout: 0 });
}

// GET /experiments/{name}; config carries params such as the VM table's
// page, and the request's signal
export function getExperiment(name, config) {
  return axiosInstance.get('experiments/' + name, config);
}

// PATCH /experiments/{name}, as with the VLAN assignments
export function updateExperiment(name, body) {
  return axiosInstance.patch('experiments/' + name, body);
}

// DELETE /experiments/{name}
export function deleteExperiment(name) {
  return axiosInstance.delete('experiments/' + name);
}

// POST /experiments/{name}/start
export function startExperiment(name) {
  return axiosInstance.post('experiments/' + name + '/start');
}

// POST /experiments/{name}/stop; force=true marks it stopped even when
// minimega cannot kill its VMs
export function stopExperiment(name, { force = false } = {}) {
  const url = 'experiments/' + name + '/stop';
  if (!force) return axiosInstance.post(url);
  return axiosInstance.post(url, null, { params: { force: true } });
}

// POST /experiments/{name}/schedule: places the VMs on hosts by algorithm
export function scheduleExperiment(name, algorithm) {
  return axiosInstance.post('experiments/' + name + '/schedule', {
    algorithm,
  });
}

// POST /experiments/{name}/trigger?apps=, apps a comma-separated list
export function triggerApps(name, apps) {
  return axiosInstance.post('experiments/' + name + '/trigger', null, {
    params: { apps },
  });
}

// GET /experiments/{name}/apps: whether each app is running, by name
export function getExperimentApps(name) {
  return axiosInstance.get('experiments/' + name + '/apps');
}

// POST /experiments/{name}/trigger?apps=soh, with the query in the URL
export function triggerSoh(name) {
  return axiosInstance.post('experiments/' + name + '/trigger?apps=soh');
}

// GET /experiments/{name}/soh, filtered by statusFilter when it is given
export function getSoh(name, statusFilter, signal) {
  let url = 'experiments/' + name + '/soh';
  if (statusFilter) {
    url = url + '?statusFilter=' + statusFilter;
  }
  return axiosInstance.get(url, { signal });
}

const netflowURL = (name) => `experiments/${name}/netflow`;

// GET /experiments/{name}/netflow: fails unless a capture is running
export function getNetflowCapture(name) {
  return axiosInstance.get(netflowURL(name));
}

// POST /experiments/{name}/netflow
export function startNetflowCapture(name) {
  return axiosInstance.post(netflowURL(name));
}

// DELETE /experiments/{name}/netflow
export function stopNetflowCapture(name) {
  return axiosInstance.delete(netflowURL(name));
}

// the recorded netflow captures of an experiment, the name and ID encoded
const netflowCapturesURL = (name) =>
  `experiments/${encodeURIComponent(name)}/netflow/captures`;

// GET /experiments/{name}/netflow/captures, newest first
export function listNetflowCaptures(name) {
  return axiosInstance.get(netflowCapturesURL(name));
}

// HEAD /experiments/{name}/netflow/captures/{id}?format=, to check that a
// capture is still there before downloading it
export function headNetflowCapture(name, id, format) {
  return axiosInstance.head(
    `${netflowCapturesURL(name)}/${encodeURIComponent(id)}`,
    { params: { format } },
  );
}

// where a capture downloads from in the format; the token goes in the query,
// as for job artifacts, since a link cannot send the auth header
export function netflowCaptureURL(name, id, format, token) {
  const query = new URLSearchParams({ format });
  if (token) query.set('token', token);
  return `${import.meta.env.BASE_URL}api/v1/${netflowCapturesURL(name)}/${encodeURIComponent(id)}?${query}`;
}

// GET /experiments/{name}/captures, the name encoded: the packet captures
// running on the experiment's VMs
export function listExperimentCaptures(name) {
  return axiosInstance.get(`experiments/${encodeURIComponent(name)}/captures`);
}
