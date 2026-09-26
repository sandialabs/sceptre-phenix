// Identifier helpers.
//
// Editor entities (documents, nodes, networks, edges, interface handles) are
// identified by random UUIDs, matching the server contract: identifiers are
// opaque, stable, and never derived from labels.

function hexOf(bytes) {
  return Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join(
    '',
  );
}

/**
 * A new random identifier. Browsers offer crypto.randomUUID only in a secure
 * context (HTTPS or localhost), and phenix is often served over plain HTTP,
 * so the UUID is built from crypto.getRandomValues, which works everywhere,
 * when randomUUID is missing.
 *
 * @returns {string} a new RFC 4122 v4 UUID
 * @throws {Error} when the browser has no cryptographic random source
 */
export function newId() {
  const source = globalThis.crypto;

  if (typeof source?.randomUUID === 'function') {
    return source.randomUUID();
  }

  if (typeof source?.getRandomValues !== 'function') {
    throw new Error('This browser cannot generate random identifiers.');
  }

  const bytes = source.getRandomValues(new Uint8Array(16));

  // Version 4 and the RFC 4122 variant (section 4.4).
  bytes[6] = (bytes[6] & 0x0f) | 0x40;
  bytes[8] = (bytes[8] & 0x3f) | 0x80;

  const hex = hexOf(bytes);

  return [
    hex.slice(0, 8),
    hex.slice(8, 12),
    hex.slice(12, 16),
    hex.slice(16, 20),
    hex.slice(20),
  ].join('-');
}

/**
 * Returns a name that does not collide with `taken`, appending -2, -3, ... .
 *
 * @param {string} base desired name
 * @param {Iterable<string>} taken already used names
 * @param {(value: string) => string} [normalize] comparison normalization
 * @param {string} [separator] put between the name and its number
 * @returns {string}
 */
export function uniqueName(
  base,
  taken,
  normalize = (value) => value,
  separator = '-',
) {
  const used = new Set([...(taken || [])].map(normalize));

  if (!used.has(normalize(base))) {
    return base;
  }

  let n = 2;

  while (used.has(normalize(`${base}${separator}${n}`))) {
    n += 1;
  }

  return `${base}${separator}${n}`;
}

/**
 * Deterministic, non-cryptographic hash used for stable presentation choices
 * (network color tokens, dash patterns). Never used for identity.
 *
 * @param {string} value
 * @returns {number} non-negative integer
 */
export function stableHash(value) {
  const str = String(value ?? '');
  let hash = 5381;

  for (let i = 0; i < str.length; i += 1) {
    hash = ((hash << 5) + hash + str.charCodeAt(i)) | 0;
  }

  return Math.abs(hash);
}
