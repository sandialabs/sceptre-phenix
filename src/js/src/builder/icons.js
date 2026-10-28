// Custom icons, which mirror phenix/types/builder customicons.go. The checks
// here are the server's checks, except those that need a PNG decoder. Only an
// <img> draws an icon, and only from a PNG data URL (see iconSrc).

import { sha256Hex } from './digest.js';
import { isPlainObject } from './values.js';

// The form of an icon name (IconNameProblem in customicons.go): 1 to 64
// letters, digits, "_", "@", "." and "-", the characters of a phenix config
// name. A name is also neither "." nor ".." (see isIconName).
export const ICON_NAME = /^[A-Za-z0-9_@.-]{1,64}$/;

// What a name must be, as a field's hint says it.
export const ICON_NAME_HINT =
  'Use 1 to 64 letters, digits, _, @, . or -, and not . or .. alone.';

// Limits on an icon and on the icons of one document (MaxIconPixels,
// MaxIconBytes, MaxIconNameBytes and MaxDocumentIcons in customicons.go):
// the pixels on each side, the bytes of the PNG, the bytes of the name, and
// how many icons a document can carry.
export const MAX_ICON_PIXELS = 96;
export const MAX_ICON_BYTES = 40960;
export const MAX_ICON_NAME_BYTES = 64;
export const MAX_DOCUMENT_ICONS = 50;

// The length of the base64 text of the largest icon.
export const MAX_ICON_DATA_LENGTH = Math.ceil(MAX_ICON_BYTES / 3) * 4;

// The largest image file from which the editor reads to make an icon, and
// the largest PNG that the server accepts for one (MaxIconUploadBytes in
// api/builder icons.go). The server then stores it within MAX_ICON_BYTES.
export const MAX_ICON_FILE_BYTES = 5 * 1024 * 1024;
export const MAX_ICON_UPLOAD_BYTES = 65536;

// Standard base64 with padding and nothing else: no line breaks, no
// URL-safe characters.
const BASE64 =
  /^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/;

const BASE64_ALPHABET =
  'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/';

// The value of each base64 character, by its character code.
const BASE64_VALUES = new Uint8Array(128);

[...BASE64_ALPHABET].forEach((character, value) => {
  BASE64_VALUES[character.charCodeAt(0)] = value;
});

/** Whether text is an icon name (IconNameProblem in customicons.go). */
export function isIconName(name) {
  return (
    typeof name === 'string' &&
    ICON_NAME.test(name) &&
    name !== '.' &&
    name !== '..'
  );
}

/**
 * The bytes of an icon's data (decodeIconData in customicons.go), or null.
 * Strict base64 makes one icon have one text. The length check comes first, so
 * data far past the limit costs nothing.
 */
export function decodeIconData(text) {
  if (
    typeof text !== 'string' ||
    text === '' ||
    text.length > MAX_ICON_DATA_LENGTH ||
    !BASE64.test(text)
  ) {
    return null;
  }

  const padding = text.endsWith('==') ? 2 : text.endsWith('=') ? 1 : 0;
  const bytes = new Uint8Array((text.length / 4) * 3 - padding);
  const value = (index) => BASE64_VALUES[text.charCodeAt(index)];
  const last = text.length - padding - 1;

  // The bits of the last character that no byte takes.
  const spare = padding === 2 ? 0x0f : padding === 1 ? 0x03 : 0;

  if ((value(last) & spare) !== 0) {
    return null;
  }

  for (let from = 0, to = 0; from < text.length; from += 4) {
    const group =
      (value(from) << 18) |
      (value(from + 1) << 12) |
      ((from + 2 <= last ? value(from + 2) : 0) << 6) |
      (from + 3 <= last ? value(from + 3) : 0);

    bytes[to++] = group >> 16;

    if (to < bytes.length) {
      bytes[to++] = (group >> 8) & 0xff;
    }

    if (to < bytes.length) {
      bytes[to++] = group & 0xff;
    }
  }

  return bytes.length > 0 && bytes.length <= MAX_ICON_BYTES ? bytes : null;
}

