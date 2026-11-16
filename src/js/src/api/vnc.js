// The VNC session of a VM: what a console needs to connect to its screen.
import axiosInstance from '@/utils/axios.js';

/**
 * GET /experiments/{exp}/vms/{vm}/vnc, both names encoded. It answers the
 * session JSON: `experiment`, `vm`, `websocket` (relative to the API root),
 * `banners.top` and `banners.bottom` (`lines`, `backgroundColor`,
 * `textColor`), `display` (`vga`, `remoteResize`) and `clipboard.mode`
 * (`pasteAsKeys` or `bidirectional`).
 *
 * With `probe`, the server also dials the VNC endpoint of the VM. When it
 * cannot, it answers 502 with the code `vnc_unreachable` and `details.host`
 * and `details.port`. A VM that is not running gives 409.
 *
 * @param {string} exp the experiment name
 * @param {string} vm the VM name
 * @param {{ probe?: boolean }} [options]
 * @returns {Promise<import('axios').AxiosResponse>}
 */
export function getVncSession(exp, vm, { probe = false } = {}) {
  const e = encodeURIComponent(exp);
  const n = encodeURIComponent(vm);
  return axiosInstance.get(
    `experiments/${e}/vms/${n}/vnc`,
    probe ? { params: { probe: true } } : {},
  );
}
