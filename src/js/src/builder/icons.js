// Custom icons, mirroring phenix/types/builder customicons.go.
//
// A custom icon is a small PNG a document carries as base64 text, named by
// the SHA-256 of its bytes, so the document needs nothing else to show it.
// The checks here are the server's, but for those that need a decoder: the
// checksum of each chunk, that the pixels inflate, and that nothing follows
// them in their chunks. The server makes those when it stores the document.
//
// An icon is only ever drawn by an <img> whose address is a data URL of a
// PNG, built here from the base64 text of a PNG that passes those checks
// (see iconSrc). An image file of another format, SVG included, is drawn by
// the browser into a canvas and leaves it as a PNG (see rasterizeIcon):
// nothing of the file itself is stored, sent or put into the page.

import { sha256Hex } from './digest.js';
import { utf8Length } from './text.js';

// The form of an icon id (IconID in customicons.go): "sha256:" and the
// lowercase hex SHA-256 of the PNG bytes.
export const ICON_ID = /^sha256:[0-9a-f]{64}$/;

// Bounds on an icon and on the icons of one document (MaxIconPixels,
// MaxIconBytes, MaxIconNameBytes and MaxDocumentIcons in customicons.go):
// its pixels on a side, the bytes of its PNG, the UTF-8 bytes of its name,
// and how many a document may carry.
export const MAX_ICON_PIXELS = 96;
export const MAX_ICON_BYTES = 40960;
export const MAX_ICON_NAME_BYTES = 64;
export const MAX_DOCUMENT_ICONS = 50;

// The length of the base64 text of the largest icon.
export const MAX_ICON_DATA_LENGTH = Math.ceil(MAX_ICON_BYTES / 3) * 4;

// The largest image file the editor reads to make an icon of, and the
// largest PNG the server takes for one (MaxIconUploadBytes in api/builder
// icons.go), which it then stores within MAX_ICON_BYTES.
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

/**
 * The id of an icon (IconID in customicons.go).
 *
 * @param {Uint8Array} bytes the PNG
 * @returns {string} "sha256:" and the lowercase hex SHA-256 of the bytes
 */
export function iconId(bytes) {
  return `sha256:${sha256Hex(bytes)}`;
}

/**
 * The bytes of an icon's data (decodeIconData in customicons.go): strict
 * standard base64, with padding and without line breaks, of 1 to
 * MAX_ICON_BYTES bytes. Strict means the bits of the last character that no
 * byte takes are zero, so one icon has one text. The text is measured before
 * it is decoded, so data far past the limit costs nothing.
 *
 * @param {*} text
 * @returns {Uint8Array|null} null for anything else
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
// transparency chunk (colorTypeUses in customicons.go): a palette only
// where the pixels are palette indexes (3), and transparency there and
// where they are grayscale (0) or color (2) without alpha. The color type
// is the tenth byte of the header's data.
const COLOR_TYPE_AT = 9;
const COLOR_TYPES_USING = { PLTE: [3], tRNS: [0, 2, 3] };

const CUT_SHORT = 'the PNG is cut short';

/**
 * Why bytes are not a PNG the Builder accepts as an icon, in the server's
 * words (ValidateIconPNG in customicons.go), or '' when they are one as far
 * as the editor checks: at most MAX_ICON_BYTES long, 1 to MAX_ICON_PIXELS
 * pixels on each side, only the chunks IHDR, PLTE, tRNS, IDAT and IEND, a
 * PLTE or a tRNS only where its color type uses one, and nothing after the
 * IEND chunk. Nothing is decoded: the size and the color type are read from
 * the header.
 *
 * @param {Uint8Array} bytes
 * @returns {string}
 */
export function iconPNGProblem(bytes) {
  if (bytes.length === 0) {
    return 'the image is empty';
  }

  if (bytes.length > MAX_ICON_BYTES) {
    return `the image is ${bytes.length} bytes; the limit is ${MAX_ICON_BYTES}`;
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
        return `icon is ${width} x ${height} pixels; the limit is ${MAX_ICON_PIXELS} x ${MAX_ICON_PIXELS}`;
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
      return `the PNG holds a ${JSON.stringify(kind)} chunk; only IHDR, PLTE, tRNS, IDAT and IEND are allowed`;
    } else if (
      Object.hasOwn(COLOR_TYPES_USING, kind) &&
      !COLOR_TYPES_USING[kind].includes(colorType)
    ) {
      return `the PNG holds a ${JSON.stringify(kind)} chunk, which its color type does not use`;
    }
  }

  return ended ? '' : CUT_SHORT;
}