// What starts every PNG file. It is what makes bytes a PNG here: no file
// name, extension or declared type is ever read.
const PNG_SIGNATURE = [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a];

// What a PNG chunk takes besides its data: the length of the data, the
// chunk's type and a checksum, four bytes each. The data of the header
// chunk is 13 bytes, starting with the width and the height.
const CHUNK_OVERHEAD = 12;
const HEADER_BYTES = 13;

// The chunks an icon may hold: the header, a palette and its transparency,
// the pixels and the end. Text, metadata, color profiles and animation
// frames have no use in an icon.
const HEADER = 'IHDR';
const END = 'IEND';
const OTHER_CHUNKS = ['PLTE', 'tRNS', 'IDAT'];

// The color types whose pixels use a palette, and those that use a
// transparency chunk (colorTypeUses in customicons.go). A palette applies
// only where the pixels are palette indexes (3). Transparency applies there,
// and where they are grayscale (0) or color (2) without alpha. The color
// type is the tenth byte of the header's data.
const COLOR_TYPE_AT = 9;
const COLOR_TYPES_USING = { PLTE: [3], tRNS: [0, 2, 3] };

const CUT_SHORT = 'the PNG is not complete';

/**
 * Why bytes are not a PNG icon, in the server's words (ValidateIconPNG in
 * customicons.go), or ''. Nothing is decoded: the size and color type come from
 * the header.
 */
export function iconPNGProblem(bytes) {
  if (bytes.length === 0) {
    return 'the image is empty';
  }

  if (bytes.length > MAX_ICON_BYTES) {
    return `the image is ${bytes.length} bytes: the limit is ${MAX_ICON_BYTES}`;
  }

  if (PNG_SIGNATURE.some((byte, index) => bytes[index] !== byte)) {
    return 'the image is not a PNG';
  }

  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  let offset = PNG_SIGNATURE.length;
  let first = true;
  let ended = false;
  let colorType = 0;

  while (offset < bytes.length) {
    if (ended) {
      return 'the PNG has data after its end';
    }

    if (bytes.length - offset < CHUNK_OVERHEAD) {
      return CUT_SHORT;
    }

    const length = view.getUint32(offset);

    if (length > bytes.length - offset - CHUNK_OVERHEAD) {
      return CUT_SHORT;
    }

    const kind = String.fromCharCode(...bytes.subarray(offset + 4, offset + 8));
    const data = offset + 8;

    offset += CHUNK_OVERHEAD + length;

    if (first) {
      if (kind !== HEADER || length !== HEADER_BYTES) {
        return 'the PNG does not start with its header';
      }

      const width = view.getUint32(data);
      const height = view.getUint32(data + 4);

      if (
        width < 1 ||
        width > MAX_ICON_PIXELS ||
        height < 1 ||
        height > MAX_ICON_PIXELS
      ) {
        return `icon is ${width} x ${height} pixels: the limit is ${MAX_ICON_PIXELS} x ${MAX_ICON_PIXELS}`;
      }

      colorType = bytes[data + COLOR_TYPE_AT];
      first = false;
    } else if (kind === HEADER) {
      return 'the PNG has a second header';
    } else if (kind === END) {
      if (length !== 0) {
        return 'the PNG does not end as a PNG does';
      }

      ended = true;
    } else if (!OTHER_CHUNKS.includes(kind)) {
      return `the PNG holds a ${JSON.stringify(kind)} chunk: an icon can hold only IHDR, PLTE, tRNS, IDAT and IEND chunks`;
    } else if (
      Object.hasOwn(COLOR_TYPES_USING, kind) &&
      !COLOR_TYPES_USING[kind].includes(colorType)
    ) {
      return `the PNG holds a ${JSON.stringify(kind)} chunk, which its color type does not use`;
    }
  }

  return ended ? '' : CUT_SHORT;
}

