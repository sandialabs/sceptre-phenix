// The server's settings.
import axiosInstance from '@/utils/axios.js';

// GET /settings; config carries the request's signal and meta
export function getSettings(config) {
  return axiosInstance.get('settings', config);
}

// POST /settings, with no timeout
export function saveSettings(settings) {
  return axiosInstance.post('settings', settings, { timeout: 0 });
}

// GET /settings/password: the rules a new password must meet
export function getPasswordRequirements() {
  return axiosInstance.get('settings/password');
}

// GET /settings/timeout: when an idle user is signed out
export function getTimeoutSettings() {
  return axiosInstance.get('settings/timeout');
}
