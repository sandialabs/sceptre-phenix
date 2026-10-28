// Identifier helpers.
//
// Editor entities (documents, nodes, networks, edges, interface handles) have
// random UUIDs as identifiers, as the server contract requires. Identifiers
// are opaque and stable, and never come from labels.

function hexOf(bytes) {
  return Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join(
    '',
  );
}

/**
 * A new RFC 4122 v4 UUID. crypto.randomUUID needs a secure context, and phenix
 * often runs over plain HTTP, so it falls back to getRandomValues.
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
 * A name that does not collide with `taken`: base, then base-2, base-3 and so
 * on.
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
 * A non-cryptographic hash for stable presentation choices, never for identity.
 */
export function stableHash(value) {
  const str = String(value ?? '');
  let hash = 5381;

  for (let i = 0; i < str.length; i += 1) {
    hash = ((hash << 5) + hash + str.charCodeAt(i)) | 0;
  }

  return Math.abs(hash);
}
