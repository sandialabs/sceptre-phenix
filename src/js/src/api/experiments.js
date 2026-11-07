// Experiments and what runs across one: its apps, netflow captures and health.
// Names go into the path as given unless a function says it encodes them.
import axiosInstance from '@/utils/axios.js';

// GET /experiments?vms=false: the experiments with VM counts but no VMs;
// config carries the request's signal and meta
export function listExperiments(config) {
  return axiosInstance.get('experiments?vms=false', config);
}

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

// POST /experiments/{name}/start; options is the body (see startRequestBody
// in utils/startOptions.js), and no options sends no body, the plain start
export function startExperiment(name, options = {}) {
  const url = 'experiments/' + name + '/start';
  if (!options || !Object.keys(options).length) return axiosInstance.post(url);
  return axiosInstance.post(url, options);
}

// POST /experiments/{name}/stop; force=true marks it stopped even when
// minimega cannot kill its VMs
export function stopExperiment(name, { force = false } = {}) {
  const url = 'experiments/' + name + '/stop';
  if (!force) return axiosInstance.post(url);
  return axiosInstance.post(url, null, { params: { force: true } });
}

// GET /experiments/{name}/schedule: { schedule: [{ vm, host }] }
export function getExperimentSchedule(name) {
  return axiosInstance.get('experiments/' + name + '/schedule');
}

// GET /schedulers: { schedulers: [{ name, builtin }] }
export function listSchedulers() {
  return axiosInstance.get('schedulers');
}

// POST /experiments/{name}/schedule: places the VMs on hosts by algorithm
export function scheduleExperiment(name, algorithm) {
  return axiosInstance.post('experiments/' + name + '/schedule', {
    algorithm,
  });
}

// POST /experiments/{name}/trigger?apps=&stage=, apps a comma-separated
// list; the server runs the running stage when stage is left out
export function triggerApps(name, apps, stage = 'running') {
  const params = stage === 'running' ? { apps } : { apps, stage };
  return axiosInstance.post('experiments/' + name + '/trigger', null, {
    params,
  });
}

// GET /experiments/{name}/apps: whether each app is running, by name
export function getExperimentApps(name) {
  return axiosInstance.get('experiments/' + name + '/apps');
}

// GET /soh: every experiment's state of health, as far as the role may get it
export function listSoh(config) {
  return axiosInstance.get('soh', config);
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
// running on the experiment's VMs, and its experiment captures with whether
// minimega supports them and how many Open vSwitch mirrors each host uses
export function listExperimentCaptures(name) {
  return axiosInstance.get(`experiments/${encodeURIComponent(name)}/captures`);
}

const experimentCaptureURL = (exp, id) =>
  `experiments/${encodeURIComponent(exp)}/captures/${encodeURIComponent(id)}`;

// POST /experiments/{exp}/captures: starts one capture of the experiment's
// traffic on every host, body { vlans, filter, snaplen, name, format };
// answers { capture, warnings }
export function startExperimentCapture(exp, body) {
  return axiosInstance.post(
    `experiments/${encodeURIComponent(exp)}/captures`,
    body,
  );
}

// GET /experiments/{exp}/captures/{id}: one experiment capture and its
// streams
export function getExperimentCapture(exp, id) {
  return axiosInstance.get(experimentCaptureURL(exp, id));
}

// DELETE /experiments/{exp}/captures/{id}: stops an experiment capture and
// answers with its last counters
export function stopExperimentCapture(exp, id) {
  return axiosInstance.delete(experimentCaptureURL(exp, id));
}

// POST /experiments/{name}/captureVlan: starts packet captures on every
// interface of the experiment's VMs on the VLAN alias, in the format
// (pcapng or pcap) when it is given
export function startVlanCaptures(name, vlan, format) {
  return axiosInstance.post(
    `experiments/${encodeURIComponent(name)}/captureVlan`,
    format ? { vlan, format } : { vlan },
  );
}

// POST /experiments/{name}/stopCaptureVlan: stops the packet captures on
// the VLAN alias
export function stopVlanCaptures(name, vlan) {
  return axiosInstance.post(
    `experiments/${encodeURIComponent(name)}/stopCaptureVlan`,
    { vlan },
  );
}
