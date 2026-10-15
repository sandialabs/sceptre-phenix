// SVG and PNG exports of the State of Health graph as it is drawn: the
// current zoom level, the whole graph in view, plus a title and a legend.
import {
  SOH_ERROR_COLOR,
  STATUS_COLORS,
  STATUS_LABELS,
  VLAN_COLOR,
} from './graph.js';
import { escapeXml } from './xml.js';

// yyyymmdd-hhmmss in local time, for file names
function exportTimestamp(date) {
  const p = (n) => String(n).padStart(2, '0');
  return (
    `${date.getFullYear()}${p(date.getMonth() + 1)}${p(date.getDate())}-` +
    `${p(date.getHours())}${p(date.getMinutes())}${p(date.getSeconds())}`
  );
}

// A file name part without path separators or characters that some file
// systems refuse.
function safeFilePart(text) {
  const s = String(text ?? '')
    .replace(/[^A-Za-z0-9._-]+/g, '_')
    .replace(/^[._]+|_+$/g, '');
  return s || 'experiment';
}

export function exportFileName(experiment, layout, ext, date) {
  return `${safeFilePart(experiment)}-soh-${safeFilePart(layout)}-${exportTimestamp(date)}.${ext}`;
}

// The legend drawn under an exported graph.
export const LEGEND = [
  { label: 'VLAN segment', fill: VLAN_COLOR, vlan: true },
  ...Object.entries(STATUS_LABELS).map(([k, label]) => ({
    label,
    fill: STATUS_COLORS[k],
  })),
  { label: 'SOH errors', fill: 'none', stroke: SOH_ERROR_COLOR },
];

const HEAD = 56; // title and subtitle
const FOOT = 40; // legend
const PAD = 20;
const BACKGROUND = '#333';

// The area of the drawing that holds the graph, in the SVG's own units:
// the graph's bounding box moved through the zoom transform {k, x, y}.
function graphArea(bbox, transform) {
  const { k, x, y } = transform;
  return {
    x: bbox.x * k + x,
    y: bbox.y * k + y,
    width: bbox.width * k,
    height: bbox.height * k,
  };
}

// Puts the serialized graph (the zoom group's children, in graph
// coordinates) into a standalone SVG sized in CSS pixels:
//   graph      markup of the graph layer
//   defs       markup of the <defs> it references
//   bbox       the graph's bounding box in graph coordinates
//   transform  the zoom transform {k, x, y}
//   scale      CSS pixels per SVG unit on screen
// Returns { svg, width, height }.
export function composeSvg({
  graph,
  defs,
  bbox,
  transform,
  scale,
  title,
  subtitle,
}) {
  const area = graphArea(bbox, transform);
  const gw = Math.max(1, area.width * scale);
  const gh = Math.max(1, area.height * scale);
  // each entry: mark, gap, label (about 7 px per character at 12 px)
  const itemWidth = (item) => 30 + item.label.length * 7;
  const legendWidth = LEGEND.reduce((w, item) => w + itemWidth(item), 0);
  const width = Math.ceil(Math.max(gw + 2 * PAD, legendWidth + 2 * PAD, 480));
  const height = Math.ceil(gh + HEAD + FOOT + PAD);
  // screen transform: graph coordinates -> zoom -> on-screen units ->
  // pixels, shifted so the graph area starts under the title, centered
  const ox = (width - gw) / 2 - area.x * scale;
  const oy = HEAD - area.y * scale;
  const n = (v) => Math.round(v * 1000) / 1000;
  const k = transform.k * scale;
  const graphTransform = `translate(${n(ox + transform.x * scale)},${n(oy + transform.y * scale)}) scale(${n(k)})`;

  const ly = height - FOOT / 2;
  const lx0 = (width - legendWidth) / 2;
  const vlanIcon = /id="switch"/.test(defs);
  let lx = lx0;
  const legendMarkup = LEGEND.map((item) => {
    const x = lx + 8;
    lx += itemWidth(item);
    // the VLAN icon pattern when the graph's defs carry it
    const mark = item.vlan
      ? vlanIcon
        ? `<circle cx="${x}" cy="${ly}" r="5" fill="url(#switch)"/>`
        : `<rect x="${x - 6}" y="${ly - 6}" width="12" height="12" rx="2" fill="${item.fill}"/>`
      : `<circle cx="${x}" cy="${ly}" r="6" fill="${item.fill}" stroke="${item.stroke ?? 'whitesmoke'}" stroke-width="${item.stroke ? 2 : 0.8}"/>`;
    return `${mark}<text x="${x + 12}" y="${ly + 4}" font-size="12" fill="whitesmoke">${escapeXml(item.label)}</text>`;
  }).join('');

  const svg =
    `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" ` +
    `width="${width}" height="${height}" viewBox="0 0 ${width} ${height}" ` +
    `font-family="system-ui, -apple-system, 'Segoe UI', Roboto, sans-serif">` +
    `<title>${escapeXml(title)}</title>` +
    `<rect width="${width}" height="${height}" fill="${BACKGROUND}"/>` +
    (defs ? `<defs>${defs}</defs>` : '') +
    `<text x="${PAD}" y="26" font-size="18" font-weight="bold" fill="white">${escapeXml(title)}</text>` +
    `<text x="${PAD}" y="45" font-size="12" fill="#cfcfcf">${escapeXml(subtitle)}</text>` +
    `<g transform="${graphTransform}">${graph}</g>` +
    `<g class="legend">${legendMarkup}</g>` +
    `</svg>`;
  return { svg, width, height };
}