/**
 * The base64 text of bytes, as an icon's data holds them: standard base64
 * with padding.
 *
 * @param {Uint8Array} bytes
 * @returns {string}
 */
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
 * Whether an address is one a custom icon is drawn from: a data URL of a
 * PNG in base64, as iconSrc builds it, and nothing else. BuilderIcon gives
 * an <img> no other address, so no document, file or server response can
 * make it load a URL of another scheme or type.
 *
 * @param {*} src
 * @returns {boolean}
 */
export function isIconSrc(src) {
  return (
    typeof src === 'string' &&
    src.startsWith(SRC_PREFIX) &&
    isIconData(src.slice(SRC_PREFIX.length))
  );
}

// The addresses built last, by icon id, each with the data it was built
// from ('' for data no address is built from). An address is tens of
// kilobytes of text, and every node that shows the icon is given it on
// every edit: the same icon gets the same string, which is compared, and
// neither checked nor built, again.
const sources = new Map();
const MAX_SOURCES = 256;

// Whether data is base64 of a PNG the Builder accepts as an icon, as far as
// the editor checks (see iconPNGProblem): the browser is given nothing else
// to decode, so no image larger than an icon, and none of another format.
function isIconImage(data) {
  if (!isIconData(data)) {
    return false;
  }

  const bytes = decodeIconData(data);

  return bytes !== null && iconPNGProblem(bytes) === '';
}

/**
 * The address an <img> draws a custom icon from: "data:image/png;base64,"
 * and the icon's data, when `icons` has the icon and its data is base64 of
 * a PNG the Builder accepts.
 *
 * @param {string} id icon id
 * @param {object|null|undefined} icons by icon id, {name?, data}: a
 *   document's `icons`
 * @returns {string} '' for no icon, one `icons` lacks, or data that is not
 *   such a PNG
 */
export function iconSrc(id, icons) {
  if (
    typeof id !== 'string' ||
    !ICON_ID.test(id) ||
    !icons ||
    typeof icons !== 'object' ||
    !Object.hasOwn(icons, id)
  ) {
    return '';
  }

  const data = icons[id]?.data;
  const built = sources.get(id);

  if (built && built.data === data) {
    return built.src;
  }

  const src = isIconImage(data) ? SRC_PREFIX + data : '';

  if (sources.size >= MAX_SOURCES) {
    sources.clear();
  }

  sources.set(id, { data, src });

  return src;
}

/**
 * The size of an icon, read from its data, for a list that shows it.
 *
 * @param {string} data an icon's base64 text
 * @returns {{width: number, height: number, bytes: number}|null} null for
 *   data that is not a PNG the Builder accepts
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
const MIB = 1024 * 1024;

function kibibytes(bytes) {
  return `${(bytes / KIB).toFixed(1)} KiB`;
}

/**
 * Icons in the order the server lists a library: by name, whatever its
 * case, then by id.
 *
 * @param {{id: string, name?: string}[]} icons
 * @returns {object[]} a new list
 */
export function sortIcons(icons) {
  const key = (icon) => String(icon?.name || '').toLowerCase();

  return [...icons].sort((a, b) => {
    const [first, second] = [key(a), key(b)];

    if (first !== second) {
      return first < second ? -1 : 1;
    }

    return a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
  });
}

/**
 * What a list says of an icon's size: "96 × 64 pixels, 5.0 KiB". A
 * library's icon says its size itself; a document's has it in its data.
 *
 * @param {{width?: number, height?: number, bytes?: number, data?: string}} icon
 * @returns {string} '' for an icon whose size cannot be read
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
 * The heading of the user's icon library: how many icons it holds of how
 * many it may, and their bytes of those it may hold.
 *
 * @param {{icons: object[], maxIcons: number, maxBytes: number,
 *   usedBytes: number}|null} library as listIcons answers; null while it
 *   is not read
 * @returns {string} "My library (3 of 64, 20.0 KiB of 1 MiB)"
 */
export function libraryTitle(library) {
  if (!library) {
    return 'My library';
  }

  const { icons, maxIcons, maxBytes, usedBytes } = library;
  const most =
    maxBytes >= MIB && maxBytes % MIB === 0
      ? `${maxBytes / MIB} MiB`
      : kibibytes(maxBytes);

  return `My library (${icons.length} of ${maxIcons}, ${kibibytes(usedBytes)} of ${most})`;
}

