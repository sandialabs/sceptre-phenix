// Small checks and copies of JSON values, and the field checks that the
// template and layer set file readers share.

import { utf8Length } from './text.js';

/** Whether a value is an object that is not null and not an array. */
export function isPlainObject(value) {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value);
}

/**
 * A copy of a JSON value that shares nothing with it. It is a JSON round
 * trip, so undefined keys drop out and a Date becomes its ISO text.
 */
export function deepCopy(value) {
  return JSON.parse(JSON.stringify(value));
}

/** name followed by " (n)", shortened by whole characters to fit maxBytes. */
export function numberedName(name, n, maxBytes) {
  const suffix = ` (${n})`;
  const characters = [...name];

  while (
    characters.length &&
    utf8Length(characters.join('') + suffix) > maxBytes
  ) {
    characters.pop();
  }

  return `${characters.join('').trimEnd()}${suffix}`;
}

/**
 * The checks of a file reader that refuse what the server's strict decoding
 * refuses. Each throws a FileError with "<path>: <problem>."
 */
export function fileFieldChecks(FileError) {
  return {
    // Refuses a value that is not an object, or a key not in allowed.
    rejectUnknown(value, allowed, path) {
      if (!isPlainObject(value)) {
        throw new FileError(`${path}: expected an object.`);
      }

      for (const key of Object.keys(value)) {
        if (!allowed.has(key)) {
          throw new FileError(`${path}: unknown field "${key}".`);
        }
      }
    },

    // Refuses a value of text that is not text. Null is no value.
    rejectNonText(value, path) {
      if (value !== undefined && value !== null && typeof value !== 'string') {
        throw new FileError(`${path}: expected text.`);
      }
    },
  };
}
