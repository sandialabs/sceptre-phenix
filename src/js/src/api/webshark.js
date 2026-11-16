// The captures an experiment opens in WebShark or streams to a local
// Wireshark. Experiment names are encoded into the path.
import axiosInstance from '@/utils/axios.js';

const expURL = (exp) => `experiments/${encodeURIComponent(exp)}`;

// POST /experiments/{exp}/webshark: readies a capture for WebShark
export function openWebSharkCapture(exp, body) {
  return axiosInstance.post(`${expURL(exp)}/webshark`, body);
}

// POST /experiments/{exp}/captureTokens: a token for streaming a capture
export function createCaptureToken(exp, body) {
  return axiosInstance.post(`${expURL(exp)}/captureTokens`, body);
}
