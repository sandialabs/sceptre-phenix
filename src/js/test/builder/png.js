// PNG files for the custom icon tests, built chunk by chunk so a test can
// make one the Builder must refuse.

import { Buffer } from 'node:buffer';
import { crc32, deflateSync } from 'node:zlib';

// A 1 by 1 PNG of 70 bytes, in base64, and its icon id. customicons_test.go
// uses the same icon.
export const ICON_DATA =
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==';
export const ICON_KEY =
  'sha256:497790947d4666760ce38f3c00e852c71fdb66cae849bae8e9ede352719e1581';

export const SIGNATURE = Uint8Array.of(137, 80, 78, 71, 13, 10, 26, 10);

/**
 * @param {string} text base64
 * @returns {Uint8Array}
 */
export function bytesOf(text) {
  return new Uint8Array(Buffer.from(text, 'base64'));
}

/**
 * @param {Uint8Array} bytes
 * @returns {string} base64
 */
export function base64Of(bytes) {
  return Buffer.from(bytes).toString('base64');
}

/**
 * @param {...(Uint8Array|number[])} parts
 * @returns {Uint8Array} the parts, one after another
 */
export function concat(...parts) {
  return new Uint8Array(Buffer.concat(parts.map((part) => Buffer.from(part))));
}

/**
 * One PNG chunk: the length of its data, its type, the data, and the
 * checksum of the type and the data.
 *
 * @param {string} kind
 * @param {Uint8Array|number[]} [data]
 * @returns {Uint8Array}
 */
export function chunk(kind, data = []) {
  const body = Buffer.concat([Buffer.from(kind, 'latin1'), Buffer.from(data)]);
  const length = Buffer.alloc(4);
  const checksum = Buffer.alloc(4);

  length.writeUInt32BE(data.length);
  checksum.writeUInt32BE(crc32(body));

  return new Uint8Array(Buffer.concat([length, body, checksum]));
}

// The color types of a PNG header.
export const GRAY = 0;
export const RGB = 2;
export const GRAY_ALPHA = 4;
export const RGBA = 6;

/**
 * The header chunk of an image, of 8 bits a channel with alpha unless told
 * otherwise.
 *
 * @param {number} width
 * @param {number} height
 * @param {number} [depth] bits a channel
 * @param {number} [colorType]
 * @returns {Uint8Array}
 */
export function header(width, height, depth = 8, colorType = RGBA) {
  const data = Buffer.alloc(13);

  data.writeUInt32BE(width, 0);
  data.writeUInt32BE(height, 4);
  data.set([depth, colorType, 0, 0, 0], 8);

  return chunk('IHDR', data);
}

// The checksum that ends a compressed stream: Adler-32 of the bytes it
// holds.
function adler32(bytes) {
  let [low, high] = [1, 0];

  for (const byte of bytes) {
    low = (low + byte) % 65521;
    high = (high + low) % 65521;
  }

  return ((high << 16) | low) >>> 0;
}

/**
 * Raw pixel bytes as the data of a pixel chunk, stored without compression:
 * an empty block and then the bytes in one block, so the data is 15 bytes
 * longer than they are. customicons_test.go stores pixels the same way.
 *
 * @param {Uint8Array|number[]} raw fewer bytes than a block holds, 65536
 * @returns {Uint8Array}
 */
export function stored(raw) {
  const bytes = Buffer.from(raw);
  // The header of the last block: its length and the complement of it.
  const block = Buffer.alloc(5, 1);
  const checksum = Buffer.alloc(4);

  block.writeUInt16LE(bytes.length, 1);
  block.writeUInt16LE(~bytes.length & 0xffff, 3);
  checksum.writeUInt32BE(adler32(bytes));

  return concat(
    // The stream's header, and a block of no bytes that is not the last.
    [0x78, 0x01, 0, 0, 0, 0xff, 0xff],
    block,
    bytes,
    checksum,
  );
}

/**
 * A 1 by 1 PNG of 8 bits a channel whose pixel is the given bytes, with
 * chunks put between its header and its pixels.
 *
 * @param {number} colorType
 * @param {number[]} pixel
 * @param {...Uint8Array} before
 * @returns {Uint8Array}
 */
export function onePixel(colorType, pixel, ...before) {
  return concat(
    SIGNATURE,
    header(1, 1, 8, colorType),
    ...before,
    // A row starts with its filter: none.
    chunk('IDAT', stored([0, ...pixel])),
    chunk('IEND'),
  );
}

/**
 * A PNG of one color.
 *
 * @param {number} width
 * @param {number} height
 * @param {number[]} [pixel] red, green, blue and alpha
 * @returns {Uint8Array}
 */
export function png(width, height, pixel = [0, 0, 0, 0]) {
  const row = [0, ...Array(width).fill(pixel).flat()];
  const pixels = deflateSync(Buffer.from(Array(height).fill(row).flat()));

  return concat(
    SIGNATURE,
    header(width, height),
    chunk('IDAT', pixels),
    chunk('IEND'),
  );
}

// The three chunks of the fixture icon: its header, its pixels and its end.
const FIXTURE = bytesOf(ICON_DATA);

export const FIXTURE_CHUNKS = [
  FIXTURE.subarray(8, 33),
  FIXTURE.subarray(33, 58),
  FIXTURE.subarray(58, 70),
];

// The largest icon there is, to the byte (40960): 81 by 63 transparent
// pixels of 16 bits a channel, stored without compression, each row its
// filter and eight bytes a pixel. customicons_test.go and the validation
// corpus hold the same icon.
export const LARGEST_KEY =
  'sha256:6620402302a9ebd8e78490436e065649504fa7d02922d0432905f04debb0c5a2';

/** @returns {Uint8Array} */
export function largest() {
  return concat(
    SIGNATURE,
    header(81, 63, 16, RGBA),
    chunk('IDAT', stored(new Uint8Array(63 * (1 + 8 * 81)))),
    chunk('IEND'),
  );
}
