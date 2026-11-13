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

// GET /logs/files: the log files to download, and whether each can be
export function listLogFiles(signal) {
  return axiosInstance.get('logs/files', { signal });
}
