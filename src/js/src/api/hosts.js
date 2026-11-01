// The cluster's hosts.
import axiosInstance from '@/utils/axios.js';

// GET /hosts; config carries the request's signal and meta
export function listHosts(config) {
  return axiosInstance.get('hosts', config);
}
