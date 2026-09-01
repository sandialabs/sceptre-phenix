// Identifier helpers.
//
// Editor entities (documents, nodes, networks, edges, interface handles) are
// identified by UUIDs generated with crypto.randomUUID, matching the server
// contract: identifiers are opaque, stable, and never derived from labels.

/**
 * @returns {string} a new RFC 4122 v4 UUID
 * @throws {Error} when crypto.randomUUID is unavailable
 */
export function newId() {
  const source = globalThis.crypto;

  if (!source || typeof source.randomUUID !== 'function') {
    throw new Error(
      'crypto.randomUUID is unavailable; the builder requires a secure context.',
    );
  }

  return source.randomUUID();
}

/**
 * Returns a name that does not collide with `taken`, appending -2, -3, ... .
 *
 * @param {string} base desired name
 * @param {Iterable<string>} taken already used names
 * @returns {string}
 */
export function uniqueName(base, taken) {
  const used = taken instanceof Set ? taken : new Set(taken || []);

  if (!used.has(base)) {
    return base;
  }

  let n = 2;

  while (used.has(`${base}-${n}`)) {
    n += 1;
  }

  return `${base}-${n}`;
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
