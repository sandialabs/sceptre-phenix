// The server's logs.
import axiosInstance from '@/utils/axios.js';

// GET /logs from start to end (Dates), or to now when end is null; config
// carries the request's signal and meta
export function listLogs(start, end, config) {
  const query =
    `logs?start=${start.toISOString()}` +
    (end === null ? '' : `&end=${end.toISOString()}`);
  return axiosInstance.get(query, config);
}
