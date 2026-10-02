// Values this browser remembers in localStorage. Storage can be missing or
// throw (private windows, blocked site data); reads then find nothing and
// writes are dropped, so a value lasts only for the visit.

// the stored string, or null
export function readPref(key) {
  try {
    return globalThis.localStorage?.getItem(key) ?? null;
  } catch {
    return null;
  }
}

export function writePref(key, value) {
  try {
    globalThis.localStorage?.setItem(key, String(value));
  } catch {
    // see above
  }
}

export function removePref(key) {
  try {
    globalThis.localStorage?.removeItem(key);
  } catch {
    // see above
  }
}
