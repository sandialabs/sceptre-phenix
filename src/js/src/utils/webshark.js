// WebShark, served under /webshark/ when installed: the files it opens, what a
// role needs, how the page asks for a capture, and the stream command.
import * as webSharkApi from '@/api/webshark.js';
import { roleAllowed } from '@/utils/rbac.js';
import { shellQuote } from '@/utils/shell.js';

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

// WebShark's page for a capture, under the UI's base path like the API.
// `embed` drops WebShark's own chrome for the frame on the WebShark page.
export function webSharkURL(capture, { embed = true } = {}) {
  const url =
    `${import.meta.env.BASE_URL}webshark/embed` +
    `?capture=${encodeURIComponent(capture)}`;
  return embed ? `${url}&embed=1` : url;
}

// A saved capture file or a running capture: `key` names it in the picker,
// `body` is the POST /experiments/{exp}/webshark body, `query` the URL's.
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

// POST /experiments/{exp}/webshark: readies a target's capture for WebShark.
// Resolves to { capture, live, title }; `capture` names it in webSharkURL.
export async function openInWebShark(exp, target) {
  const resp = await webSharkApi.openWebSharkCapture(exp, target.body);
  return resp.data;
}

// Why a capture could not be opened, for the page to show in place of it;
// the error notification shows the server's reason in full.
export function openFailure(err) {
  switch (err?.response?.status) {
    case 400:
      return 'The server did not accept the request to open it.';
    case 403:
      return 'You do not have permission to open it.';
    case 404:
      return 'The server did not find it, or its capture stopped.';
    case 501:
      return 'WebShark is not installed on this phēnix server.';
    default:
      return 'The server could not open it.';
  }
}

// POST /experiments/{exp}/captureTokens: a 5-minute stream token for a command
// outside the browser. Resolves to { id, token, expires, useBy, urls }.
export async function mintCaptureStream(exp, vm, iface) {
  const resp = await webSharkApi.createCaptureToken(exp, {
    vm,
    interface: iface,
    operations: ['stream'],
  });
  return resp.data;
}

// An absolute URL, for tools outside the browser, of a path the server gives
// under the UI's base path.
export const capabilityURL = (rel, origin) =>
  `${origin}${import.meta.env.BASE_URL}${rel}`;

// A shell command that streams url, whose token is its only credential, into
// Wireshark; curl fails rather than pass on an error page or a redirect.
export function wiresharkCommand(url) {
  return (
    `curl -fsSN --max-redirs 0 -L ${shellQuote(url)}` + ' | wireshark -k -i -'
  );
}

// The CLI command that streams the same capture on the phēnix server.
export function captureStreamCLI(exp, vm, iface) {
  return (
    `phenix vm capture stream ${shellQuote(exp)} ${shellQuote(vm)} ${iface}` +
    ' | wireshark -k -i -'
  );
}
