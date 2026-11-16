// The minimega consoles the server runs for the Console page.
import axiosInstance from '@/utils/axios.js';

// POST /console?namespace=: starts a console, in the minimega namespace of
// the experiment named unless that is empty; the answer carries its pid and
// namespace
export function startConsole(namespace = '') {
  return axiosInstance.post(
    namespace
      ? `console?namespace=${encodeURIComponent(namespace)}`
      : 'console',
  );
}

// GET /console/{pid}: the console's pid and namespace; fails with 404 once
// the console has ended
export function getConsole(pid) {
  return axiosInstance.get(`console/${pid}`);
}

// DELETE /console/{pid}: ends the console
export function endConsole(pid) {
  return axiosInstance.delete(`console/${pid}`);
}

// POST /console/{pid}/size?cols=&rows=: the terminal's size
export function resizeConsole(pid, cols, rows) {
  return axiosInstance.post(`console/${pid}/size?cols=${cols}&rows=${rows}`);
}
