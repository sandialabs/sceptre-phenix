// Experiments and what runs across one: its apps, captures and health.
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

// POST /experiments/{name}/stop
export function stopExperiment(name) {
  return axiosInstance.post('experiments/' + name + '/stop');
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

// GET /experiments/{name}/captures, the name encoded: the packet captures
// running on the experiment's VMs
export function listExperimentCaptures(name) {
  return axiosInstance.get(`experiments/${encodeURIComponent(name)}/captures`);
}
