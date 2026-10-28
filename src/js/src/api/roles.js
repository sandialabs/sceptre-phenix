// The roles a user may be given.
import axiosInstance from '@/utils/axios.js';

// GET /roles; config carries the request's signal and meta
export function listRoles(config) {
  return axiosInstance.get('roles', config);
}