/** The standard base64 text, with padding, of bytes. */
export function encodeIconData(bytes) {
  // In steps, so a large image is never one call's arguments.
  const STEP = 0x8000;
  let binary = '';

  for (let from = 0; from < bytes.length; from += STEP) {
    binary += String.fromCharCode(...bytes.subarray(from, from + STEP));
  }

  return btoa(binary);
}

// What the address of every icon that is drawn starts with. It is a
// constant: no part of it comes from a document or a file.
const SRC_PREFIX = 'data:image/png;base64,';

// Whether text is what an icon's data may be: strict base64, within the
// length of the largest icon.
function isIconData(text) {
  return (
    typeof text === 'string' &&
    text !== '' &&
    text.length <= MAX_ICON_DATA_LENGTH &&
    BASE64.test(text)
  );
}

/**
 * Whether an address is a base64 PNG data URL, as iconSrc makes it. BuilderIcon
 * gives an <img> no other address, so no input can make it load a URL of
 * another scheme or type.
 */
export function isIconSrc(src) {
  return (
    typeof src === 'string' &&
    src.startsWith(SRC_PREFIX) &&
    isIconData(src.slice(SRC_PREFIX.length))
  );
}

// The address of a transparent PNG of one pixel. A library icon whose image
// is not read yet is drawn from it, so that its node keeps its square and
// never shows another icon in the meantime.
const PENDING_ICON_SRC = `${SRC_PREFIX}iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAAC0lEQVR42mNgAAIAAAUAAen63NgAAAAASUVORK5CYII=`;

// The addresses made last, by icon name, each with the data that it was made
// from ('' for data that gives no address). An address is tens of kilobytes
// of text, and every node that shows the icon gets it on every edit. Thus
// the same icon gets the same string, which is compared, and not checked or
// made again.
const sources = new Map();
const MAX_SOURCES = 256;

// Whether data is base64 of a PNG that the Builder accepts as an icon, as
// far as the editor checks (see iconPNGProblem). The browser gets nothing
// else to decode, so no image larger than an icon, and no image of another
// format.
function isIconImage(data) {
  if (!isIconData(data)) {
    return false;
  }

  const bytes = decodeIconData(data);

  return bytes !== null && iconPNGProblem(bytes) === '';
}

/**
 * The icon that a name names: a document's copy, or else the library's icon. A
 * library icon whose image is not read yet has `pending` and no data.
 */
export function resolveIcon(name, icons, library = null) {
  if (!isIconName(name)) {
    return null;
  }

  if (isPlainObject(icons) && Object.hasOwn(icons, name)) {
    return isPlainObject(icons[name]) ? icons[name] : null;
  }

  return library?.lookup?.(name) || null;
}

/**
 * The data URL from which an <img> draws a custom icon, or '' when the name
 * resolves to no valid PNG. A pending library icon gives a transparent PNG.
 */
export function iconSrc(name, icons, library = null) {
  const icon = resolveIcon(name, icons, library);
  const data = icon?.data;

  if (typeof data !== 'string') {
    return icon?.pending ? PENDING_ICON_SRC : '';
  }

  const built = sources.get(name);

  if (built && built.data === data) {
    return built.src;
  }

  const src = isIconImage(data) ? SRC_PREFIX + data : '';

  if (sources.size >= MAX_SOURCES) {
    sources.clear();
  }

  sources.set(name, { data, src });

  return src;
}

/**
 * The library's icons by each lowercase name and alias, as the server matches
 * them.
 */
export function indexIcons(icons) {
  const index = new Map();

  for (const icon of icons || []) {
    if (!isIconName(icon?.name)) {
      continue;
    }

    index.set(icon.name.toLowerCase(), icon);

    for (const alias of Array.isArray(icon.aliases) ? icon.aliases : []) {
      if (isIconName(alias) && !index.has(alias.toLowerCase())) {
        index.set(alias.toLowerCase(), icon);
      }
    }
  }

  return index;
}

// The IDs of the images hashed last, by their data. A draft compares its
// copies with the library on each edit, so the same copy is not hashed again.
const imageIds = new Map();
const MAX_IMAGE_IDS = 256;

