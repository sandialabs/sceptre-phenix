// WebShark, the Wireshark-in-the-browser the phēnix server serves under
// /webshark/ when it is installed: which files it opens, what a role needs to
// use it, how the WebShark page asks the server for a capture, and the
// command that streams a live capture into a local Wireshark.
import axiosInstance from '@/utils/axios.js';
import { roleAllowed } from '@/utils/rbac.js';

// the feature GET /features lists when the server has WebShark installed
const WEBSHARK_FEATURE = 'webshark';

// packet capture files, optionally gzipped
const CAPTURE_FILE = /\.(pcap|pcapng|cap)(\.gz)?$/i;

export const isCaptureFile = (name) => CAPTURE_FILE.test(name ?? '');

export const webSharkInstalled = (features) =>
  (features ?? []).includes(WEBSHARK_FEATURE);

// Saved capture files need experiments/files get, running captures
// vms/captures list; a role with neither has nothing to open.
export const canUseWebShark = () =>
  roleAllowed('experiments/files', 'get') ||
  roleAllowed('vms/captures', 'list');

// WebShark's page for a capture, under the UI's base path like the API: the
// server moves the /webshark/ that WebShark's build hardcodes under it too.
// `embed` drops WebShark's own chrome for the frame on the WebShark page;
// without it the page is the full WebShark UI.
export function webSharkURL(capture, { embed = true } = {}) {
  const url =
    `${import.meta.env.BASE_URL}webshark/embed` +
    `?capture=${encodeURIComponent(capture)}`;
  return embed ? `${url}&embed=1` : url;
}

// What the WebShark page can open: a saved capture file or a running
// capture. `key` identifies it in the capture picker, `body` is what
// POST /experiments/{exp}/webshark takes, and `query` is how the page's URL
// names it.
export const fileTarget = (path, size = null) => ({
  key: `file:${path}`,
  live: false,
  path,
  size,
  label: path,
  body: { path },
  query: { file: path },
});

// `network` is the interface's VLAN alias, when known
export const liveTarget = (vm, iface, network = null) => ({
  key: `live:${iface}:${vm}`,
  live: true,
  vm,
  iface,
  label: `${vm} · IF${iface}${network ? ` (${network})` : ''}`,
  body: { vm, interface: iface },
  query: { vm, iface: String(iface) },
});

// a query value, which the router gives as an array when repeated
const one = (value) => (Array.isArray(value) ? value[0] : value);

// The experiment and target a WebShark page URL names:
// ?exp=<name>&file=<path> or ?exp=<name>&vm=<vm>&iface=<index>
export function queryTarget(query) {
  const exp = one(query?.exp) || null;
  if (!exp) return { exp, target: null };

  const file = one(query.file);
  if (file) return { exp, target: fileTarget(file) };

  const vm = one(query.vm);
  const iface = one(query.iface);
  if (vm && /^\d+$/.test(iface ?? '')) {
    return { exp, target: liveTarget(vm, Number(iface)) };
  }
  return { exp, target: null };
}

// POST /experiments/{exp}/webshark: has the server ready a target's capture
// for WebShark. Resolves to { capture, live, title }; `capture` names it in
// webSharkURL.
export async function openInWebShark(exp, target) {
  const resp = await axiosInstance.post(
    `experiments/${encodeURIComponent(exp)}/webshark`,
    target.body,
  );
  return resp.data;
}

// Why a capture could not be opened, for the page to show in place of it.
// The server answers with a plain-text reason, which the error notification
// shows in full.
export function openFailure(err) {
  switch (err?.response?.status) {
    case 400:
      return 'The server did not accept the request to open it.';
    case 403:
      return "You don't have permission to open it.";
    case 404:
      return 'It was not found, or is no longer being captured.';
    case 501:
      return 'WebShark is not installed on this phēnix server.';
    default:
      return 'The server could not open it.';
  }
}

// quotes a string for a POSIX shell
const shellQuote = (s) => `'${String(s).replace(/'/g, `'\\''`)}'`;

// A running capture's pcap stream, as an absolute URL for tools outside the
// browser. The API sits under the UI's base path, as axios.js builds it.
export function captureStreamURL(exp, vm, iface, origin) {
  const base = `${import.meta.env.BASE_URL}api/v1/`;
  return (
    `${origin}${base}experiments/${encodeURIComponent(exp)}` +
    `/vms/${encodeURIComponent(vm)}/captures/${iface}/stream`
  );
}

// The token a command outside the browser signs in with: none from a UI
// built without sign-in, whose token is a placeholder (see router.js).
export function signInToken(token) {
  const auth = import.meta.env.VITE_AUTH;
  return auth && auth !== 'disabled' ? token || null : null;
}

// A shell command that streams a capture into a local Wireshark, signed in
// as the current user when there is a token. curl stops with an error,
// rather than passing Wireshark an error or sign-in page, when phēnix
// refuses the request or a proxy in front of it redirects it.
export function wiresharkCommand(url, token) {
  const auth = token
    ? `-H ${shellQuote(`X-Phenix-Auth-Token: Bearer ${token}`)} `
    : '';
  return (
    `curl -fsSN --max-redirs 0 -L ${auth}${shellQuote(url)}` +
    ' | wireshark -k -i -'
  );
}

// The CLI command that streams the same capture on the phēnix server.
export function captureStreamCLI(exp, vm, iface) {
  return (
    `phenix vm capture stream ${shellQuote(exp)} ${shellQuote(vm)} ${iface}` +
    ' | wireshark -k -i -'
  );
}
