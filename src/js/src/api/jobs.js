// The server's background jobs (see utils/jobs.js).
import axiosInstance from '@/utils/axios.js';

// GET /jobs: every job the user may see
export function listJobs() {
  return axiosInstance.get('jobs');
}

// GET /jobs/{id}: the whole job, with every item's result
export function getJob(id) {
  return axiosInstance.get(`jobs/${id}`);
}

// POST /jobs/{id}/cancel
export function cancelJob(id) {
  return axiosInstance.post(`jobs/${id}/cancel`);
}

// DELETE /jobs/{id}: forgets a finished job
export function deleteJob(id) {
  return axiosInstance.delete(`jobs/${id}`);
}
