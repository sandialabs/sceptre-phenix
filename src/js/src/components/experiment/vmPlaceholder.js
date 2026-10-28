// The picture a VM table, VNC tile or details card shows for a VM with no
// screenshot: an SVG image with a large state icon and label in the state's
// color, and the guest OS in a chip at the top left when the VM has an os_type.
// The colors are those of the State of Health graph.

import {
  faBan,
  faCircleStop,
  faDesktop,
  faEyeSlash,
  faGears,
  faHourglassHalf,
  faPlug,
  faRoute,
} from '@fortawesome/free-solid-svg-icons';

import { escapeHTML } from '@/utils/escapeHTML.js';
import { faLinux, faWindows } from '@/utils/icons.js';
import { osTypeInfo } from './osType.js';

// each picture's icon, color and label
export const PLACEHOLDERS = {
  external: { icon: faPlug, color: '#4fa8f0', label: 'EXTERNAL NODE' },
  delayed: { icon: faHourglassHalf, color: '#ffd479', label: 'DELAYED START' },
  notRunning: { icon: faCircleStop, color: '#ff6b5b', label: 'NOT RUNNING' },
  doNotBoot: { icon: faBan, color: '#b58cff', label: 'DO NOT BOOT' },
  notAvailable: {
    icon: faEyeSlash,
    color: '#9aa7b5',
    label: 'NO SCREENSHOT',
  },
  busy: { icon: faGears, color: '#9aa7b5', label: 'BUSY' },
};

const OS_ICONS = {
  linux: faLinux,
  windows: faWindows,
  router: faRoute,
  other: faDesktop,
};

const WIDTH = 600;
const HEIGHT = 450;
const FONT = "system-ui, -apple-system, 'Segoe UI', Roboto, sans-serif";

// An icon definition drawn to fit a size-pixel square at x, y.
function iconPath({ icon }, x, y, size, color) {
  const [width, height, , , path] = icon;
  const scale = size / Math.max(width, height);
  const dx = x + (size - width * scale) / 2;
  const dy = y + (size - height * scale) / 2;
  const d = Array.isArray(path) ? path.join(' ') : path;
  return `<path transform="translate(${dx} ${dy}) scale(${scale})" fill="${color}" d="${d}"/>`;
}

// the OS chip: its icon and label in a rounded box, at the top left
function osChip(os) {
  // a long os_type the schema does not name is cut short to fit
  const text = os.label.length > 16 ? `${os.label.slice(0, 15)}…` : os.label;
  const width = 84 + text.length * 19;
  return (
    `<rect x="20" y="20" width="${width}" height="60" rx="12" fill="#2b3138" stroke="#4a525c"/>` +
    iconPath({ icon: OS_ICONS[os.family].icon }, 34, 32, 36, '#e6e6e6') +
    `<text x="82" y="62" fill="#e6e6e6" font-size="32" font-family="${FONT}">${escapeHTML(text)}</text>`
  );
}

// The SVG of a placeholder kind; detail is a second, smaller line under the
// label, and osType the VM's os_type.
export function placeholderSvg(kind, { osType, detail } = {}) {
  const { icon, color, label } = PLACEHOLDERS[kind];
  const os = osTypeInfo(osType);
  const lines = [
    `<rect width="${WIDTH}" height="${HEIGHT}" fill="#16191d"/>`,
    `<rect x="4" y="4" width="${WIDTH - 8}" height="${HEIGHT - 8}" rx="14" fill="none" stroke="${color}" stroke-width="8"/>`,
    os ? osChip(os) : '',
    iconPath(icon, (WIDTH - 140) / 2, 105, 140, color),
    `<text x="${WIDTH / 2}" y="320" fill="${color}" font-size="46" font-weight="700" text-anchor="middle" font-family="${FONT}">${label}</text>`,
    detail
      ? `<text x="${WIDTH / 2}" y="370" fill="#c8ced6" font-size="30" text-anchor="middle" font-family="${FONT}">${escapeHTML(detail)}</text>`
      : '',
  ];
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${WIDTH} ${HEIGHT}" width="${WIDTH}" height="${HEIGHT}">${lines.join('')}</svg>`;
}

const cache = new Map();

// The placeholder as an image URL, the same string for the same picture so an
// <img> does not load it again.
export function placeholderImage(kind, options = {}) {
  const key = JSON.stringify([
    kind,
    options.osType ?? '',
    options.detail ?? '',
  ]);
  let url = cache.get(key);
  if (!url) {
    url = `data:image/svg+xml;charset=utf-8,${encodeURIComponent(placeholderSvg(kind, options))}`;
    cache.set(key, url);
  }
  return url;
}