/**
 * The ID of an icon's image (IconID in customicons.go): "sha256:" and the
 * lowercase hex SHA-256 of the PNG. '' for data that is not an icon's.
 */
export function iconImageId(data) {
  if (imageIds.has(data)) {
    return imageIds.get(data);
  }

  const bytes = decodeIconData(data);
  const id = bytes ? `sha256:${sha256Hex(bytes)}` : '';

  if (imageIds.size >= MAX_IMAGE_IDS) {
    imageIds.clear();
  }

  imageIds.set(data, id);

  return id;
}

/**
 * Whether a library icon holds a copy's image: the same data, or the same image
 * ID while the image is not read.
 */
export function holdsImage(icon, data) {
  if (!icon || typeof data !== 'string') {
    return false;
  }

  if (typeof icon.data === 'string') {
    return icon.data === data;
  }

  return typeof icon.id === 'string' && icon.id === iconImageId(data);
}

/**
 * The size of an icon from its data, or null for data that is not a valid PNG.
 */
export function iconDetails(data) {
  const bytes = decodeIconData(data);

  if (!bytes || iconPNGProblem(bytes) !== '') {
    return null;
  }

  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  const at = PNG_SIGNATURE.length + 8;

  return {
    width: view.getUint32(at),
    height: view.getUint32(at + 4),
    bytes: bytes.length,
  };
}

const KIB = 1024;

function kibibytes(bytes) {
  return `${(bytes / KIB).toFixed(1)} KiB`;
}

/** Icons in the order the server lists the library: by name, ignoring case. */
export function sortIcons(icons) {
  const key = (icon) => String(icon?.name || '');

  return [...icons].sort((a, b) => {
    const [first, second] = [key(a).toLowerCase(), key(b).toLowerCase()];

    if (first !== second) {
      return first < second ? -1 : 1;
    }

    return key(a) < key(b) ? -1 : key(a) > key(b) ? 1 : 0;
  });
}

// The source that the icon library shows for an icon that the server added
// from its template files. No user owns such an icon, and the library lists
// it with no owner (ServerIconOwner in api/builder/templatefiles.go).
export const SERVER_ICON_OWNER = 'Server';

/** The source of a library icon: "Uploaded by <user>", or SERVER_ICON_OWNER. */
export function iconOwnerText(icon) {
  return icon?.owner ? `Uploaded by ${icon.owner}` : SERVER_ICON_OWNER;
}

/**
 * What a list says about an icon's size, such as "96 × 64 pixels, 5.0 KiB", or
 * ''.
 */
export function iconSizeText(icon) {
  const details =
    icon?.width > 0 && icon.height > 0 && icon.bytes > 0
      ? icon
      : iconDetails(icon?.data);

  return details
    ? `${details.width} × ${details.height} pixels, ${kibibytes(details.bytes)}`
    : '';
}

/**
 * "The library holds 3 of 2000 icons.", counting `builtin` icons too, or ''
 * while the library is not read.
 */
export function usageText(library, builtin = 0) {
  if (!library || !(library.maxIcons > 0)) {
    return '';
  }

  const held =
    (Array.isArray(library.icons) ? library.icons.length : 0) +
    Math.max(0, Math.trunc(builtin) || 0);

  return `The library holds ${held} of ${library.maxIcons} icons.`;
}

// The name of the icon a payload names: a device's, a group's, an icon
// node's or a template device's `icon`.
function refOf(payload) {
  const name = payload?.icon;

  return name === undefined || name === null || name === '' ? null : name;
}

// The payload of an icon node, which names its custom icon as a device does.
function drawnIconOf(node) {
  return node?.kind === 'icon' ? node.icon : undefined;
}

/**
 * The custom icon names that a document uses, in the order they are first
 * named.
 */
