// Content digests for scenario references.
//
// The server requires every ScenarioRef to carry a digest of the form
// `sha256:<64 hex>` over the scenario content, produced by Go's
// `json.Marshal` + SHA-256. Go marshals maps with sorted keys and HTML-escapes
// `<`, `>` and `&`, so the canonical form below reproduces that byte for byte;
// otherwise the server would reject a document the user cannot fix.

import { toRaw } from 'vue';

const DIGEST_PREFIX = 'sha256:';
const DIGEST_PATTERN = /^sha256:[0-9a-f]{64}$/;

/**
 * True when a digest has the shape the server accepts.
 *
 * @param {string} digest
 * @returns {boolean}
 */
export function isDigest(digest) {
  return typeof digest === 'string' && DIGEST_PATTERN.test(digest);
}

// Orders strings by Unicode code point, which is the order of their UTF-8
// bytes and so the order Go sorts map keys in. A plain sort compares UTF-16
// code units instead, which puts a character above U+FFFF (such as an emoji)
// before one in U+E000 to U+FFFF (such as fullwidth forms).
function compareCodePoints(a, b) {
  let index = 0;

  while (index < a.length && index < b.length) {
    const x = a.codePointAt(index);
    const y = b.codePointAt(index);

    if (x !== y) {
      return x - y;
    }

    index += x > 0xffff ? 2 : 1;
  }

  return a.length - b.length;
}

function escapeGoString(text) {
  return text
    .replace(/</g, '\\u003c')
    .replace(/>/g, '\\u003e')
    .replace(/&/g, '\\u0026')
    .replace(/\u2028/g, '\\u2028')
    .replace(/\u2029/g, '\\u2029');
}

/**
 * Serializes a value the way Go's encoding/json would: object keys sorted by
 * code point, HTML escaped, no insignificant whitespace.
 *
 * @param {*} value
 * @returns {string}
 */
export function canonicalJSON(value) {
  if (value === null || value === undefined) {
    return 'null';
  }

  if (Array.isArray(value)) {
    return `[${value.map((item) => canonicalJSON(item)).join(',')}]`;
  }

  if (typeof value === 'object') {
    const keys = Object.keys(value)
      .filter((key) => value[key] !== undefined)
      .sort(compareCodePoints);

    const body = keys
      .map(
        (key) =>
          `${escapeGoString(JSON.stringify(key))}:${canonicalJSON(value[key])}`,
      )
      .join(',');

    return `{${body}}`;
  }

  if (typeof value === 'string') {
    return escapeGoString(JSON.stringify(value));
  }

  return JSON.stringify(value);
}

// SHA-256 round constants (FIPS 180-4, section 4.2.2).
const K = new Uint32Array([
  0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1,
  0x923f82a4, 0xab1c5ed5, 0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3,
  0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174, 0xe49b69c1, 0xefbe4786,
  0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
  0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147,
  0x06ca6351, 0x14292967, 0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13,
  0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85, 0xa2bfe8a1, 0xa81a664b,
  0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
  0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a,
  0x5b9cca4f, 0x682e6ff3, 0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208,
  0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
]);

function rotr(value, bits) {
  return (value >>> bits) | (value << (32 - bits));
}

/**
 * SHA-256 of `bytes` as lowercase hex. Validation runs synchronously in store
 * getters, where SubtleCrypto (asynchronous, and absent outside secure
 * contexts such as a UI served over plain HTTP) cannot be used.
 *
 * @param {Uint8Array} bytes
 * @returns {string}
 */
export function sha256Hex(bytes) {
  const length = bytes.length;
  const padded = new Uint8Array((((length + 8) >> 6) + 1) << 6);
  padded.set(bytes);
  padded[length] = 0x80;

  const view = new DataView(padded.buffer);
  view.setUint32(padded.length - 8, Math.floor(length / 0x20000000));
  view.setUint32(padded.length - 4, (length << 3) >>> 0);

  const hash = new Uint32Array([
    0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c,
    0x1f83d9ab, 0x5be0cd19,
  ]);
  const words = new Uint32Array(64);

  for (let offset = 0; offset < padded.length; offset += 64) {
    for (let t = 0; t < 16; t += 1) {
      words[t] = view.getUint32(offset + t * 4);
    }

    for (let t = 16; t < 64; t += 1) {
      const s0 =
        rotr(words[t - 15], 7) ^
        rotr(words[t - 15], 18) ^
        (words[t - 15] >>> 3);
      const s1 =
        rotr(words[t - 2], 17) ^ rotr(words[t - 2], 19) ^ (words[t - 2] >>> 10);

      words[t] = words[t - 16] + s0 + words[t - 7] + s1;
    }

    let [a, b, c, d, e, f, g, h] = hash;

    for (let t = 0; t < 64; t += 1) {
      const s1 = rotr(e, 6) ^ rotr(e, 11) ^ rotr(e, 25);
      const choice = (e & f) ^ (~e & g);
      const t1 = (h + s1 + choice + K[t] + words[t]) >>> 0;
      const s0 = rotr(a, 2) ^ rotr(a, 13) ^ rotr(a, 22);
      const majority = (a & b) ^ (a & c) ^ (b & c);
      const t2 = (s0 + majority) >>> 0;

      h = g;
      g = f;
      f = e;
      e = (d + t1) >>> 0;
      d = c;
      c = b;
      b = a;
      a = (t1 + t2) >>> 0;
    }

    hash[0] += a;
    hash[1] += b;
    hash[2] += c;
    hash[3] += d;
    hash[4] += e;
    hash[5] += f;
    hash[6] += g;
    hash[7] += h;
  }

  return Array.from(hash, (word) => word.toString(16).padStart(8, '0')).join(
    '',
  );
}

// Digests by content object. Validation runs on every edit and content can be
// megabytes, so an unchanged object is neither serialized nor hashed again.
// This relies on the document model never changing content in place: an edit
// replaces the object (see setScenario in model.js), and history snapshots
// share it. Keys are raw objects, so a reactive proxy and its target share an
// entry.
const digests = new WeakMap();

/**
 * Computes the content digest of a scenario spec synchronously. The result is
 * cached by content object, which must be treated as immutable.
 *
 * @param {object} content
 * @returns {string} `sha256:<hex>`, or '' when content is empty
 */
export function contentDigestSync(content) {
  if (!content) {
    return '';
  }

  const raw = toRaw(content);
  const cacheable = typeof raw === 'object';

  if (cacheable && digests.has(raw)) {
    return digests.get(raw);
  }

  const digest =
    DIGEST_PREFIX + sha256Hex(new TextEncoder().encode(canonicalJSON(raw)));

  if (cacheable) {
    digests.set(raw, digest);
  }

  return digest;
}

/**
 * Computes the content digest of a scenario spec.
 *
 * @param {object} content
 * @returns {Promise<string>} `sha256:<hex>`, or '' when content is empty
 */
export async function contentDigest(content) {
  return contentDigestSync(content);
}