// Style properties that decide how the graph looks; copied from the
// computed style so the export does not depend on the page's CSS.
const STYLE_PROPS = [
  'display',
  'visibility',
  'opacity',
  'fill',
  'fill-opacity',
  'stroke',
  'stroke-width',
  'stroke-opacity',
  'stroke-dasharray',
  'stroke-linecap',
  'font-family',
  'font-size',
  'font-style',
  'font-weight',
  'text-anchor',
];

// url("https://host/page#id") -> url(#id): a computed paint server points at
// the page, which a standalone file does not have.
export function localUrls(value) {
  return String(value).replace(
    /url\(\s*["']?[^"')]*?(#[^"')]+)["']?\s*\)/g,
    'url($1)',
  );
}

function inlineStyles(live, copy) {
  const cs = getComputedStyle(live);
  const style = STYLE_PROPS.map((p) => {
    const v = cs.getPropertyValue(p);
    return v ? `${p}:${localUrls(v)}` : '';
  })
    .filter(Boolean)
    .join(';');
  copy.setAttribute('style', style);
  copy.removeAttribute('class');
  for (let i = 0; i < live.children.length; i++) {
    inlineStyles(live.children[i], copy.children[i]);
  }
}

async function dataUrl(url) {
  const blob = await (await fetch(url)).blob();
  return new Promise((resolve, reject) => {
    const r = new FileReader();
    r.onload = () => resolve(r.result);
    r.onerror = () => reject(r.error);
    r.readAsDataURL(blob);
  });
}

// Pattern images (the OS icons, the VLAN icon) are embedded, since a
// standalone SVG or a canvas cannot load them from the page.
async function inlineImages(root) {
  const XLINK = 'http://www.w3.org/1999/xlink';
  const cache = new Map();
  for (const img of root.querySelectorAll('image')) {
    const href = img.getAttribute('href') ?? img.getAttributeNS(XLINK, 'href');
    if (!href || href.startsWith('data:')) continue;
    if (!cache.has(href))
      cache.set(
        href,
        dataUrl(href).catch(() => null),
      );
    const data = await cache.get(href);
    if (data) {
      img.removeAttributeNS(XLINK, 'href');
      img.setAttribute('href', data);
    }
  }
}

// Serializes the graph in svgEl (drawn by the SOH view: a <defs> and one
// zoom group) as a standalone SVG. Returns { svg, width, height }.
export async function exportSvg(svgEl, { transform, title, subtitle }) {
  const layer = svgEl.querySelector(':scope > g');
  const bbox = layer.getBBox();
  const copy = layer.cloneNode(true);
  inlineStyles(layer, copy);
  const defsEl = svgEl.querySelector(':scope > defs');
  const defs = defsEl ? defsEl.cloneNode(true) : null;
  if (defs) await inlineImages(defs);

  const ser = new XMLSerializer();
  // children repeat the SVG namespace the outer element already declares
  const inner = (el) =>
    [...el.childNodes]
      .map((c) => ser.serializeToString(c))
      .join('')
      .replaceAll(' xmlns="http://www.w3.org/2000/svg"', '');
  const viewBox = svgEl.viewBox.baseVal;
  const scale = viewBox?.width ? svgEl.clientWidth / viewBox.width : 1;
  return composeSvg({
    graph: inner(copy),
    defs: defs ? inner(defs) : '',
    bbox: {
      x: bbox.x - 12,
      y: bbox.y - 12,
      width: bbox.width + 24,
      height: bbox.height + 24,
    },
    transform,
    scale,
    title,
    subtitle,
  });
}

// Browsers cap canvas size; keep the PNG within it.
const MAX_SIDE = 16384;
const MAX_AREA = 16384 * 16384 * 0.25;

export function pngScale(width, height, wanted) {
  let s = wanted;
  s = Math.min(s, MAX_SIDE / width, MAX_SIDE / height);
  s = Math.min(s, Math.sqrt(MAX_AREA / (width * height)));
  return Math.max(0.1, s);
}

// Renders an SVG string to a PNG blob at `scale` times its size.
export async function svgToPng(svg, width, height, scale) {
  const s = pngScale(width, height, scale);
  const img = new Image();
  img.src = 'data:image/svg+xml;charset=utf-8,' + encodeURIComponent(svg);
  await img.decode();
  const canvas = document.createElement('canvas');
  canvas.width = Math.round(width * s);
  canvas.height = Math.round(height * s);
  const ctx = canvas.getContext('2d');
  ctx.drawImage(img, 0, 0, canvas.width, canvas.height);
  return new Promise((resolve, reject) =>
    canvas.toBlob(
      (b) => (b ? resolve(b) : reject(new Error('could not encode the PNG'))),
      'image/png',
    ),
  );
}
