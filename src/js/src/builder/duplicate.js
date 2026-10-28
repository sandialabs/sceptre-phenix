// Duplicate draft: a new draft with a copy of a draft's diagram as it is
// now, and none of its history. The copy is linked to no config and to no
// publication of the draft it copies, so publishing it makes a new
// topology. Its name says it is a copy: "<name> (copy)", or "<name> (copy
// 2)" and on when a draft has that name.

import { newId } from './ids.js';
import { STAMP_KEYS, metadataOf } from './model.js';
import { utf8Length } from './text.js';
import { MAX_NAME_BYTES } from './validate.js';

// A name that ends in a copy's suffix: " (copy)" or " (copy 3)".
const COPY_SUFFIX = /\s\(copy(?: \d+)?\)$/;

// `base` cut, by whole characters, so that `base` and `suffix` fit in the
// name's limit.
function fitted(base, suffix) {
  const room = MAX_NAME_BYTES - utf8Length(suffix);
  const characters = [...base];

  while (characters.length && utf8Length(characters.join('')) > room) {
    characters.pop();
  }

  return `${characters.join('').trimEnd()}${suffix}`;
}

/**
 * The name of a copy of a diagram that no draft has, ignoring case. A copy of
 * a copy is numbered ("Lab (copy 2)"), and a long name is cut to fit.
 */
export function copyName(name, taken = []) {
  const base =
    String(name || '')
      .trim()
      .replace(COPY_SUFFIX, '')
      .trim() || 'Untitled diagram';
  const used = new Set(
    [...taken].map((entry) =>
      String(entry || '')
        .trim()
        .toLowerCase(),
    ),
  );

  for (let n = 1; ; n += 1) {
    const candidate = fitted(base, n === 1 ? ' (copy)' : ` (copy ${n})`);

    if (!used.has(candidate.toLowerCase())) {
      return candidate;
    }
  }
}

/**
 * The diagram of a duplicate: `doc` with a new id and name, without creator
 * and editor fields, and with a source that names no config. `doc` does not
 * change.
 */
export function duplicateDocument(doc, name) {
  const metadata = { ...metadataOf(doc), id: newId(), name };

  for (const key of STAMP_KEYS) {
    delete metadata[key];
  }

  const { includeTopologies, unresolvedIncludes } = doc?.source || {};
  const kept = (list) => (Array.isArray(list) ? list.filter(Boolean) : []);

  return {
    ...doc,
    metadata,
    source: {
      kind: 'manual',
      ...(kept(includeTopologies).length
        ? { includeTopologies: kept(includeTopologies) }
        : {}),
      ...(kept(unresolvedIncludes).length
        ? { unresolvedIncludes: kept(unresolvedIncludes) }
        : {}),
    },
  };
}
