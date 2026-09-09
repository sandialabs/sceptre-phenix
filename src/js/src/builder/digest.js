// Content digests for scenario references.
//
// The server requires every ScenarioRef to carry a digest of the form
// `sha256:<64 hex>` over the scenario content, produced by Go's
// `json.Marshal` + SHA-256. Go marshals maps with sorted keys and HTML-escapes
// `<`, `>` and `&`, so the canonical form below reproduces that byte for byte;
// otherwise the server would reject a document the user cannot fix.

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

function escapeGoString(text) {
  return text
    .replace(/</g, '\\u003c')
    .replace(/>/g, '\\u003e')
    .replace(/&/g, '\\u0026')
    .replace(/\u2028/g, '\\u2028')
    .replace(/\u2029/g, '\\u2029');
}

/**
 * Serializes a value the way Go's encoding/json would: object keys sorted,
 * HTML escaped, no insignificant whitespace.
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
      .sort();

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

function toHex(buffer) {
  return Array.from(new Uint8Array(buffer))
    .map((byte) => byte.toString(16).padStart(2, '0'))
    .join('');
}

/**
 * Computes the content digest of a scenario spec.
 *
 * @param {object} content
 * @param {object} [crypto] SubtleCrypto host, for tests
 * @returns {Promise<string>} `sha256:<hex>`, or '' when content is empty
 */
export async function contentDigest(content, crypto = globalThis.crypto) {
  if (!content) {
    return '';
  }

  const bytes = new TextEncoder().encode(canonicalJSON(content));
  const hash = await crypto.subtle.digest('SHA-256', bytes);

  return DIGEST_PREFIX + toHex(hash);
}
