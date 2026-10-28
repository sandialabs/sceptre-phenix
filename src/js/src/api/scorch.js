// An experiment's SCORCH runs: their pipelines, loops, components and
// terminals. Every read asks for JSON.
import axiosInstance from '@/utils/axios.js';

const pipelinesURL = (exp) => `experiments/${exp}/scorch/pipelines`;

// GET /experiments/{exp}/scorch/pipelines: every run's pipeline
export function listScorchPipelines(exp, signal) {
  return axiosInstance.get(pipelinesURL(exp), {
    signal,
    headers: { Accept: 'application/json' },
  });
}

// POST /experiments/{exp}/scorch/pipelines/{run}
export function startScorchRun(exp, run) {
  return axiosInstance.post(`${pipelinesURL(exp)}/${run}`);
}

// DELETE /experiments/{exp}/scorch/pipelines/{run}
export function stopScorchRun(exp, run) {
  return axiosInstance.delete(`${pipelinesURL(exp)}/${run}`);
}

// POST /experiments/{exp}/scorch/pipelines/{run}/clear: forgets the run's
// status
export function clearScorchRun(exp, run) {
  return axiosInstance.post(`${pipelinesURL(exp)}/${run}/clear`);
}

// POST /experiments/{exp}/scorch/pipelines/{run}/cleanup: runs only the
// run's cleanup stage
export function cleanupScorchRun(exp, run) {
  return axiosInstance.post(`${pipelinesURL(exp)}/${run}/cleanup`);
}

// GET /experiments/{exp}/scorch/pipelines/{run}/{loop}: one loop's pipeline
export function getScorchLoop(exp, run, loop) {
  return axiosInstance.get(`${pipelinesURL(exp)}/${run}/${loop}`, {
    headers: { Accept: 'application/json' },
  });
}

// GET /experiments/{exp}/scorch/terminals: the open component terminals
export function listScorchTerminals(exp) {
  return axiosInstance.get(`experiments/${exp}/scorch/terminals`, {
    headers: { Accept: 'application/json' },
  });
}

// GET /experiments/{exp}/scorch/components/{run}/{loop}/{stage}/{name}: a
// component's output or terminal
export function getScorchComponent({ exp, run, loop, stage, name }) {
  return axiosInstance.get(
    `experiments/${exp}/scorch/components/${run}/${loop}/${stage}/${name}`,
    { headers: { Accept: 'application/json' } },
  );
}

// POST to a terminal's exit path, which ends it
export function exitScorchTerminal(exitPath) {
  // exitPath is a server-provided path that already includes the base path;
  // clear baseURL so axios doesn't prepend it again
  return axiosInstance.post(exitPath, null, { baseURL: '' });
}