export function iconRefs(doc) {
  const refs = new Set();
  const add = (payload) => {
    const name = refOf(payload);

    if (name !== null) {
      refs.add(name);
    }
  };

  for (const node of doc?.nodes || []) {
    add(node?.device);
    add(node?.group);
    add(drawnIconOf(node));
  }

  for (const template of doc?.templates || []) {
    add(template?.device);
  }

  for (const layer of Array.isArray(doc?.layers) ? doc.layers : []) {
    add(layer);
  }

  return refs;
}

/**
 * Drops icon copies that nothing names, and copies that the library holds with
 * the same bytes. Every store commit calls this. It never adds a copy.
 */
export function settleIcons(doc, library = null) {
  if (doc?.icons === undefined) {
    return doc;
  }

  if (!isPlainObject(doc.icons)) {
    // null is none, as the server decodes it. The validator reports any
    // other value.
    if (doc.icons !== null) {
      return doc;
    }

    const next = { ...doc };

    delete next.icons;

    return next;
  }

  const refs = iconRefs(doc);
  const kept = {};
  let changed = false;

  for (const [name, entry] of Object.entries(doc.icons)) {
    const shared = refs.has(name) ? library?.lookup?.(name) : null;

    if (!refs.has(name) || holdsImage(shared, entry?.data)) {
      changed = true;
    } else {
      kept[name] = entry;
    }
  }

  if (!changed && Object.keys(kept).length > 0) {
    return doc;
  }

  const next = { ...doc };

  delete next.icons;

  if (Object.keys(kept).length > 0) {
    next.icons = kept;
  }

  return next;
}

/**
 * A copy of a document that carries every custom icon that it uses, as a
 * downloaded file must, at most MAX_DOCUMENT_ICONS. `missing` names what
 * nothing resolves, and `left` names what the limit leaves out.
 */
export function embedIcons(doc, library = null) {
  const icons = {};
  const missing = [];
  const left = [];

  for (const name of iconRefs(doc)) {
    const icon = resolveIcon(name, doc?.icons, library);

    if (!icon || typeof icon.data !== 'string') {
      missing.push(name);
    } else if (Object.keys(icons).length >= MAX_DOCUMENT_ICONS) {
      left.push(name);
    } else {
      icons[name] = { data: icon.data };
    }
  }

  const next = { ...doc };

  delete next.icons;

  if (Object.keys(icons).length > 0) {
    next.icons = icons;
  }

  return { doc: next, missing, left };
}

/**
 * Saves an icon as "<name>.png" with the stored bytes, not drawn again. Returns
 * the file name, or '' when the name or data is not an icon's.
 */
export function saveIconFile(icon, { saveAs }) {
  const bytes = decodeIconData(icon?.data);

  if (!bytes || !isIconName(icon?.name)) {
    return '';
  }

  const fileName = `${icon.name}.png`;

  saveAs(new Blob([bytes], { type: 'image/png' }), fileName);

  return fileName;
}

// What the upload dialog says of a file it cannot make an icon of.
export const ICON_FILE_TOO_LARGE = 'The image file is larger than 5 MiB.';
export const ICON_FILE_NOT_IMAGE =
  'This file is not an image the browser can read. Use a PNG, JPEG, GIF, WebP or SVG file.';
export const ICON_FILE_NOT_CONVERTED =
  'The Builder could not convert this image. Save it as a PNG, and then upload it again.';

/**
 * Why a file could not be made into an icon. Its message is one of the
 * three above, written to be shown.
 */
export class IconFileError extends Error {
  constructor(message) {
    super(message);
    this.name = 'IconFileError';
  }
}

const SVG_TYPE = 'image/svg+xml';

/**
 * The icon name that an upload proposes from a file name, or '' when nothing is
 * left.
 */
export function iconNameFromFile(fileName) {
  const name = String(fileName ?? '')
    .replace(/\.[^.]*$/, '')
    .replace(/[^A-Za-z0-9_@.-]+/g, '-')
    .slice(0, MAX_ICON_NAME_BYTES)
    .replace(/^-+|-+$/g, '');

  return isIconName(name) ? name : '';
}

