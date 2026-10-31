// Display labels of the built-in icon keys.
//
// The stored keys stay lowercase (documents, templates and the server
// registry use them). The UI shows these labels, in each vendor's own
// spelling, wherever it names a built-in icon.

/** Labels of the built-in icon keys, in the order the UI lists them. */
const ICON_LABELS = {
  server: 'Server',
  desktop: 'Desktop',
  laptop: 'Laptop',
  smartphone: 'Smartphone',
  printer: 'Printer',
  ntp: 'NTP Server',
  router: 'Router',
  switch: 'Switch',
  firewall: 'Firewall',
  vlan: 'VLAN',
  container: 'Container',
  external: 'External device',
  plc: 'PLC',
  rtu: 'RTU',
  rtac: 'RTAC',
  relay: 'Relay',
  transmission: 'Transmission',
  distribution: 'Distribution',
  'cell-tower': 'Cell Tower',
  linux: 'Linux',
  centos: 'CentOS',
  redhat: 'Red Hat',
  ubuntu: 'Ubuntu',
  fedora: 'Fedora',
  opensuse: 'openSUSE',
  alpine: 'Alpine',
  kali: 'Kali Linux',
  bsd: 'BSD',
  windows: 'Windows',
  apple: 'Apple',
  android: 'Android',
  terminal: 'Terminal',
  pfsense: 'pfSense',
  wireshark: 'Wireshark',
  zeek: 'Zeek',
  hacker: 'Hacker',
  blackhat: 'Black Hat',
  human: 'Human',
  robot: 'Robot',
  lizard: 'Lizard',
  fox: 'Fox',
  phenix: 'phēnix',
  minimega: 'minimega',
};

/** The built-in icon keys in the order the UI lists them. */
export const ICON_ORDER = Object.keys(ICON_LABELS);

/**
 * @param {string} key a built-in icon key
 * @returns {string} its display label, or the key itself when it has none
 */
export function iconLabel(key) {
  return Object.hasOwn(ICON_LABELS, key) ? ICON_LABELS[key] : key;
}