function isControlCharacter(character) {
  const code = character.codePointAt(0);

  return code < 0x20 || code === 0x7f;
}

// Whether a name is one an icon may have (rule I3 of ValidateIcons).
function isIconName(name) {
  return (
    typeof name === 'string' &&
    utf8Length(name) <= MAX_ICON_NAME_BYTES &&
    ![...name].some(isControlCharacter)
  );
}

// The ids of the icons a payload names: a device's, a group's or a
// template device's `icon`.
function refOf(payload) {
  const id = payload?.icon;

  return id === undefined || id === null || id === '' ? null : id;
}

/**
 * The custom icons a document uses: those its devices, its groups and the
 * devices of its templates name.
 *
 * @param {object} doc
 * @returns {Set<string>} icon ids, in the order they are first named
 */
export function iconRefs(doc) {
  const refs = new Set();
  const add = (payload) => {
    const id = refOf(payload);

    if (id !== null) {
      refs.add(id);
    }
  };

  for (const node of doc?.nodes || []) {
    add(node?.device);
    add(node?.group);
  }

  for (const template of doc?.templates || []) {
    add(template?.device);
  }

  return refs;
}

// Entries already found to be the icons their ids say, by id: the data
// each was checked with (as checkedIcons in validate.js).
const acceptedData = new Map();
const MAX_ACCEPTED = 256;

// The entry a document takes for an icon it comes to use: its data, when
// that is a PNG the Builder accepts whose id is `id`, with its name when it
// is one an icon may have. Null for anything else, so nothing a document
// would be refused for is ever copied into one.
function acceptedEntry(id, entry) {
  const data = entry?.data;

  if (typeof id !== 'string' || !ICON_ID.test(id) || typeof data !== 'string') {
    return null;
  }

  if (acceptedData.get(id) !== data) {
    const bytes = decodeIconData(data);

    if (!bytes || iconPNGProblem(bytes) !== '' || iconId(bytes) !== id) {
      return null;
    }

    if (acceptedData.size >= MAX_ACCEPTED) {
      acceptedData.clear();
    }

    acceptedData.set(id, data);
  }

  const name = entry.name;

  return name && isIconName(name) ? { name, data } : { data };
}

function knownEntry(known, id) {
  if (!known || typeof id !== 'string') {
    return undefined;
  }

  if (known instanceof Map) {
    return known.get(id);
  }

  return Object.hasOwn(known, id) ? known[id] : undefined;
}

// A payload without its custom icon, when that is one of `ids`; the same
// payload otherwise.
function withoutIcon(payload, ids) {
  if (!payload || !ids.has(refOf(payload))) {
    return payload;
  }

  const rest = { ...payload };

  delete rest.icon;

  return rest;
}

/**
 * Makes a document carry exactly the custom icons it uses, so it can be
 * saved: every commit of the store does this (see commit in store.js), and
 * so does a paste.
 *
 * Icons nothing names any more are removed. An icon the document names
 * without carrying it is copied from `known`, while the document has fewer
 * than MAX_DOCUMENT_ICONS; one that `known` lacks, or holds as anything but
 * the PNG of that id, or that is one too many, is left out: the nodes and
 * templates that name it lose their custom icon and show their built-in
 * one. A document left with no icon has no `icons`.
 *
 * @param {object} doc
 * @param {Map<string, object>|object|null} [known] icons by id, {name?,
 *   data}: those the session has seen (the store's icon shelf), or those a
 *   clipboard carries
 * @returns {{doc: object, dropped: number}} the document, the same object
 *   when nothing changed, and how many icons were left out
 */