/**
 * The size at which an image is drawn to make an icon: inside MAX_ICON_PIXELS,
 * in its own proportions. A bitmap is never enlarged. A vector image is drawn
 * as large as an icon can be.
 */
export function iconCanvasSize(width, height, vector = false) {
  const from = {
    width: width > 0 ? width : MAX_ICON_PIXELS,
    height: height > 0 ? height : MAX_ICON_PIXELS,
  };
  const fit = Math.min(
    MAX_ICON_PIXELS / from.width,
    MAX_ICON_PIXELS / from.height,
  );
  const scale = vector ? fit : Math.min(1, fit);
  const side = (length) =>
    Math.min(MAX_ICON_PIXELS, Math.max(1, Math.round(length * scale)));

  return { width: side(from.width), height: side(from.height) };
}

// The browser's own image, canvas and URL functions, which a test replaces.
const BROWSER = {
  createObjectURL: (blob) => URL.createObjectURL(blob),
  revokeObjectURL: (url) => URL.revokeObjectURL(url),
  loadImage: async (url) => {
    const image = new Image();

    image.src = url;
    await image.decode();

    return image;
  },
  createCanvas: (width, height) => {
    const canvas = document.createElement('canvas');

    canvas.width = width;
    canvas.height = height;

    return canvas;
  },
  BlobCtor: globalThis.Blob,
};

function canvasBlob(canvas) {
  return new Promise((resolve, reject) => {
    try {
      canvas.toBlob(resolve, 'image/png');
    } catch (error) {
      reject(error);
    }
  });
}

/**
 * Makes a PNG icon of an image file, as the browser draws it into a canvas.
 * Throws IconFileError. The file loads only into a detached <img>, where SVG
 * scripts do nothing, and only the drawn pixels are kept.
 */
export async function rasterizeIcon(file, deps = {}) {
  const {
    createObjectURL,
    revokeObjectURL,
    loadImage,
    createCanvas,
    BlobCtor,
  } = { ...BROWSER, ...deps };

  if (!file) {
    throw new IconFileError(ICON_FILE_NOT_IMAGE);
  }

  if (file.size > MAX_ICON_FILE_BYTES) {
    throw new IconFileError(ICON_FILE_TOO_LARGE);
  }

  const vector =
    file.type === SVG_TYPE || (!file.type && /\.svg$/i.test(file.name || ''));
  // The browser reads a file with no type as an image by its content, except
  // an SVG, which it identifies only by its type.
  const source =
    vector && file.type !== SVG_TYPE
      ? new BlobCtor([file], { type: SVG_TYPE })
      : file;
  const url = createObjectURL(source);

  try {
    let image;

    try {
      image = await loadImage(url);
    } catch {
      throw new IconFileError(ICON_FILE_NOT_IMAGE);
    }

    let bytes;

    try {
      const { width, height } = iconCanvasSize(
        image.naturalWidth,
        image.naturalHeight,
        vector,
      );
      const canvas = createCanvas(width, height);
      const context = canvas.getContext('2d');

      context.imageSmoothingQuality = 'high';
      context.drawImage(image, 0, 0, width, height);

      // An image the browser loads but cannot draw, as one browser does an
      // SVG that states no size, leaves the canvas as it was: clear.
      const pixels = context.getImageData(0, 0, width, height).data;
      let drawn = false;

      for (let at = 3; at < pixels.length && !drawn; at += 4) {
        drawn = pixels[at] !== 0;
      }

      const blob = drawn ? await canvasBlob(canvas) : null;

      bytes = blob ? new Uint8Array(await blob.arrayBuffer()) : null;
    } catch {
      bytes = null;
    }

    if (
      !bytes ||
      bytes.length > MAX_ICON_UPLOAD_BYTES ||
      PNG_SIGNATURE.some((byte, index) => bytes[index] !== byte)
    ) {
      throw new IconFileError(ICON_FILE_NOT_CONVERTED);
    }

    return { name: iconNameFromFile(file.name), data: encodeIconData(bytes) };
  } finally {
    revokeObjectURL(url);
  }
}
