// The server's logs.
import axiosInstance from '@/utils/axios.js';

// GET /logs from start to end (Dates), or to now when end is null
export function listLogs(start, end, signal) {
  const query =
    `logs?start=${start.toISOString()}` +
    (end === null ? '' : `&end=${end.toISOString()}`);
  return axiosInstance.get(query, { signal });
}