export function settleIcons(doc, known) {
  const refs = iconRefs(doc);
  const carried =
    doc?.icons && typeof doc.icons === 'object' && !Array.isArray(doc.icons)
      ? doc.icons
      : null;
  const ids = carried ? Object.keys(carried) : [];

  // Nothing to do, as after most edits: every icon in use is carried under
  // its id, and nothing else is.
  if (
    ids.length === refs.size &&
    ids.every((id) => refs.has(id) && ICON_ID.test(id)) &&
    (ids.length > 0 || doc?.icons === undefined)
  ) {
    return { doc, dropped: 0 };
  }

  // Only what an icon id names is carried on: a key that is none, which
  // only an edited file can hold, goes, and the nodes that name it lose it.
  const icons = {};

  for (const id of ids) {
    if (refs.has(id) && ICON_ID.test(id)) {
      icons[id] = carried[id];
    }
  }

  const dropped = new Set();
  let count = Object.keys(icons).length;

  for (const id of refs) {
    if (Object.hasOwn(icons, id)) {
      continue;
    }

    const entry =
      count < MAX_DOCUMENT_ICONS
        ? acceptedEntry(id, knownEntry(known, id))
        : null;

    if (entry) {
      icons[id] = entry;
      count += 1;
    } else {
      dropped.add(id);
    }
  }

  const next = { ...doc };

  delete next.icons;

  if (dropped.size > 0) {
    next.nodes = (doc.nodes || []).map((node) => {
      const device = withoutIcon(node.device, dropped);
      const group = withoutIcon(node.group, dropped);

      if (device === node.device && group === node.group) {
        return node;
      }

      return {
        ...node,
        ...(node.device ? { device } : {}),
        ...(node.group ? { group } : {}),
      };
    });

    if (Array.isArray(doc.templates)) {
      next.templates = doc.templates.map((template) => {
        const device = withoutIcon(template?.device, dropped);

        return device === template?.device ? template : { ...template, device };
      });
    }
  }

  if (count > 0) {
    next.icons = icons;
  }

  return { doc: next, dropped: dropped.size };
}

/**
 * What an edit that left custom icons out says of them, after what it says
 * of itself.
 *
 * @param {number} dropped how many icons settleIcons left out
 * @returns {string} '' for none
 */
export function droppedIconsNote(dropped) {
  if (!(dropped > 0)) {
    return '';
  }

  return `${dropped === 1 ? '1 custom icon was' : `${dropped} custom icons were`} left out: a diagram holds at most ${MAX_DOCUMENT_ICONS}.`;
}

// What the upload dialog says of a file it cannot make an icon of.
export const ICON_FILE_TOO_LARGE = 'The image file is larger than 5 MiB.';
export const ICON_FILE_NOT_IMAGE =
  'This file is not an image the browser can read. Use a PNG, JPEG, GIF, WebP or SVG file.';
export const ICON_FILE_NOT_CONVERTED =
  'This image could not be converted. Save it as a PNG and upload it again.';

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
 * The name an icon made from a file gets: the file's name without its
 * extension and without control characters, trimmed, and cut to
 * MAX_ICON_NAME_BYTES bytes between two characters.
 *
 * @param {string} fileName
 * @returns {string} '' for a name with nothing left
 */
export function iconNameFromFile(fileName) {
  const base = [...String(fileName ?? '').replace(/\.[^.]*$/, '')]
    .filter((character) => !isControlCharacter(character))
    .join('')
    .trim();
  let name = '';
  let length = 0;

  for (const character of base) {
    length += utf8Length(character);

    if (length > MAX_ICON_NAME_BYTES) {
      break;
    }

    name += character;
  }

  return name.trim();
}

/**
 * The size an image is drawn at to make an icon of it: inside
 * MAX_ICON_PIXELS a side, in its own proportions, each side at least one
 * pixel. A bitmap is never enlarged; a vector image, which has no pixels
 * of its own, is drawn as large as an icon may be. An image that states no
 * size (an SVG without one) counts as square.
 *
 * @param {number} width the image's own width, 0 for none
 * @param {number} height the image's own height, 0 for none
 * @param {boolean} [vector] the image is an SVG
 * @returns {{width: number, height: number}}
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
 * Makes an icon of an image file: a PNG of at most MAX_ICON_PIXELS a side,
 * as the browser draws the image.
 *
 * The file is given to the browser as an image and to nothing else: it is
 * loaded into an <img> that is never in the page, where a script or an
 * outside reference in an SVG does nothing, drawn into a canvas, and read
 * back as PNG. Whatever the file held besides its picture stays behind.
 * What makes the file an image is that the browser can draw it; its name
 * says only whether it is an SVG, which has no other mark.
 *
 * @param {File|Blob} file with a `name` when it is a file
 * @param {object} [deps] createObjectURL, revokeObjectURL, loadImage,
 *   createCanvas and BlobCtor, in place of the browser's
 * @returns {Promise<{name: string, data: string}>} the icon's name, from
 *   the file's, and its PNG in base64, to send to the icon library
 * @throws {IconFileError} for a file too large to read, one the browser
 *   cannot read as an image, and an image it cannot turn into a PNG
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
  // A file with no type is still an image to the browser by its content,
  // but for an SVG, which it knows by its type alone.
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
